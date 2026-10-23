package bot

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"
	"strconv"
	"strings"
	"sync/atomic"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/pyed/transmission"
	"github.com/pyed/transmission-telegram/v2/internal/config"
)

// Bot manages the Telegram bot and Transmission client interaction.
type Bot struct {
	API    *tgbot.Bot
	Client *transmission.TransmissionClient
	Config *config.Config
	Logger *slog.Logger

	chatID     int64 // accessed atomically for thread safety
	mdReplacer *strings.Replacer
	commands   map[string]commandFunc

	// username is the bot's own Telegram username, without the @, from
	// GetMe; commands addressed to any other bot are ignored.
	username string
}

// commandFunc is the handler signature for bot commands.
type commandFunc func(ctx context.Context, ud *models.Update, args []string)

// New creates a new Bot instance and performs the Telegram startup calls.
// Extra options are passed to the Telegram library; tests use them to point
// the bot at a fake server.
func New(ctx context.Context, cfg *config.Config, client *transmission.TransmissionClient, logger *slog.Logger, extra ...tgbot.Option) (*Bot, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if logger == nil {
		logger = slog.Default()
	}

	b := &Bot{
		Client: client,
		Config: cfg,
		Logger: logger,
		mdReplacer: strings.NewReplacer(
			"*", "•",
			"[", "(",
			"]", ")",
			"_", "-",
			"`", "'",
		),
	}
	b.registerCommands()

	opts := []tgbot.Option{
		tgbot.WithDefaultHandler(b.handleUpdate),
		tgbot.WithMiddlewares(b.recoverMiddleware),
		tgbot.WithErrorsHandler(func(err error) {
			logger.Error("Telegram API error", "error", err)
		}),
		// GetMe is called below, once, so its answer can be kept.
		tgbot.WithSkipGetMe(),
	}

	api, err := tgbot.New(cfg.BotToken, append(opts, extra...)...)
	if err != nil {
		return nil, err
	}
	b.API = api

	me, err := api.GetMe(ctx)
	if err != nil {
		return nil, fmt.Errorf("get the bot's identity from Telegram: %w", err)
	}
	b.username = me.Username
	logger.Info("Telegram bot authorized", "username", me.Username)

	if err := clearWebhook(ctx, api); err != nil {
		return nil, err
	}

	// Register command list with Telegram menu button
	if _, err := api.SetMyCommands(ctx, &tgbot.SetMyCommandsParams{
		Commands: defaultBotCommands(),
	}); err != nil {
		logger.Warn("SetMyCommands failed", "error", err)
	}

	return b, nil
}

func clearWebhook(ctx context.Context, api *tgbot.Bot) error {
	info, err := api.GetWebhookInfo(ctx)
	if err != nil {
		return fmt.Errorf("get Telegram webhook info: %w", err)
	}
	if info.URL == "" {
		return nil
	}

	if _, err := api.DeleteWebhook(ctx, &tgbot.DeleteWebhookParams{DropPendingUpdates: false}); err != nil {
		return fmt.Errorf("delete stale Telegram webhook: %w", err)
	}
	return nil
}

// defaultBotCommands returns the list of primary commands to display in Telegram's menu.
func defaultBotCommands() []models.BotCommand {
	return []models.BotCommand{
		{Command: "list", Description: "List torrents (optional tracker filter)"},
		{Command: "head", Description: "List first n torrents with live speed"},
		{Command: "tail", Description: "List last n torrents with live speed"},
		{Command: "active", Description: "List active uploading/downloading torrents"},
		{Command: "downs", Description: "List downloading torrents"},
		{Command: "seeding", Description: "List seeding torrents"},
		{Command: "paused", Description: "List paused torrents"},
		{Command: "checking", Description: "List verifying torrents"},
		{Command: "errors", Description: "List torrents with errors"},
		{Command: "latest", Description: "List newest added torrents"},
		{Command: "search", Description: "Search torrents by name"},
		{Command: "info", Description: "Show torrent details and controls"},
		{Command: "add", Description: "Add torrent URLs or magnet links"},
		{Command: "stop", Description: "Pause torrents by ID or all"},
		{Command: "start", Description: "Resume torrents by ID or all"},
		{Command: "check", Description: "Verify torrents by ID or all"},
		{Command: "del", Description: "Remove torrents by ID"},
		{Command: "deldata", Description: "Remove torrents and their local data"},
		{Command: "speed", Description: "Show current upload & download speeds"},
		{Command: "stats", Description: "Show Transmission cumulative stats"},
		{Command: "free", Description: "Show download directory free disk space"},
		{Command: "turtle", Description: "Toggle Turtle Mode (alternative speed limits)"},
		{Command: "reannounce", Description: "Force tracker re-announce"},
		{Command: "downlimit", Description: "Set the global download speed limit"},
		{Command: "uplimit", Description: "Set the global upload speed limit"},
		{Command: "downloaddir", Description: "Set the default download directory"},
		{Command: "sort", Description: "Set torrent listing order"},
		{Command: "trackers", Description: "Show trackers and torrent counts"},
		{Command: "count", Description: "Show torrent count by status"},
		{Command: "help", Description: "Show help message"},
		{Command: "version", Description: "Show Transmission & bot versions"},
	}
}

