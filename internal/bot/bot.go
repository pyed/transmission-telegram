package bot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/pyed/transmission"
	"github.com/pyed/transmission-telegram/v2/internal/config"
	"github.com/pyed/transmission-telegram/v2/internal/redact"
	"github.com/pyed/transmission-telegram/v2/internal/state"
)

// Bot manages the Telegram bot and Transmission client interaction.
type Bot struct {
	API    *tgbot.Bot
	Client *transmission.TransmissionClient
	Config *config.Config
	Logger *slog.Logger
	// State is what the bot remembers across restarts.
	State *state.Store

	commands map[string]commandFunc

	// username is the bot's own Telegram username, without the @, from
	// GetMe; commands addressed to any other bot are ignored.
	username string

	// retryDelay is the first wait before a failed Telegram request is
	// tried again; a second when zero. Tests shorten it.
	retryDelay time.Duration
	// typingDelay is how long a message's handling takes before the chat
	// shows the bot typing; a second when zero.
	typingDelay time.Duration

	// queues holds each master chat's updates waiting to be handled.
	queuesMu sync.Mutex
	queues   map[int64]chan *models.Update
	// wg counts the background work Start waits for at shutdown: chat
	// queues, live views, typing indicators.
	wg sync.WaitGroup

	// liveViews holds each chat's live view, if it has one.
	liveMu    sync.Mutex
	liveViews map[int64]*liveView

	// screens remembers the list messages whose buttons turn pages.
	screensMu sync.Mutex
	screens   screenStore

	// toldIDs are the users told their ID in setup mode.
	setupMu sync.Mutex
	toldIDs map[int64]bool
}

// chatQueueSize is how many updates of one chat may wait for its handler.
const chatQueueSize = 64

// request is a command to carry out: the chat it came from, its arguments,
// and the message that carried it.
type request struct {
	chatID  int64
	args    []string
	message *models.Message
}

// commandFunc is the handler signature for bot commands.
type commandFunc func(ctx context.Context, r *request)

// reply sends an HTML reply to the request's chat. It returns the
// message's ID, or 0 when the reply was not one message; failures are
// logged.
func (b *Bot) reply(ctx context.Context, r *request, text string) int {
	messageID, _ := b.send(ctx, r.chatID, text, nil)
	return messageID
}

// replyError tells the request's chat that the command failed, and why,
// without any credentials a request URL in err carries.
func (b *Bot) replyError(ctx context.Context, r *request, command string, err error) {
	b.reply(ctx, r, "<b>"+esc(command)+":</b> "+esc(redact.Error(err)))
}

// SendHTML sends an HTML message to a chat, with markup unless it is nil,
// retrying passing failures; the notification watcher uses it.
func (b *Bot) SendHTML(ctx context.Context, chatID int64, text string, markup models.ReplyMarkup) error {
	_, err := b.send(ctx, chatID, text, markup)
	return err
}

// OpenButton is a button that sends a torrent's card, for notifications.
// It carries all it needs, so it works however old the notification is.
func OpenButton(id int, hash string) models.ReplyMarkup {
	return &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{{
		{Text: "🔎 Open", CallbackData: fmt.Sprintf("t:%d:%s", id, torrentFingerprint(hash))},
	}}}
}

// New creates a new Bot instance and performs the Telegram startup calls.
// Extra options are passed to the Telegram library; tests use them to point
// the bot at a fake server.
func New(ctx context.Context, cfg *config.Config, client *transmission.TransmissionClient, store *state.Store, logger *slog.Logger, extra ...tgbot.Option) (*Bot, error) {
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
		State:  store,
	}
	b.registerCommands()

	opts := []tgbot.Option{
		// The library hands updates to dispatch one at a time, in order;
		// dispatch queues them per chat and returns at once.
		tgbot.WithNotAsyncHandlers(),
		tgbot.WithDefaultHandler(b.dispatch),
		tgbot.WithMiddlewares(b.recoverMiddleware),
		tgbot.WithAllowedUpdates(tgbot.AllowedUpdates{models.AllowedUpdateMessage, models.AllowedUpdateCallbackQuery}),
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

	me, err := retryStartup(ctx, logger, "getMe", api.GetMe)
	if permanentStartupError(err) {
		return nil, fmt.Errorf("telegram refused the bot token; check -token or TT_BOT_TOKEN with @BotFather: %w", err)
	}
	if err != nil {
		return nil, fmt.Errorf("get the bot's identity from Telegram: %w", err)
	}
	b.username = me.Username
	logger.Info("Telegram bot authorized", "username", me.Username)

	if _, err := retryStartup(ctx, logger, "clear webhook", func(ctx context.Context) (struct{}, error) {
		return struct{}{}, clearWebhook(ctx, api)
	}); err != nil {
		return nil, err
	}

	b.registerMenu(ctx)
	return b, nil
}

