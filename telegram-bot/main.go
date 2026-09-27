// Command bot is a Telegram bot that records bank receipt photos as Sure transactions.
//
// Flow: photo -> ocr-server /ocr -> Sure POST /transactions -> reply with details and
// category buttons -> tap -> Sure PUT /transactions/:id. It uses long polling, so it needs
// no public URL or webhook.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

type Config struct {
	TelegramToken    string
	TelegramAPIURL   string
	AllowedChats     map[int64]bool
	OCRURL           string
	SureAPIURL       string
	SureAPIKey       string
	MaxConcurrentOCR int
	HealthAddr       string
	PollTimeout      int // seconds
}

func env(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func LoadConfig() (Config, error) {
	c := Config{
		TelegramToken:  env("TELEGRAM_BOT_TOKEN", ""),
		TelegramAPIURL: env("TELEGRAM_API_URL", "https://api.telegram.org"),
		AllowedChats:   map[int64]bool{},
		OCRURL:         env("OCR_URL", "http://ocr-server:8001"),
		SureAPIURL:     env("SURE_API_URL", ""),
		SureAPIKey:     env("SURE_API_KEY", ""),
		HealthAddr:     env("HEALTH_ADDR", ":8080"),
		PollTimeout:    50,
	}
	var missing []string
	for k, v := range map[string]string{"TELEGRAM_BOT_TOKEN": c.TelegramToken, "SURE_API_URL": c.SureAPIURL, "SURE_API_KEY": c.SureAPIKey} {
		if v == "" {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		return c, fmt.Errorf("missing required env: %s", strings.Join(missing, ", "))
	}
	for _, s := range strings.Split(env("TELEGRAM_ALLOWED_CHAT_IDS", ""), ",") {
		if s = strings.TrimSpace(s); s == "" {
			continue
		}
		id, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return c, fmt.Errorf("TELEGRAM_ALLOWED_CHAT_IDS: %q is not a number", s)
		}
		c.AllowedChats[id] = true
	}
	n, err := strconv.Atoi(env("MAX_CONCURRENT_OCR", "1"))
	if err != nil || n < 1 {
		return c, errors.New("MAX_CONCURRENT_OCR must be a positive integer")
	}
	c.MaxConcurrentOCR = n
	return c, nil
}

func main() {
	healthcheck := flag.Bool("healthcheck", false, "query the local /health endpoint and exit 0/1 (for Docker HEALTHCHECK)")
	flag.Parse()

	level := slog.LevelInfo
	if strings.EqualFold(os.Getenv("LOG_LEVEL"), "debug") {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level}))

	if *healthcheck {
		os.Exit(runHealthcheck(env("HEALTH_ADDR", ":8080")))
	}

	cfg, err := LoadConfig()
	if err != nil {
		log.Error("config", "err", err)
		os.Exit(1)
	}
	if len(cfg.AllowedChats) == 0 {
		log.Warn("TELEGRAM_ALLOWED_CHAT_IDS is empty: anyone who finds the bot can add transactions")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := Run(ctx, cfg, log); err != nil {
		log.Error("bot stopped", "err", err)
		os.Exit(1)
	}
}

// Run polls Telegram until ctx is cancelled.
func Run(ctx context.Context, cfg Config, log *slog.Logger) error {
	pollClient := &http.Client{Timeout: time.Duration(cfg.PollTimeout+15) * time.Second}
	tg := NewTelegram(cfg.TelegramAPIURL, cfg.TelegramToken, pollClient)
	bot := &Bot{
		tg:      tg,
		ocr:     NewOCRClient(cfg.OCRURL, &http.Client{Timeout: 3 * time.Minute}),
		sure:    NewSureClient(cfg.SureAPIURL, cfg.SureAPIKey, &http.Client{Timeout: 30 * time.Second}),
		allowed: cfg.AllowedChats,
		ocrSem:  make(chan struct{}, cfg.MaxConcurrentOCR),
		log:     log,
		now:     time.Now,
	}

	var lastPoll atomic.Int64
	lastPoll.Store(time.Now().Unix())
	srv := startHealthServer(cfg.HealthAddr, time.Duration(cfg.PollTimeout)*time.Second*3, &lastPoll, log)
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	// Long polling (getUpdates) only works while no webhook is set for the bot token.
	// Remove any leftover webhook so updates come here.
	for backoff := time.Second; ; backoff = min(backoff*2, time.Minute) {
		err := tg.DeleteWebhook(ctx)
		if err == nil {
			break
		}
		if errors.Is(err, context.Canceled) || ctx.Err() != nil {
			return nil
		}
		log.Error("deleteWebhook (retrying)", "err", err, "retry_in", backoff)
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return nil
		}
	}
	log.Info("bot started (long polling; any Telegram webhook was removed)",
		"ocr", cfg.OCRURL, "sure", cfg.SureAPIURL, "allowed_chats", len(cfg.AllowedChats))

	var wg sync.WaitGroup
	var offset int64
	backoff := time.Second
	for ctx.Err() == nil {
		updates, err := tg.GetUpdates(ctx, offset, cfg.PollTimeout)
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			log.Error("getUpdates", "err", err, "retry_in", backoff)
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
			}
			backoff = min(backoff*2, time.Minute)
			continue
		}
		backoff = time.Second
		lastPoll.Store(time.Now().Unix())
		for _, u := range updates {
			offset = u.UpdateID + 1
			wg.Add(1)
			go func(u Update) {
				defer wg.Done()
				defer func() {
					if r := recover(); r != nil {
						log.Error("panic handling update", "update", u.UpdateID, "panic", r)
					}
				}()
				// Handlers get their own deadline so a shutdown lets in-flight receipts finish.
				hctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Minute)
				defer cancel()
				bot.HandleUpdate(hctx, u)
			}(u)
		}
	}
	log.Info("shutting down, waiting for in-flight receipts")
	wg.Wait()
	return nil
}

func startHealthServer(addr string, maxPollAge time.Duration, lastPoll *atomic.Int64, log *slog.Logger) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		age := time.Since(time.Unix(lastPoll.Load(), 0))
		status, code := "ok", http.StatusOK
		if age > maxPollAge {
			status, code = "telegram polling stalled", http.StatusServiceUnavailable
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(map[string]any{"status": status, "last_poll_seconds_ago": int(age.Seconds())})
	})
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("health server", "err", err)
		}
	}()
	return srv
}

func runHealthcheck(addr string) int {
	if strings.HasPrefix(addr, ":") {
		addr = "127.0.0.1" + addr
	}
	c := &http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get("http://" + addr + "/health")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}