// Start starts the bot polling loop (blocks until context is cancelled).
func (b *Bot) Start(ctx context.Context) {
	b.API.Start(ctx)
}

// normalizeCommand processes the raw first token of a message:
// - detects magnet/http links
// - strips leading "/"
// - splits off a trailing "@botname", as group commands carry, into mention
// - lowercases the command
func normalizeCommand(token string) (cmd, mention string, isLink bool) {
	lowerToken := strings.ToLower(token)
	if strings.HasPrefix(lowerToken, "magnet:") || strings.HasPrefix(lowerToken, "http://") || strings.HasPrefix(lowerToken, "https://") {
		return "add", "", true
	}
	cmd, mention, _ = strings.Cut(strings.TrimPrefix(lowerToken, "/"), "@")
	return cmd, mention, false
}

// addressedToUs reports whether a command's @mention, if any, names this bot.
// Telegram sends a bot that sees every group message the commands meant for
// other bots too, such as /stop@OtherBot.
func (b *Bot) addressedToUs(mention string) bool {
	return mention == "" || b.username == "" || strings.EqualFold(mention, b.username)
}

// handleUpdate processes incoming updates from Telegram.
func (b *Bot) handleUpdate(ctx context.Context, _ *tgbot.Bot, update *models.Update) {
	if update == nil {
		return
	}
	if update.CallbackQuery != nil {
		b.handleCallbackQuery(ctx, update.CallbackQuery)
		return
	}

	if update.Message == nil {
		return
	}

	username := ""
	var userID int64
	if update.Message.From != nil {
		username = update.Message.From.Username
		userID = update.Message.From.ID
	}

	if !b.Config.Masters.Contains(username, userID) {
		fromStr := username
		if fromStr == "" && update.Message.From != nil {
			fromStr = fmt.Sprintf("%s (ID: %d)", update.Message.From.FirstName, userID)
		}
		b.Logger.Warn("Ignored message from unauthorized sender",
			"sender", fromStr,
			"username", username,
			"user_id", userID,
			"chat_id", update.Message.Chat.ID,
		)
		return
	}

	fields := strings.Fields(update.Message.Text)
	var command, mention string
	var isLink bool
	if len(fields) > 0 {
		command, mention, isLink = normalizeCommand(fields[0])
		if !b.addressedToUs(mention) {
			return
		}
	}

	// Always store active chat ID for notifications
	atomic.StoreInt64(&b.chatID, update.Message.Chat.ID)

	b.Logger.Info("Received message",
		"sender", username,
		"user_id", userID,
		"chat_id", update.Message.Chat.ID,
		"message_id", update.Message.ID,
		"has_document", update.Message.Document != nil,
	)

	if len(fields) == 0 {
		if update.Message.Document != nil {
			b.receiveTorrent(ctx, update)
		}
		return
	}

	var args []string
	if isLink {
		args = fields
	} else {
		args = fields[1:]
	}

	b.Logger.Info("Dispatching command", "command", command, "arg_count", len(args), "sender", username)

	if handler, ok := b.commands[command]; ok {
		handler(ctx, update, args)
	} else if command == "" {
		b.receiveTorrent(ctx, update)
	} else {
		b.Logger.Warn("Unknown command", "sender", username, "user_id", userID)
		b.Send(ctx, "No such command, try /help", update.Message.Chat.ID, false)
	}
}

func (b *Bot) recoverMiddleware(next tgbot.HandlerFunc) tgbot.HandlerFunc {
	return func(ctx context.Context, api *tgbot.Bot, update *models.Update) {
		var updateID int64
		if update != nil {
			updateID = update.ID
		}
		defer func() {
			if recovered := recover(); recovered != nil {
				b.Logger.Error("Recovered from Telegram update panic",
					"panic", recovered,
					"update_id", updateID,
					"stack", string(debug.Stack()),
				)
			}
		}()
		next(ctx, api, update)
	}
}

func (b *Bot) goSafe(operation string, fn func()) {
	go func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				b.Logger.Error("Recovered from background task panic",
					"operation", operation,
					"panic", recovered,
					"stack", string(debug.Stack()),
				)
			}
		}()
		fn()
	}()
}

