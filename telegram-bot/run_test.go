package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestRunPollsAndDispatches checks the polling loop: webhook removed, offset advanced,
// update handled, health endpoint served, clean shutdown.
func TestRunPollsAndDispatches(t *testing.T) {
	var polls, deleted atomic.Int32
	var lastOffset atomic.Int64
	answered := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		var result any = true
		switch method {
		case "deleteWebhook":
			deleted.Add(1)
		case "getUpdates":
			n := polls.Add(1)
			lastOffset.Store(int64(body["offset"].(float64)))
			if n == 1 {
				result = []any{map[string]any{"update_id": 10, "callback_query": map[string]any{
					"id": "q", "data": "garbage", "message": map[string]any{"message_id": 1, "chat": map[string]any{"id": 5}}}}}
			} else {
				time.Sleep(20 * time.Millisecond)
				result = []any{}
			}
		case "answerCallbackQuery":
			select {
			case answered <- struct{}{}:
			default:
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": result})
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cfg := Config{TelegramToken: "t", TelegramAPIURL: srv.URL, AllowedChats: map[int64]bool{}, OCRURL: srv.URL,
		SureAPIURL: srv.URL, SureAPIKey: "k", MaxConcurrentOCR: 1, HealthAddr: "127.0.0.1:18089", PollTimeout: 1}
	done := make(chan error)
	go func() { done <- Run(ctx, cfg, slog.New(slog.NewTextHandler(io.Discard, nil))) }()

	select {
	case <-answered:
	case <-time.After(3 * time.Second):
		t.Fatal("update was not handled")
	}
	time.Sleep(100 * time.Millisecond)
	if code := runHealthcheck("127.0.0.1:18089"); code != 0 {
		t.Errorf("healthcheck exit = %d", code)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not stop")
	}
	if deleted.Load() != 1 {
		t.Errorf("deleteWebhook calls = %d", deleted.Load())
	}
	if lastOffset.Load() != 11 {
		t.Errorf("offset after update 10 = %d, want 11", lastOffset.Load())
	}
}

func TestLoadConfig(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "x")
	t.Setenv("SURE_API_URL", "http://sure")
	t.Setenv("SURE_API_KEY", "k")
	t.Setenv("TELEGRAM_ALLOWED_CHAT_IDS", "123456789, -100123")
	c, err := LoadConfig()
	if err != nil || !c.AllowedChats[123456789] || !c.AllowedChats[-100123] || c.OCRURL != "http://ocr-server:8001" {
		t.Fatalf("config = %+v, %v", c, err)
	}
	t.Setenv("SURE_API_KEY", "")
	if _, err := LoadConfig(); err == nil || !strings.Contains(err.Error(), "SURE_API_KEY") {
		t.Errorf("expected missing SURE_API_KEY error, got %v", err)
	}
}
