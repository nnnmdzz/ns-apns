package channels

import (
	"fmt"
	"net/url"
	"strings"
	"unicode"
)

type Config struct {
	WeCom WeComConfig `json:"wecom"`
	Bark  BarkConfig  `json:"bark"`
}
type WeComConfig struct {
	Enabled    bool   `json:"enabled"`
	WebhookURL string `json:"webhook_url"`
}
type BarkConfig struct {
	Username  string `json:"username,omitempty"`
	Password  string `json:"password,omitempty"`
	Enabled   bool   `json:"enabled"`
	ServerURL string `json:"server_url"`
	DeviceKey string `json:"device_key"`
	Group     string `json:"group"`
	Level     string `json:"level"`
}

func (c *Config) Normalize() {
	c.WeCom.WebhookURL = strings.TrimSpace(c.WeCom.WebhookURL)
	c.Bark.ServerURL = strings.TrimRight(strings.TrimSpace(c.Bark.ServerURL), "/")
	if c.Bark.ServerURL == "" {
		c.Bark.ServerURL = "https://api.day.app"
	}
	c.Bark.DeviceKey = strings.TrimSpace(c.Bark.DeviceKey)
	c.Bark.Group = strings.TrimSpace(c.Bark.Group)
	if c.Bark.Group == "" {
		c.Bark.Group = "NodeSeek"
	}
	if c.Bark.Level == "" {
		c.Bark.Level = "active"
	}
}
func (c Config) Validate() error {
	if c.WeCom.Enabled {
		u, err := url.Parse(c.WeCom.WebhookURL)
		if err != nil || u.Scheme != "https" || u.Host != "qyapi.weixin.qq.com" || u.Path != "/cgi-bin/webhook/send" || u.User != nil || u.Fragment != "" {
			return fmt.Errorf("请输入企业微信机器人的完整 HTTPS Webhook 地址")
		}
		q, err := url.ParseQuery(u.RawQuery)
		if err != nil || len(q) != 1 || len(q["key"]) != 1 || !validKey(q.Get("key")) {
			return fmt.Errorf("企业微信 Webhook 缺少有效的 key")
		}
	}
	if c.Bark.Enabled {
		u, err := url.Parse(c.Bark.ServerURL)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
			return fmt.Errorf("Bark 服务地址应为 HTTPS，不应包含设备 Key、查询参数或密码")
		}
		if !validKey(c.Bark.DeviceKey) {
			return fmt.Errorf("请输入 Bark 设备 Key，不要填写完整推送链接")
		}
		if (c.Bark.Username == "") != (c.Bark.Password == "") {
			return fmt.Errorf("Bark Basic Auth 需要同时填写用户名和密码")
		}
		if len(c.Bark.Username) > 256 || len(c.Bark.Password) > 1024 {
			return fmt.Errorf("Bark 鉴权信息过长")
		}
		if len(c.Bark.Group) > 128 {
			return fmt.Errorf("Bark 分组名称过长")
		}
		switch c.Bark.Level {
		case "active", "passive", "timeSensitive":
		default:
			return fmt.Errorf("请选择有效的 Bark 通知级别")
		}
	}
	return nil
}
func validKey(s string) bool {
	return s != "" && len(s) <= 256 && !strings.ContainsAny(s, "/?#&;=") && strings.IndexFunc(s, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) < 0
}
