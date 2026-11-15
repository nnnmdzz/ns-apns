package tgclient

import (
	"context"
	"log/slog"

	"ns-apns/internal/apns"
	"ns-apns/internal/channels"
	"ns-apns/internal/config"
	"ns-apns/internal/forward"
	"ns-apns/internal/settings"
	"ns-apns/internal/web"
)

func newPusher(cfg config.Config, log *slog.Logger) (*channels.Dispatcher, error) {
	apnsSender, err := apns.New(cfg, log)
	if err != nil {
		return nil, err
	}
	var sender channels.Sender
	if apnsSender != nil {
		sender = apnsSender
	}
	p, err := channels.New(cfg.Channels, sender, log)
	if err == nil && p != nil {
		err = p.LoadState(cfg.SettingsPath + ".deliveries.json")
		if err != nil {
			p.Close()
		}
	}
	return p, err
}
func (h *Hub) saveChannels(ctx context.Context, form web.ChannelForm) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	cfg := h.cfg
	next, err := web.MergeChannels(cfg.Channels, form)
	if err != nil {
		return err
	}
	cfg.Channels = next
	pusher, err := newPusher(cfg, h.log)
	if err != nil {
		return err
	}
	if err := settings.Save(cfg.SettingsPath, cfg.Settings()); err != nil {
		pusher.Close()
		return err
	}
	h.cfg = cfg
	if h.client != nil {
		h.client.mu.Lock()
		old := h.client.pusher
		h.client.pusher = pusher
		h.client.cfg.Channels = next
		h.client.mu.Unlock()
		old.Close()
	} else {
		pusher.Close()
	}
	return nil
}
func (h *Hub) testChannel(ctx context.Context, name, kind string) error {
	h.mu.Lock()
	c := h.client
	cfg := h.cfg
	h.mu.Unlock()
	if c != nil {
		c.mu.Lock()
		p := c.pusher
		c.mu.Unlock()
		return p.SendChannel(ctx, name, forward.TestEvent(kind))
	}
	p, err := newPusher(cfg, h.log)
	if err != nil {
		return err
	}
	defer p.Close()
	err = p.SendChannel(ctx, name, forward.TestEvent(kind))
	h.mu.Lock()
	h.channelStates = p.Statuses()
	h.mu.Unlock()
	return err
}
