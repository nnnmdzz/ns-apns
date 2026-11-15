package channels

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"ns-apns/internal/forward"
)

type Sender interface {
	Send(context.Context, forward.Event) error
}
type Status struct {
	LastAttempt time.Time
	LastSuccess time.Time
	Error       string
}
type Dispatcher struct {
	cfg       Config
	apns      Sender
	client    *http.Client
	log       *slog.Logger
	ctx       context.Context
	cancel    context.CancelFunc
	slots     chan struct{}
	mu        sync.Mutex
	statuses  map[string]Status
	wecomMu   sync.Mutex
	barkMu    sync.Mutex
	statePath string
	delivered []string
	nextWeCom time.Time
	interval  time.Duration
}

func New(cfg Config, apns Sender, log *slog.Logger) (*Dispatcher, error) {
	cfg.Normalize()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if apns == nil && !cfg.WeCom.Enabled && !cfg.Bark.Enabled {
		return nil, nil
	}
	if log == nil {
		log = slog.Default()
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Dispatcher{cfg: cfg, apns: apns, log: log, ctx: ctx, cancel: cancel, slots: make(chan struct{}, 128), statuses: map[string]Status{}, interval: 3100 * time.Millisecond,
		client: &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (s *Dispatcher) Close() {
	if s != nil {
		s.cancel()
	}
}
func (s *Dispatcher) Statuses() map[string]Status {
	out := map[string]Status{}
	if s == nil {
		return out
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, v := range s.statuses {
		out[k] = v
	}
	return out
}
func (s *Dispatcher) Send(ctx context.Context, ev forward.Event) error {
	if s == nil {
		return nil
	}
	names := []string{}
	if s.apns != nil {
		names = append(names, "apns")
	}
	if s.cfg.WeCom.Enabled {
		names = append(names, "wecom")
	}
	if s.cfg.Bark.Enabled {
		names = append(names, "bark")
	}
	var wg sync.WaitGroup
	errs := make(chan error, len(names))
	for _, name := range names {
		wg.Go(func() {
			if err := s.SendChannel(ctx, name, ev); err != nil {
				errs <- err
			}
		})
	}
	wg.Wait()
	close(errs)
	var all []error
	for err := range errs {
		all = append(all, err)
	}
	return errors.Join(all...)
}
func (s *Dispatcher) SendChannel(parent context.Context, name string, ev forward.Event) error {
	if s == nil {
		return fmt.Errorf("请先保存并启用推送渠道")
	}
	ctx, cancel := context.WithTimeout(parent, 8*time.Minute)
	defer cancel()
	stop := context.AfterFunc(s.ctx, cancel)
	defer stop()
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	default:
		return fmt.Errorf("推送等待队列已满，请稍后重试")
	}
	var err error
	switch name {
	case "apns":
		if s.apns == nil {
			return fmt.Errorf("尚未配置 APNs")
		}
		err = s.apns.Send(ctx, ev)
	case "wecom":
		if !s.cfg.WeCom.Enabled {
			return fmt.Errorf("请先保存并启用企业微信机器人")
		}
		s.wecomMu.Lock()
		defer s.wecomMu.Unlock()
		if s.wasDelivered(name, ev) {
			return nil
		}
		err = s.deliver(ctx, name, ev)
	case "bark":
		if !s.cfg.Bark.Enabled {
			return fmt.Errorf("请先保存并启用 Bark")
		}
		s.barkMu.Lock()
		defer s.barkMu.Unlock()
		if s.wasDelivered(name, ev) {
			return nil
		}
		err = s.deliver(ctx, name, ev)
	default:
		return fmt.Errorf("未知推送渠道")
	}
	s.mu.Lock()
	state := s.statuses[name]
	state.LastAttempt = time.Now()
	state.Error = ""
	if err == nil {
		state.LastSuccess = state.LastAttempt
	} else {
		state.Error = err.Error()
	}
	s.statuses[name] = state
	if err == nil && ev.TelegramMessageID != 0 {
		s.delivered = append(s.delivered, deliveryKey(name, ev))
		if len(s.delivered) > 512 {
			s.delivered = s.delivered[len(s.delivered)-512:]
		}
	}
	if persistErr := s.persistLocked(); persistErr != nil {
		s.log.Warn("投递状态无法保存，请检查工作目录权限")
	}
	s.mu.Unlock()
	if err != nil {
		s.log.Warn("通知投递失败", "channel", name, "error", err.Error())
	} else {
		s.log.Info("通知已投递", "channel", name)
	}
	return err
}
func (s *Dispatcher) deliver(ctx context.Context, name string, ev forward.Event) error {
	endpoint, payload := s.message(name, ev)
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("通知内容无法编码")
	}
	for attempt := 0; attempt < 3; attempt++ {
		if name == "wecom" {
			if err := wait(ctx, time.Until(s.nextWeCom)); err != nil {
				return err
			}
			s.nextWeCom = time.Now().Add(s.interval)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
		if err != nil {
			return fmt.Errorf("通知请求无效")
		}
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
		if name == "bark" && s.cfg.Bark.Username != "" {
			req.SetBasicAuth(s.cfg.Bark.Username, s.cfg.Bark.Password)
		}
		res, err := s.client.Do(req)
		if err != nil {
			return fmt.Errorf("%s 请求未确认，请先核对通知再重试", label(name))
		}
		body, readErr := io.ReadAll(io.LimitReader(res.Body, 65537))
		res.Body.Close()
		if readErr != nil || len(body) > 65536 {
			return fmt.Errorf("%s 响应异常，请先核对通知", label(name))
		}
		var result struct {
			Code    *int `json:"code"`
			ErrCode *int `json:"errcode"`
		}
		decodeErr := json.Unmarshal(body, &result)
		code := result.Code
		success := 200
		if name == "wecom" {
			code = result.ErrCode
			success = 0
		}
		retry := res.StatusCode == 429 || (res.StatusCode == 200 && decodeErr == nil && code != nil && name == "wecom" && (*code == 45009 || *code == 45033 || *code == -1))
		if retry && attempt < 2 {
			if err := wait(ctx, time.Duration(attempt+1)*3*time.Second); err != nil {
				return err
			}
			continue
		}
		if res.StatusCode < 200 || res.StatusCode >= 300 {
			return fmt.Errorf("%s 服务拒绝请求（HTTP %d）", label(name), res.StatusCode)
		}
		if decodeErr != nil || code == nil {
			return fmt.Errorf("%s 响应格式有误，请先核对通知", label(name))
		}
		if *code != success {
			return fmt.Errorf("%s 拒绝通知（错误码 %d），请检查渠道配置", label(name), *code)
		}
		return nil
	}
	return fmt.Errorf("%s 请求频繁，请稍后重试", label(name))
}
func (s *Dispatcher) message(name string, ev forward.Event) (string, map[string]any) {
	title, body := copyFor(ev)
	link := safeSiteURL(ev.URL)
	if name == "wecom" {
		suffix := ""
		if link != "" {
			suffix = "\n\n[查看详情](" + strings.NewReplacer("(", "%28", ")", "%29").Replace(link) + ")"
		}
		head := "### " + markdown(clip(title, 240)) + "\n"
		text := head + clip(markdown(body), 4096-len(head)-len(suffix)) + suffix
		return s.cfg.WeCom.WebhookURL, map[string]any{"msgtype": "markdown", "markdown": map[string]any{"content": text}}
	}
	target := url.URL{Scheme: "nsconnect", Host: "notification"}
	query := url.Values{"type": {ev.Type}}
	if link != "" {
		query.Set("url", link)
	}
	target.RawQuery = query.Encode()
	if len(target.String()) > 1600 {
		query.Del("url")
		target.RawQuery = query.Encode()
	}
	id := ""
	if ev.TelegramMessageID != 0 {
		sum := sha256.Sum256([]byte(fmt.Sprintf("%s/%d", ev.Source, ev.TelegramMessageID)))
		id = "ns-" + hex.EncodeToString(sum[:16])
	}
	payload := map[string]any{"device_key": s.cfg.Bark.DeviceKey, "title": clip(title, 240), "body": clip(body, 2500), "group": s.cfg.Bark.Group, "level": s.cfg.Bark.Level, "url": target.String(), "isArchive": 1}
	if id != "" {
		payload["id"] = id
	}
	for {
		encoded, _ := json.Marshal(payload)
		if len(encoded) <= 3500 {
			break
		}
		body := payload["body"].(string)
		if body == "" {
			break
		}
		payload["body"] = clip(body, max(0, len(body)-(len(encoded)-3500)-6))
	}
	return s.cfg.Bark.ServerURL + "/push", payload
}
func safeSiteURL(raw string) string {
	if len(raw) > 1800 {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || (u.Port() != "" && u.Port() != "443") {
		return ""
	}
	switch strings.ToLower(u.Hostname()) {
	case "nodeseek.com", "www.nodeseek.com", "deepflood.com", "www.deepflood.com":
		return u.String()
	}
	return ""
}
func copyFor(ev forward.Event) (string, string) {
	author := ev.Author
	if author == "" {
		author = "有人"
	}
	title := "NodeSeek 通知"
	switch ev.Type {
	case "reply":
		title = author + " 评论了你的帖子"
	case "at", "mention":
		title = author + " 提到了你"
	case "message", "dm":
		title = author + " 发来私信"
	case "checkin":
		title = "签到结果"
	case "inbox", "notification":
		title = "站内通知"
	}
	body := ev.RawText
	if body == "" {
		body = "你有一条新的 NodeSeek 通知"
	}
	return title, body
}
func markdown(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "*", "\\*", "[", "\\[", "]", "\\]", "`", "\\`", "#", "\\#").Replace(s)
}
func clip(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if len(s) <= n {
		return s
	}
	s = s[:max(0, n-3)]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s + "…"
}
func wait(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("推送已取消或等待超时")
	}
}
func label(name string) string {
	if name == "wecom" {
		return "企业微信"
	}
	return "Bark"
}
