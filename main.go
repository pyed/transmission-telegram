package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	tgbot "github.com/go-telegram/bot"
	"github.com/pyed/transmission"
	"github.com/pyed/transmission-telegram/v2/internal/bot"
	"github.com/pyed/transmission-telegram/v2/internal/config"
	"github.com/pyed/transmission-telegram/v2/internal/logfile"
	"github.com/pyed/transmission-telegram/v2/internal/notify"
	"github.com/pyed/transmission-telegram/v2/internal/redact"
	"github.com/pyed/transmission-telegram/v2/internal/service"
	"github.com/pyed/transmission-telegram/v2/internal/state"
)

func main() {
	if service.Run(runService) {
		return
	}
	cfg := config.Parse()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, cfg, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "[ERROR]", err)
		os.Exit(1)
	}
}

// runService runs the bot as the Windows service, which the service
// manager starts with args.
func runService(ctx context.Context, args []string, output io.Writer) error {
	fs := flag.NewFlagSet("transmission-telegram", flag.ContinueOnError)
	fs.SetOutput(output)
	cfg, err := config.ParseFlags(fs, args, os.Getenv)
	if err != nil {
		return err
	}
	return run(ctx, cfg, output)
}

// run does what cfg asks: it prints the version, installs or removes the
// service, or runs the bot until ctx ends, logging to output unless cfg
// names a log file.
func run(ctx context.Context, cfg *config.Config, output io.Writer) error {
	switch {
	case cfg.ShowVersion:
		_, err := fmt.Fprintln(output, "transmission-telegram", config.Version)
		return err
	case cfg.Uninstall:
		return service.Uninstall(output)
	case cfg.Install:
		return service.Install(ctx, cfg, func(ctx context.Context) error { return checkConnections(ctx, cfg, output) }, output)
	}

	if cfg.LogFile != "" {
		logf, err := logfile.Open(cfg.LogFile, logfile.MaxSize)
		if err != nil {
			return fmt.Errorf("open the log file: %w", err)
		}
		defer logf.Close()
		output = logf
	}
	logger := slog.New(slog.NewTextHandler(output, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	logger.Info("Starting transmission-telegram",
		"version", config.Version,
		"master_count", len(cfg.Masters),
		"rpc_endpoint", redact.URL(cfg.RPCURL),
	)
	if usernames := cfg.Masters.Usernames(); len(usernames) > 0 {
		logger.Warn("Some masters are given by username. A username its owner gives up can be claimed by someone else, "+
			"who would then control this bot; use numeric user IDs instead (/whoami shows yours)",
			"usernames", usernames)
	}
	for _, warning := range cfg.Warnings {
		logger.Warn(warning)
	}
	if cfg.Masters.Setup() {
		logger.Warn("The only master is the placeholder 1, so the bot tells anyone who writes to it their user ID; " +
			"restart it with yours as -master or TT_MASTERS")
	}

	// Initialize Transmission client. The client works without a first
	// answer, so a Transmission that starts after the bot, as at boot or in
	// Compose, is no reason to stop; the watcher reports while it is down.
	client, err := transmission.New(cfg.RPCURL, cfg.Username, cfg.Password)
	if client == nil {
		return errors.New("the Transmission RPC URL (-url or TR_URL) is invalid")
	}
	if err != nil {
		logger.Warn("Transmission is not answering; starting anyway, and commands will work once it does",
			"error", redact.Error(err))
	}

	store, err := loadState(cfg.StateFile, logger)
	if err != nil {
		return err
	}

	// Create bot instance
	b, err := bot.New(ctx, cfg, client, store, logger)
	if err != nil {
		return fmt.Errorf("telegram: %w", err)
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
	return nil
}

// checkConnections reaches Telegram and Transmission with cfg's settings,
// so that -install does not set up a service that cannot work. Tests give
// extra options for the Telegram client.
func checkConnections(ctx context.Context, cfg *config.Config, output io.Writer, extra ...tgbot.Option) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	api, err := tgbot.New(cfg.BotToken, append([]tgbot.Option{tgbot.WithSkipGetMe()}, extra...)...)
	if err != nil {
		return fmt.Errorf("telegram: %w", err)
	}
	me, err := api.GetMe(ctx)
	if err != nil {
		return fmt.Errorf("telegram, with the bot token: %s", redact.Error(err))
	}
	client, err := transmission.New(cfg.RPCURL, cfg.Username, cfg.Password)
	if err != nil {
		return fmt.Errorf("transmission at %s: %s; start it, or correct the settings", redact.URL(cfg.RPCURL), redact.Error(err))
	}
	session, err := client.GetSession(ctx)
	if err != nil {
		return fmt.Errorf("transmission at %s: %s; start it, or correct the settings", redact.URL(cfg.RPCURL), redact.Error(err))
	}
	fmt.Fprintf(output, "Checked: Telegram bot @%s, Transmission %s at %s\n", me.Username, session.Version, redact.URL(cfg.RPCURL))
	return nil
}

// loadState opens the state file: the one given, or the default one. Without
// a place for it, or when it cannot be written, the bot still runs, keeping
// its state in memory, and says so; a damaged file stops it, since starting
// afresh would lose what the bot remembered.
func loadState(path string, logger *slog.Logger) (*state.Store, error) {
	if path == "" {
		var err error
		if path, err = state.DefaultPath(); err != nil {
			logger.Warn("No directory for the state file, so what the bot remembers, such as where notifications go, "+
				"lasts only until it stops; set -state or TT_STATE", "error", err)
		}
	}
	store, err := state.Load(path)
	if err != nil {
		return nil, err
	}
	if err := store.Update(func(*state.Data) {}); err != nil {
		logger.Warn("Cannot save the state file, so what the bot remembers lasts only until it stops; "+
			"make its directory writable or set -state or TT_STATE", "path", store.Path(), "error", err)
	} else if store.Path() != "" {
		logger.Info("Keeping state", "path", store.Path())
	}
	return store, nil
}
