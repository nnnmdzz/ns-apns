package channels

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"ns-apns/internal/forward"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(code int, body string) *http.Response {
	return &http.Response{StatusCode: code, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}
func configForTest() Config {
	return Config{WeCom: WeComConfig{true, "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=fake-wecom-key"}, Bark: BarkConfig{Enabled: true, ServerURL: "https://bark.example.com/prefix", DeviceKey: "fake-bark-key", Group: "NodeSeek", Level: "active"}}
}
func newTestDispatcher(t *testing.T) *Dispatcher {
	t.Helper()
	s, err := New(configForTest(), nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	s.interval = 0
	t.Cleanup(s.Close)
	return s
}
func TestIndependentProvidersAndJSON(t *testing.T) {
	s := newTestDispatcher(t)
	var mu sync.Mutex
	seen := map[string]map[string]any{}
	s.client.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		var p map[string]any
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			t.Error(err)
		}
		mu.Lock()
		seen[r.URL.Host] = p
		mu.Unlock()
		if r.Method != "POST" || !strings.Contains(r.Header.Get("Content-Type"), "application/json") {
			t.Error("not JSON POST")
		}
		if strings.Contains(r.URL.String(), "fake-bark-key") {
			t.Error("key exposed in URL")
		}
		if r.URL.Host == "qyapi.weixin.qq.com" {
			return response(200, `{"errcode":93000,"errmsg":"fake-wecom-key"}`), nil
		}
		return response(200, `{"code":200}`), nil
	})
	ev := forward.TestEvent("message")
	ev.TelegramMessageID = 42
	err := s.Send(context.Background(), ev)
	if err == nil || strings.Contains(err.Error(), "fake-wecom-key") {
		t.Fatalf("missing redacted failure: %v", err)
	}
	if len(seen) != 2 {
		t.Fatal("one provider blocked another")
	}
	bark := seen["bark.example.com"]
	if bark["device_key"] != "fake-bark-key" || bark["group"] != "NodeSeek" || bark["level"] != "active" || bark["id"] == "" || !strings.HasPrefix(bark["url"].(string), "nsconnect://notification?") {
		t.Fatal(bark)
	}
	states := s.Statuses()
	if states["bark"].LastSuccess.IsZero() || states["wecom"].Error == "" {
		t.Fatal(states)
	}
}
func TestWeComPayloadIsValidUTF8AndBounded(t *testing.T) {
	s := newTestDispatcher(t)
	ev := forward.TestEvent("message")
	ev.Author = strings.Repeat("用户", 1000)
	ev.RawText = strings.Repeat("🙂<[*", 2000)
	_, p := s.message("wecom", ev)
	text := p["markdown"].(map[string]any)["content"].(string)
	if len(text) > 4096 || !utf8.ValidString(text) || !strings.Contains(text, "[查看详情]") {
		t.Fatalf("invalid message: %d", len(text))
	}
	ev.URL = "https://evil.test/post-123-1"
	_, p = s.message("wecom", ev)
	if strings.Contains(p["markdown"].(map[string]any)["content"].(string), "evil.test") {
		t.Fatal("untrusted link")
	}
}
func TestAmbiguousNetworkIsNotRetried(t *testing.T) {
	s := newTestDispatcher(t)
	calls := 0
	s.client.Transport = roundTrip(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("network failure contains fake-bark-key")
	})
	err := s.SendChannel(context.Background(), "bark", forward.TestEvent("message"))
	if calls != 1 || err == nil || strings.Contains(err.Error(), "fake-bark-key") {
		t.Fatal(calls, err)
	}
}
func TestRedirectCannotLeakPayload(t *testing.T) {
	s := newTestDispatcher(t)
	calls := 0
	s.client.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		res := response(307, "")
		res.Header.Set("Location", "https://evil.test/collect")
		return res, nil
	})
	err := s.SendChannel(context.Background(), "bark", forward.TestEvent("message"))
	if calls != 1 || err == nil {
		t.Fatal(calls, err)
	}
}
func TestProviderResponseRequiresSuccessCode(t *testing.T) {
	for _, body := range []string{`{}`, `{"code":500}`, `not json`} {
		t.Run(body, func(t *testing.T) {
			s := newTestDispatcher(t)
			s.client.Transport = roundTrip(func(*http.Request) (*http.Response, error) { return response(200, body), nil })
			if s.SendChannel(context.Background(), "bark", forward.TestEvent("message")) == nil {
				t.Fatal("accepted invalid response")
			}
		})
	}
}
func TestRateLimitHasBoundedRetryAndCancellation(t *testing.T) {
	s := newTestDispatcher(t)
	calls := 0
	s.client.Transport = roundTrip(func(*http.Request) (*http.Response, error) { calls++; return response(429, `{"code":429}`), nil })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if s.SendChannel(ctx, "bark", forward.TestEvent("message")) == nil || calls != 1 {
		t.Fatal(calls)
	}
}
func TestConfirmedBusyRetriesThenSucceeds(t *testing.T) {
	s := newTestDispatcher(t)
	calls := 0
	s.client.Transport = roundTrip(func(*http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return response(200, `{"errcode":45009}`), nil
		}
		return response(200, `{"errcode":0}`), nil
	})
	if err := s.SendChannel(context.Background(), "wecom", forward.TestEvent("reply")); err != nil || calls != 2 {
		t.Fatal(calls, err)
	}
}
func TestSuccessfulDeliverySurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	s := newTestDispatcher(t)
	if err := s.LoadState(path); err != nil {
		t.Fatal(err)
	}
	calls := 0
	s.client.Transport = roundTrip(func(*http.Request) (*http.Response, error) { calls++; return response(200, `{"code":200}`), nil })
	ev := forward.TestEvent("message")
	ev.TelegramMessageID = 123
	if err := s.SendChannel(context.Background(), "bark", ev); err != nil {
		t.Fatal(err)
	}
	next := newTestDispatcher(t)
	next.client.Transport = s.client.Transport
	if err := next.LoadState(path); err != nil {
		t.Fatal(err)
	}
	if err := next.SendChannel(context.Background(), "bark", ev); err != nil || calls != 1 {
		t.Fatal(calls, err)
	}
	if next.Statuses()["bark"].LastSuccess.IsZero() {
		t.Fatal("status lost")
	}
}
func TestInvalidAddressesAndDisabledChannels(t *testing.T) {
	for _, raw := range []string{"http://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=x", "https://evil.test/cgi-bin/webhook/send?key=x", "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=x&key=y"} {
		c := configForTest()
		c.WeCom.WebhookURL = raw
		if c.Validate() == nil {
			t.Fatal(raw)
		}
	}
	c := configForTest()
	c.Bark.ServerURL = "https://user:secret@bark.test"
	if c.Validate() == nil {
		t.Fatal("userinfo accepted")
	}
	c = Config{}
	c.Normalize()
	s, err := New(c, nil, nil)
	if err != nil || s != nil {
		t.Fatal("disabled channels require APNs")
	}
}