// handleCallbackQuery processes button clicks from inline keyboards.
func (b *Bot) handleCallbackQuery(ctx context.Context, cb *models.CallbackQuery) {
	username := cb.From.Username
	userID := cb.From.ID

	if !b.Config.Masters.Contains(username, userID) {
		b.API.AnswerCallbackQuery(ctx, &tgbot.AnswerCallbackQueryParams{
			CallbackQueryID: cb.ID,
			Text:            "Unauthorized",
			ShowAlert:       true,
		})
		return
	}

	// Expected callback format: cmd:<action>:<id>:<torrent fingerprint>.
	// Pause/resume callbacks from v2.0.0 remain supported, but legacy delete
	// buttons are deliberately expired because they were destructive and were
	// not tied to a stable torrent identity.
	parts := strings.Split(cb.Data, ":")
	if (len(parts) != 3 && len(parts) != 4) || parts[0] != "cmd" {
		b.API.AnswerCallbackQuery(ctx, &tgbot.AnswerCallbackQueryParams{
			CallbackQueryID: cb.ID,
			Text:            "Invalid action",
		})
		return
	}

	action := parts[1]
	torrentID, err := strconv.Atoi(parts[2])
	if err != nil {
		b.API.AnswerCallbackQuery(ctx, &tgbot.AnswerCallbackQueryParams{
			CallbackQueryID: cb.ID,
			Text:            "Invalid torrent ID",
		})
		return
	}

	if len(parts) == 3 && action == "del" {
		b.answerCallback(ctx, cb.ID, "Delete button expired; run /info again", true)
		return
	}

	torrent, err := b.Client.GetTorrent(torrentID, ctx)
	if err != nil {
		b.answerCallback(ctx, cb.ID, "Torrent no longer exists", true)
		return
	}
	if len(parts) == 4 && parts[3] != torrentFingerprint(torrent.HashString) {
		b.answerCallback(ctx, cb.ID, "Torrent changed; run /info again", true)
		return
	}

	var answerText string
	switch action {
	case "stop":
		if err := b.Client.StopTorrents(ctx, torrentID); err != nil {
			answerText = "Error stopping: " + err.Error()
		} else {
			answerText = "Torrent paused"
		}
	case "start":
		if err := b.Client.StartTorrents(ctx, torrentID); err != nil {
			answerText = "Error resuming: " + err.Error()
		} else {
			answerText = "Torrent resumed"
		}
	case "del":
		if err := b.editCallbackKeyboard(ctx, cb, deleteConfirmationKeyboard(torrent.ID, torrent.HashString)); err != nil {
			b.Logger.Warn("Failed to show delete confirmation", "error", err, "torrent_id", torrentID)
			b.answerCallback(ctx, cb.ID, fmt.Sprintf("Use /del %d to delete", torrentID), true)
			return
		}
		b.answerCallback(ctx, cb.ID, "Confirm deletion below", false)
		return
	case "confirm-del":
		if err := b.Client.RemoveTorrents(ctx, false, torrentID); err != nil {
			answerText = "Error deleting: " + err.Error()
			break
		}
		answerText = "Deleted: " + torrent.Name
		if err := b.editCallbackKeyboard(ctx, cb, &models.InlineKeyboardMarkup{}); err != nil {
			b.Logger.Warn("Failed to remove deleted torrent keyboard", "error", err, "torrent_id", torrentID)
		}
	case "cancel":
		if err := b.editCallbackKeyboard(ctx, cb, infoKeyboard(torrent.ID, torrent.HashString)); err != nil {
			b.Logger.Warn("Failed to restore torrent keyboard", "error", err, "torrent_id", torrentID)
			answerText = "Could not cancel; run /info again"
		} else {
			answerText = "Deletion cancelled"
		}
	default:
		answerText = "Unknown action"
	}

	b.answerCallback(ctx, cb.ID, answerText, false)
}

func torrentFingerprint(hash string) string {
	const fingerprintLength = 16
	hash = strings.ToLower(strings.TrimSpace(hash))
	if len(hash) > fingerprintLength {
		return hash[:fingerprintLength]
	}
	return hash
}

func (b *Bot) answerCallback(ctx context.Context, callbackID, text string, alert bool) {
	if _, err := b.API.AnswerCallbackQuery(ctx, &tgbot.AnswerCallbackQueryParams{
		CallbackQueryID: callbackID,
		Text:            text,
		ShowAlert:       alert,
	}); err != nil {
		b.Logger.Warn("AnswerCallbackQuery failed", "error", err)
	}
}

func (b *Bot) editCallbackKeyboard(ctx context.Context, cb *models.CallbackQuery, markup models.ReplyMarkup) error {
	params := &tgbot.EditMessageReplyMarkupParams{
		InlineMessageID: cb.InlineMessageID,
		ReplyMarkup:     markup,
	}

	switch {
	case cb.Message.Message != nil:
		params.ChatID = cb.Message.Message.Chat.ID
		params.MessageID = cb.Message.Message.ID
	case cb.Message.InaccessibleMessage != nil:
		params.ChatID = cb.Message.InaccessibleMessage.Chat.ID
		params.MessageID = cb.Message.InaccessibleMessage.MessageID
	case cb.InlineMessageID == "":
		return fmt.Errorf("callback has no message target")
	}

	_, err := b.API.EditMessageReplyMarkup(ctx, params)
	return err
}

// ChatID returns the current chat ID atomically (used by the notify package).
func (b *Bot) ChatID() int64 {
	return atomic.LoadInt64(&b.chatID)
}
