package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"os/signal"
	"syscall"

	"github.com/pyed/transmission"
	"github.com/pyed/transmission-telegram/v2/internal/bot"
	"github.com/pyed/transmission-telegram/v2/internal/config"
	"github.com/pyed/transmission-telegram/v2/internal/notify"
)

func main() {
	cfg := config.Parse()
	if cfg.ShowVersion {
		fmt.Println("transmission-telegram", config.Version)
		return
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// Setup structured logger
	var logOutput *os.File = os.Stdout
	if cfg.LogFile != "" {
		logf, err := os.OpenFile(cfg.LogFile, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[ERROR] Open logfile: %s\n", err)
			os.Exit(1)
		}
		defer logf.Close()
		logOutput = logf
	}

	logger := slog.New(slog.NewTextHandler(logOutput, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	logger.Info("Starting transmission-telegram",
		"version", config.Version,
		"master_count", len(cfg.Masters),
		"rpc_endpoint", safeEndpoint(cfg.RPCURL),
	)

	// Initialize Transmission client
	client, err := transmission.New(cfg.RPCURL, cfg.Username, cfg.Password)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] Transmission: Make sure you have the right URL, Username and Password\n")
		os.Exit(1)
	}

	// Create bot instance
	b, err := bot.New(ctx, cfg, client, logger)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] Telegram: %s\n", err)
		os.Exit(1)
	}

	// Start completion notifications
	startWatcher := func() {
		notify.StartWatcher(ctx, cfg.Interval, func(reqCtx context.Context) (transmission.Torrents, error) {
			return client.GetTorrents(reqCtx)
		}, b.ChatID, func(text string, chatID int64, markdown bool) int {
			return b.Send(ctx, text, chatID, markdown)
		}, logger)
	}
	if cfg.TransLogFile != "" {
		if err := notify.StartTailer(ctx, cfg.TransLogFile, b.ChatID, func(text string, chatID int64, markdown bool) int {
			return b.Send(ctx, text, chatID, markdown)
		}, logger); err != nil {
			logger.Warn("Transmission logfile unavailable; using RPC completion watcher", "error", err)
			startWatcher()
		}
	} else {
		startWatcher()
	}

	// Run the bot (blocks until context is cancelled)
	b.Start(ctx)
}

// safeEndpoint keeps operationally useful URL details out of logs without
// exposing embedded credentials, query tokens, or fragments.
func safeEndpoint(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "[invalid URL]"
	}
	u.User = nil
	u.RawQuery = ""
	u.ForceQuery = false
	u.Fragment = ""
	return u.String()
}