func TestBarkPayloadBudgetAndBasicAuth(t *testing.T) {
	s := newTestDispatcher(t)
	s.cfg.Bark.Username = "owner"
	s.cfg.Bark.Password = "private-password"
	ev := forward.TestEvent("message")
	ev.RawText = strings.Repeat("<🙂", 2000)
	ev.URL = "https://www.nodeseek.com/notification#" + strings.Repeat("x", 1700)
	_, payload := s.message("bark", ev)
	data, _ := json.Marshal(payload)
	if len(data) > 3500 {
		t.Fatal(len(data))
	}
	s.client.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "owner" || pass != "private-password" {
			t.Error("missing isolated auth")
		}
		return response(200, `{"code":200}`), nil
	})
	if err := s.SendChannel(context.Background(), "bark", ev); err != nil {
		t.Fatal(err)
	}
}

func TestReplacingDispatcherKeepsBothSuccessfulRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	a, b := newTestDispatcher(t), newTestDispatcher(t)
	for _, s := range []*Dispatcher{a, b} {
		if err := s.LoadState(path); err != nil {
			t.Fatal(err)
		}
		s.client.Transport = roundTrip(func(*http.Request) (*http.Response, error) { return response(200, `{"code":200}`), nil })
	}
	ev := forward.TestEvent("message")
	ev.TelegramMessageID = 1
	if err := a.SendChannel(context.Background(), "bark", ev); err != nil {
		t.Fatal(err)
	}
	ev.TelegramMessageID = 2
	if err := b.SendChannel(context.Background(), "bark", ev); err != nil {
		t.Fatal(err)
	}
	next := newTestDispatcher(t)
	if err := next.LoadState(path); err != nil {
		t.Fatal(err)
	}
	ev.TelegramMessageID = 1
	if !next.wasDelivered("bark", ev) {
		t.Fatal("old delivery lost")
	}
	ev.TelegramMessageID = 2
	if !next.wasDelivered("bark", ev) {
		t.Fatal("new delivery lost")
	}
}
