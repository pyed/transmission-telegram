package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/pyed/transmission"
	"github.com/pyed/transmission-telegram/v2/internal/bot"
	"github.com/pyed/transmission-telegram/v2/internal/config"
	"github.com/pyed/transmission-telegram/v2/internal/logfile"
	"github.com/pyed/transmission-telegram/v2/internal/notify"
	"github.com/pyed/transmission-telegram/v2/internal/redact"
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
	var logOutput io.Writer = os.Stdout
	if cfg.LogFile != "" {
		logf, err := logfile.Open(cfg.LogFile, logfile.MaxSize)
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
		"rpc_endpoint", redact.URL(cfg.RPCURL),
	)
	if usernames := cfg.Masters.Usernames(); len(usernames) > 0 {
		logger.Warn("Some masters are given by username. A username its owner gives up can be claimed by someone else, "+
			"who would then control this bot; use numeric user IDs instead (the log shows each sender's user_id)",
			"usernames", usernames)
	}

	// Initialize Transmission client. The client works without a first
	// answer, so a Transmission that starts after the bot, as at boot or in
	// Compose, is no reason to stop; the watcher reports while it is down.
	client, err := transmission.New(cfg.RPCURL, cfg.Username, cfg.Password)
	if client == nil {
		fmt.Fprintln(os.Stderr, "[ERROR] The Transmission RPC URL (-url or TR_URL) is invalid")
		os.Exit(1)
	}
	if err != nil {
		logger.Warn("Transmission is not answering; starting anyway, and commands will work once it does",
			"error", redact.Error(err))
	}

	// Create bot instance
	b, err := bot.New(ctx, cfg, client, logger)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] Telegram: %s\n", err)
		os.Exit(1)
	}

	// Start completion notifications
	if cfg.TransLogFile != "" {
		logger.Warn("-transmission-logfile is no longer used: Transmission 4 does not log completions at its " +
			"default level, so notifications come from Transmission's RPC on every version; remove the flag")
	}
	notify.StartWatcher(ctx, cfg.Interval, func(reqCtx context.Context) (transmission.Torrents, error) {
		return client.GetTorrents(reqCtx)
	}, b.ChatID, func(text string, chatID int64, markdown bool) int {
		return b.Notify(ctx, chatID, text)
	}, logger)

	// Run the bot (blocks until context is cancelled)
	b.Start(ctx)
}
