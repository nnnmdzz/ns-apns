package web

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"ns-apns/internal/channels"
)

type ChannelPage struct {
	BarkUsername string
	BarkAuthSet  bool
	WeComEnabled bool
	WeComSet     bool
	BarkEnabled  bool
	BarkSet      bool
	BarkServer   string
	BarkGroup    string
	BarkLevel    string
	WeComStatus  channels.Status
	BarkStatus   channels.Status
}
type ChannelForm struct {
	Username  string
	Password  string
	ClearAuth bool
	Channel   string
	Enabled   bool
	Clear     bool
	Address   string
	Group     string
	Level     string
}

func channelPage(c channels.Config, states map[string]channels.Status) ChannelPage {
	return NewChannelPage(c, states)
}
func NewChannelPage(c channels.Config, states map[string]channels.Status) ChannelPage {
	c.Normalize()
	return ChannelPage{BarkUsername: c.Bark.Username, BarkAuthSet: c.Bark.Password != "", WeComEnabled: c.WeCom.Enabled, WeComSet: c.WeCom.WebhookURL != "", BarkEnabled: c.Bark.Enabled, BarkSet: c.Bark.DeviceKey != "", BarkServer: c.Bark.ServerURL, BarkGroup: c.Bark.Group, BarkLevel: c.Bark.Level, WeComStatus: states["wecom"], BarkStatus: states["bark"]}
}
func MergeChannels(c channels.Config, in ChannelForm) (channels.Config, error) {
	switch in.Channel {
	case "wecom":
		c.WeCom.Enabled = in.Enabled
		if strings.TrimSpace(in.Address) != "" {
			c.WeCom.WebhookURL = in.Address
		}
		if in.Clear {
			c.WeCom = channels.WeComConfig{}
		}
	case "bark":
		c.Bark.Enabled = in.Enabled
		if raw := strings.TrimSpace(in.Address); raw != "" {
			u, err := url.Parse(raw)
			if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
				return c, fmt.Errorf("请粘贴 Bark App 中复制的 HTTPS 推送地址")
			}
			path := strings.TrimRight(u.Path, "/")
			i := strings.LastIndex(path, "/")
			if i < 0 || i == len(path)-1 {
				return c, fmt.Errorf("Bark 推送地址缺少设备 Key")
			}
			c.Bark.DeviceKey = path[i+1:]
			u.Path = path[:i]
			u.RawPath = ""
			c.Bark.ServerURL = u.String()
		}
		c.Bark.Username = strings.TrimSpace(in.Username)
		if in.Password != "" {
			c.Bark.Password = in.Password
		}
		if in.ClearAuth {
			c.Bark.Username = ""
			c.Bark.Password = ""
		}
		c.Bark.Group = in.Group
		c.Bark.Level = in.Level
		if in.Clear {
			c.Bark = channels.BarkConfig{}
		}
	default:
		return c, fmt.Errorf("未知推送渠道")
	}
	c.Normalize()
	return c, c.Validate()
}
func handleChannels(w http.ResponseWriter, r *http.Request, h Hooks) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "表单格式有误", 400)
		return
	}
	name := r.FormValue("channel")
	if name != "wecom" && name != "bark" {
		http.Error(w, "未知推送渠道", 400)
		return
	}
	var err error
	switch r.FormValue("op") {
	case "save":
		if h.SaveChannels == nil {
			http.Error(w, "此服务版本不支持渠道设置", 503)
			return
		}
		err = h.SaveChannels(r.Context(), ChannelForm{Username: r.FormValue("username"), Password: r.FormValue("password"), ClearAuth: r.FormValue("clear_auth") == "1", Channel: name, Enabled: r.FormValue("enabled") == "1", Clear: r.FormValue("clear") == "1", Address: r.FormValue("address"), Group: r.FormValue("group"), Level: r.FormValue("level")})
	case "test":
		if h.TestChannel == nil {
			http.Error(w, "此服务版本不支持测试通知", 503)
			return
		}
		err = h.TestChannel(r.Context(), name, "message")
	default:
		http.Error(w, "未知操作", 400)
		return
	}
	p := h.View()
	if err != nil {
		p.Error = err.Error()
	} else if r.FormValue("op") == "test" {
		p.Flash = "测试通知已发送，请在对应应用中确认。"
	} else {
		p.Flash = "渠道设置已保存并生效。"
	}
	renderChannels(w, p, r)
}
func renderChannels(w http.ResponseWriter, p Page, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := pages.ExecuteTemplate(w, "channels.html", p); err != nil {
		http.Error(w, "页面暂时无法显示", 500)
	}
}
func sameOrigin(r *http.Request) bool {
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return r.Header.Get("Sec-Fetch-Site") != "cross-site"
	}
	u, err := url.Parse(origin)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && strings.EqualFold(u.Host, r.Host)
}
