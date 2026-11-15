package tgclient

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"ns-apns/internal/config"
	"ns-apns/internal/settings"
	"ns-apns/internal/web"
)

func TestChannelsSaveWithoutAPNsOrTelegramConnection(t *testing.T) {
	cfg := config.FromFile(settings.Default())
	cfg.SettingsPath = filepath.Join(t.TempDir(), "settings.json")
	h := NewHub(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	in := web.ChannelForm{Channel: "bark", Enabled: true, Address: "https://bark.example.com/prefix/fake-device-key", Group: "Forum", Level: "active"}
	if err := h.saveChannels(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	saved, err := settings.Load(cfg.SettingsPath)
	if err != nil || saved.Channels.Bark.DeviceKey != "fake-device-key" || len(saved.DeviceTokens) != 0 {
		t.Fatal(err)
	}
	p, err := newPusher(h.cfg, h.log)
	if err != nil || p == nil {
		t.Fatal("Bark-only requires APNs", err)
	}
	p.Close()
	if err := h.save(context.Background(), web.Form{SourceBot: "nodemaid_bot"}); err != nil {
		t.Fatal(err)
	}
	if h.cfg.Channels.Bark.DeviceKey != "fake-device-key" {
		t.Fatal("general form cleared channel")
	}
}
func TestLiveChannelUpdateDoesNotResolveTelegram(t *testing.T) {
	cfg := config.FromFile(settings.Default())
	cfg.SettingsPath = filepath.Join(t.TempDir(), "settings.json")
	h := NewHub(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	h.client = &Client{cfg: cfg, log: h.log}
	if err := h.saveChannels(context.Background(), web.ChannelForm{Channel: "wecom", Enabled: true, Address: "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=fake-key"}); err != nil {
		t.Fatal(err)
	}
	defer h.client.pusher.Close()
	page := h.view()
	if !page.Channels.WeComEnabled || page.APNsOn {
		t.Fatal("invalid enabled status")
	}
}
