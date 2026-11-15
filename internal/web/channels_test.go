package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"ns-apns/internal/channels"
)

func TestChannelMergePreservesOtherSecrets(t *testing.T) {
	c := channels.Config{WeCom: channels.WeComConfig{Enabled: true, WebhookURL: "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=secret"}}
	next, err := MergeChannels(c, ChannelForm{Channel: "bark", Enabled: true, Address: "https://bark.example.com/prefix/device-key", Group: "Forum", Level: "active"})
	if err != nil || next.WeCom != c.WeCom || next.Bark.ServerURL != "https://bark.example.com/prefix" || next.Bark.DeviceKey != "device-key" {
		t.Fatal(next, err)
	}
	kept, err := MergeChannels(next, ChannelForm{Channel: "bark", Enabled: true, Level: "active"})
	if err != nil || kept.Bark.DeviceKey != "device-key" {
		t.Fatal(err)
	}
	cleared, err := MergeChannels(kept, ChannelForm{Channel: "bark", Clear: true})
	if err != nil || cleared.Bark.Enabled || cleared.Bark.DeviceKey != "" || cleared.WeCom != c.WeCom {
		t.Fatal(cleared, err)
	}
}
func TestChannelPageDoesNotRevealSecrets(t *testing.T) {
	cfg := channels.Config{WeCom: channels.WeComConfig{WebhookURL: "private-wecom-secret"}, Bark: channels.BarkConfig{DeviceKey: "private-bark-secret"}}
	w := httptest.NewRecorder()
	renderChannels(w, Page{Channels: NewChannelPage(cfg, nil)}, httptest.NewRequest("GET", "/channels", nil))
	body := w.Body.String()
	if w.Code != 200 || strings.Contains(body, "private-wecom-secret") || strings.Contains(body, "private-bark-secret") || !strings.Contains(body, "Bark 推送地址") {
		t.Fatal(w.Code, body)
	}
}
func TestChannelTestTargetsOnlySelectedProvider(t *testing.T) {
	selected := ""
	h := Hooks{View: func() Page { return Page{} }, TestChannel: func(_ context.Context, name, kind string) error { selected = name; return nil }}
	form := url.Values{"op": {"test"}, "channel": {"bark"}}
	r := httptest.NewRequest("POST", "https://notify.example.com/channels", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	gate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handleChannels(w, r, h) }), h).ServeHTTP(w, r)
	if w.Code != 200 || selected != "bark" {
		t.Fatal(w.Code, selected)
	}
}
func TestChannelsRequireAuthenticationAndSameOrigin(t *testing.T) {
	called := false
	h := Hooks{Password: func() string { return "password" }}
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true })
	for _, origin := range []string{"https://evil.example", ""} {
		r := httptest.NewRequest("POST", "https://notify.example/channels", strings.NewReader("op=test&channel=bark"))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		gate(next, h).ServeHTTP(w, r)
		if called {
			t.Fatal("unauthorized request allowed")
		}
		if origin != "" && w.Code != 403 {
			t.Fatal(w.Code)
		}
	}
}
