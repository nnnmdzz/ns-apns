package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"ns-apns/internal/channels"
)

// 僅供本機畫面驗收，所有設定與測試均留在記憶體，不會呼叫第三方。
func TestChannelPreviewServer(t *testing.T) {
	if os.Getenv("NS_APNS_PREVIEW") != "1" {
		t.Skip("preview only")
	}
	cfg := channels.Config{}
	cfg.Normalize()
	hooks := Hooks{View: func() Page { return Page{Channels: NewChannelPage(cfg, nil)} }}
	hooks.SaveChannels = func(_ context.Context, in ChannelForm) error {
		next, err := MergeChannels(cfg, in)
		if err == nil {
			cfg = next
		}
		return err
	}
	hooks.TestChannel = func(context.Context, string, string) error { return nil }
	done := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("GET /channels", func(w http.ResponseWriter, r *http.Request) { renderChannels(w, hooks.View(), r) })
	mux.HandleFunc("POST /channels", func(w http.ResponseWriter, r *http.Request) { handleChannels(w, r, hooks) })
	mux.HandleFunc("POST /__stop", func(w http.ResponseWriter, r *http.Request) { close(done) })
	server := httptest.NewServer(gate(mux, hooks))
	defer server.Close()
	if err := os.WriteFile("/private/tmp/ns-apns-preview-url", []byte(server.URL), 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(3 * time.Minute):
		t.Fatal("preview timed out")
	}
}