// startupDelay is how long New waits after its n-th failed call to Telegram,
// counting from 0: a second, doubling up to a minute. Tests shorten it.
var startupDelay = func(attempt int) time.Duration {
	if attempt >= 6 {
		return time.Minute
	}
	return time.Second << attempt
}

// retryStartup calls fn until it succeeds, Telegram refuses the token, or ctx
// ends, so a bot that starts before the network is up, as at boot, waits for
// Telegram instead of exiting.
func retryStartup[T any](ctx context.Context, logger *slog.Logger, step string, fn func(context.Context) (T, error)) (T, error) {
	for attempt := 0; ; attempt++ {
		result, err := fn(ctx)
		if err == nil || permanentStartupError(err) || ctx.Err() != nil {
			return result, err
		}
		delay := startupDelay(attempt)
		logger.Warn("Telegram did not answer; retrying", "step", step, "error", err, "retry_in", delay)
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		case <-time.After(delay):
		}
	}
}

// permanentStartupError reports the errors that retrying cannot fix:
// Telegram answers 401 for a wrong or revoked token and 404 for a malformed
// one.
func permanentStartupError(err error) bool {
	return errors.Is(err, tgbot.ErrorUnauthorized) || errors.Is(err, tgbot.ErrorNotFound)
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

// Start starts the bot polling loop (blocks until context is cancelled).
func (b *Bot) Start(ctx context.Context) {
	b.API.Start(ctx)
	b.wg.Wait()
}

// dispatch passes an update to its chat's queue, so that chats are handled
// side by side and each chat's updates in order: a command waiting for a
// busy Transmission holds up only its own chat, and /stop then /start never
// run the other way round. Updates from anyone but the masters are handled
// at once and make no queue.
func (b *Bot) dispatch(ctx context.Context, _ *tgbot.Bot, update *models.Update) {
	chatID, from := updateOrigin(update)
	if from == nil || !b.Config.Masters.Contains(from.Username, from.ID) {
		b.handleUpdate(ctx, nil, update)
		return
	}
	b.queuesMu.Lock()
	queue, ok := b.queues[chatID]
	if !ok {
		if b.queues == nil {
			b.queues = make(map[int64]chan *models.Update)
		}
		queue = make(chan *models.Update, chatQueueSize)
		b.queues[chatID] = queue
		b.launch(ctx, "chat queue", func(ctx context.Context) { b.serveChat(ctx, queue) })
	}
	b.queuesMu.Unlock()
	select {
	case queue <- update:
	case <-ctx.Done():
	}
}

// serveChat handles a chat's updates one at a time until ctx ends.
func (b *Bot) serveChat(ctx context.Context, queue <-chan *models.Update) {
	for {
		select {
		case <-ctx.Done():
			return
		case update := <-queue:
			b.serveUpdate(ctx, update)
		}
	}
}

// serveUpdate handles one update from a chat's queue. When a message takes
// a while, the chat shows the bot typing. A panic is logged, and the chat's
// queue goes on.
func (b *Bot) serveUpdate(ctx context.Context, update *models.Update) {
	defer b.recoverPanic("chat update", "update_id", update.ID)
	if update.Message != nil {
		handled := make(chan struct{})
		defer close(handled)
		chatID := update.Message.Chat.ID
		b.launch(ctx, "typing indicator", func(ctx context.Context) {
			delay := b.typingDelay
			if delay <= 0 {
				delay = time.Second
			}
			timer := time.NewTimer(delay)
			defer timer.Stop()
			select {
			case <-handled:
			case <-ctx.Done():
			case <-timer.C:
				_, _ = b.API.SendChatAction(ctx, &tgbot.SendChatActionParams{ChatID: chatID, Action: models.ChatActionTyping})
			}
		})
	}
	b.handleUpdate(ctx, nil, update)
}

// updateOrigin returns the chat an update comes from, and who sent it.
func updateOrigin(update *models.Update) (int64, *models.User) {
	switch {
	case update == nil:
	case update.Message != nil:
		return update.Message.Chat.ID, update.Message.From
	case update.CallbackQuery != nil:
		from := &update.CallbackQuery.From
		switch message := update.CallbackQuery.Message; {
		case message.Message != nil:
			return message.Message.Chat.ID, from
		case message.InaccessibleMessage != nil:
			return message.InaccessibleMessage.Chat.ID, from
		}
		return from.ID, from
	}
	return 0, nil
}

// launch runs fn in the background; Start waits for it at shutdown. A
// panic in fn is logged rather than ending the bot.
func (b *Bot) launch(ctx context.Context, operation string, fn func(context.Context)) {
	b.wg.Add(1)
	go func() {
		defer b.wg.Done()
		defer b.recoverPanic(operation)
		fn(ctx)
	}()
}

// recoverPanic, deferred, logs a panic and stops it there.
func (b *Bot) recoverPanic(operation string, attrs ...any) {
	if recovered := recover(); recovered != nil {
		b.Logger.Error("Recovered from a panic",
			append([]any{"operation", operation, "panic", recovered, "stack", string(debug.Stack())}, attrs...)...)
	}
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
		if b.Config.Masters.Setup() && userID > 0 && b.firstSetupReply(userID) {
			b.reply(ctx, &request{chatID: update.Message.Chat.ID}, fmt.Sprintf(
				"Your Telegram user ID is <code>%d</code>. To let this account use the bot, start it with "+
					"<code>-master=%d</code> or <code>TT_MASTERS=%d</code> instead of the placeholder 1.", userID, userID, userID))
		}
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

	b.rememberChat(update.Message.Chat.ID)

	b.Logger.Info("Received message",
		"sender", username,
		"user_id", userID,
		"chat_id", update.Message.Chat.ID,
		"message_id", update.Message.ID,
		"has_document", update.Message.Document != nil,
	)

	r := &request{chatID: update.Message.Chat.ID, message: update.Message}
	if len(fields) == 0 {
		if update.Message.Document != nil {
			b.receiveTorrent(ctx, r)
		}
		return
	}

	if isLink {
		r.args = fields
	} else {
		r.args = fields[1:]
	}

	b.Logger.Info("Dispatching command", "command", command, "arg_count", len(r.args), "sender", username)

	if handler, ok := b.commands[command]; ok {
		handler(ctx, r)
	} else if command == "" {
		b.receiveTorrent(ctx, r)
	} else {
		b.Logger.Warn("Unknown command", "sender", username, "user_id", userID)
		b.reply(ctx, r, "No such command, try /help")
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

// firstSetupReply reports whether userID has not yet been told their ID in
// setup mode, and notes that they now have: once is enough.
func (b *Bot) firstSetupReply(userID int64) bool {
	b.setupMu.Lock()
	defer b.setupMu.Unlock()
	if b.toldIDs == nil {
		b.toldIDs = make(map[int64]bool)
	}
	if b.toldIDs[userID] {
		return false
	}
	b.toldIDs[userID] = true
	return true
}

// rememberChat records the chat a master used last, where notifications go
// until a chat chooses its own. It is saved, so a restart does not lose it;
// it is written only when it changes.
func (b *Bot) rememberChat(chatID int64) {
	var changed bool
	b.State.Read(func(data *state.Data) { changed = data.LastChat != chatID })
	if !changed {
		return
	}
	if err := b.State.Update(func(data *state.Data) { data.LastChat = chatID }); err != nil {
		b.Logger.Warn("Could not save the chat notifications go to", "error", err)
	}
}
