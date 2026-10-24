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
	"github.com/pyed/transmission-telegram/v2/internal/state"
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

	store := loadState(cfg.StateFile, logger)

	// Create bot instance
	b, err := bot.New(ctx, cfg, client, store, logger)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ERROR] Telegram: %s\n", err)
		os.Exit(1)
	}

	// Start completion notifications
	if cfg.TransLogFile != "" {
		logger.Warn("-transmission-logfile is no longer used: Transmission 4 does not log completions at its " +
			"default level, so notifications come from Transmission's RPC on every version; remove the flag")
	}
	watcher := &notify.Watcher{
		Transmission: client,
		State:        store,
		Send:         b.SendHTML,
		Open:         bot.OpenButton,
		Logger:       logger,
		Interval:     cfg.WatchInterval,
		LowDisk:      cfg.LowDisk,
	}
	go watcher.Run(ctx)

	// Run the bot (blocks until context is cancelled)
	b.Start(ctx)
}

// loadState opens the state file: the one given, or the default one. Without
// a place for it, or when it cannot be written, the bot still runs, keeping
// its state in memory, and says so; a damaged file stops it, since starting
// afresh would lose what the bot remembered.
func loadState(path string, logger *slog.Logger) *state.Store {
	if path == "" {
		var err error
		if path, err = state.DefaultPath(); err != nil {
			logger.Warn("No directory for the state file, so what the bot remembers, such as where notifications go, "+
				"lasts only until it stops; set -state or TT_STATE", "error", err)
		}
	}
	store, err := state.Load(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "[ERROR]", err)
		os.Exit(1)
	}
	if err := store.Update(func(*state.Data) {}); err != nil {
		logger.Warn("Cannot save the state file, so what the bot remembers lasts only until it stops; "+
			"make its directory writable or set -state or TT_STATE", "path", store.Path(), "error", err)
	} else if store.Path() != "" {
		logger.Info("Keeping state", "path", store.Path())
	}
	return store
}
