package bot

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/pyed/transmission"
	"github.com/pyed/transmission-telegram/v2/internal/config"
)

func TestBot_ChatID(t *testing.T) {
	b := &Bot{}
	if b.ChatID() != 0 {
		t.Errorf("expected initial ChatID to be 0")
	}

	atomic.StoreInt64(&b.chatID, 123456789)
	if b.ChatID() != 123456789 {
		t.Errorf("expected ChatID to be 123456789, got %d", b.ChatID())
	}
}

func TestBot_MarkdownReplacer(t *testing.T) {
	replacer := strings.NewReplacer(
		"*", "•",
		"[", "(",
		"]", ")",
		"_", "-",
		"`", "'",
	)

	input := "[Torrent_Name] *2026* `1080p`"
	expected := "(Torrent-Name) •2026• '1080p'"
	got := replacer.Replace(input)
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

func TestBot_CommandRegistration(t *testing.T) {
	b := &Bot{
		Config: &config.Config{},
	}
	b.registerCommands()

	expectedCommands := []string{
		"list", "li", "ls",
		"head", "he",
		"tail", "ta",
		"downs", "dg",
		"seeding", "sd",
		"paused", "pa",
		"checking", "ch",
		"active", "ac",
		"errors", "er",
		"sort", "so",
		"trackers", "tr",
		"downloaddir", "dd",
		"add", "ad",
		"search", "se",
		"latest", "la",
		"info", "in",
		"stop", "sp",
		"start", "st",
		"check", "ck",
		"stats", "sa",
		"downlimit", "dl",
		"uplimit", "ul",
		"speed", "ss",
		"count", "co",
		"del", "rm",
		"deldata",
		"turtle", "alt", "tu",
		"free", "space", "disk",
		"reannounce", "ra",
		"help",
		"version", "ver",
	}

	for _, cmd := range expectedCommands {
		if _, ok := b.commands[cmd]; !ok {
			t.Errorf("expected command %q to be registered", cmd)
		}
	}
}

func TestNormalizeCommand(t *testing.T) {
	tests := []struct {
		input       string
		wantCmd     string
		wantMention string
		wantLink    bool
	}{
		{"/list", "list", "", false},
		{"/list@MyTransmissionBot", "list", "mytransmissionbot", false},
		{"/LiSt@my_bot", "list", "my_bot", false},
		{"list", "list", "", false},
		{"/info@TransmissionBot", "info", "transmissionbot", false},
		{"/help", "help", "", false},
		{"magnet:?xt=urn:btih:xyz", "add", "", true},
		{"http://example.com/test.torrent", "add", "", true},
		{"https://example.com/test.torrent", "add", "", true},
		{"HTTPS://example.com/test.torrent", "add", "", true},
		{"MAGNET:?xt=urn:btih:xyz", "add", "", true},
		{"http-not-a-url", "http-not-a-url", "", false},
		{"/add", "add", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			cmd, mention, isLink := normalizeCommand(tt.input)
			if cmd != tt.wantCmd || mention != tt.wantMention || isLink != tt.wantLink {
				t.Errorf("normalizeCommand(%q) = (%q, %q, %v), want (%q, %q, %v)",
					tt.input, cmd, mention, isLink, tt.wantCmd, tt.wantMention, tt.wantLink)
			}
		})
	}
}

// A bot that sees every group message also receives the commands meant for
// other bots, such as /stop@OtherBot all. Only commands addressed to this
// bot, or to no bot, are its own.
func TestCommandsForOtherBotsAreIgnored(t *testing.T) {
	ft := newFakeTelegram(t)
	fts := newFakeTransmission(t)
	client, err := transmission.New(fts.URL+"/transmission/rpc", "", "")
	if err != nil {
		t.Fatalf("failed to create transmission client: %v", err)
	}
	b := newTestBot(t, ft.newBot(t), client)
	b.Config.Masters = config.MasterSlice{"12345"}
	b.username = testBotUsername

	b.handleUpdate(context.Background(), nil, message("/stop@OtherBot all"))
	if got := fts.methodCount("torrent-stop"); got != 0 {
		t.Fatalf("another bot's command stopped torrents (%d calls)", got)
	}
	if texts := ft.sentTexts(); len(texts) != 0 {
		t.Fatalf("answered another bot's command: %v", texts)
	}
	if got := b.ChatID(); got != 0 {
		t.Fatalf("another bot's command moved notifications to chat %d", got)
	}

	b.handleUpdate(context.Background(), nil, message("/stop@mytransmissionbot all"))
	b.handleUpdate(context.Background(), nil, message("/stop all"))
	if got := fts.methodCount("torrent-stop"); got != 2 {
		t.Fatalf("expected both of this bot's commands to stop torrents, got %d calls", got)
	}
}

// New asks Telegram who the bot is once and keeps its username, which the
// mention check needs.
func TestNewKeepsTheBotUsername(t *testing.T) {
	ft := newFakeTelegram(t)
	cfg := &config.Config{BotToken: testBotToken, Masters: config.MasterSlice{"12345"}}
	b, err := New(context.Background(), cfg, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), tgbot.WithServerURL(ft.URL))
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	if b.username != testBotUsername {
		t.Fatalf("username = %q, want %q", b.username, testBotUsername)
	}
}

func TestDefaultBotCommands(t *testing.T) {
	b := &Bot{
		Config: &config.Config{},
	}
	b.registerCommands()

	cmds := defaultBotCommands()
	if len(cmds) == 0 {
		t.Fatalf("expected non-empty defaultBotCommands")
	}

	for _, c := range cmds {
		if strings.HasPrefix(c.Command, "/") {
			t.Errorf("command %q should not have leading slash", c.Command)
		}
		if c.Description == "" {
			t.Errorf("command %q has empty description", c.Command)
		}
		if _, ok := b.commands[c.Command]; !ok {
			t.Errorf("menu command %q is not registered in bot handlers", c.Command)
		}
	}

	expectedPrimary := []string{
		"list", "head", "tail", "active", "downs", "seeding", "paused", "checking", "errors",
		"latest", "search", "info", "add", "stop", "start", "check", "del", "deldata",
		"speed", "stats", "free", "turtle", "reannounce", "downlimit", "uplimit",
		"downloaddir", "sort", "trackers", "count", "help", "version",
	}
	menu := make(map[string]bool, len(cmds))
	for _, command := range cmds {
		menu[command.Command] = true
	}
	for _, command := range expectedPrimary {
		if !menu[command] {
			t.Errorf("primary command %q is missing from the Telegram menu", command)
		}
	}
}

func TestClearWebhookSkipsDeleteWhenUnset(t *testing.T) {
	var deleteCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/getWebhookInfo") {
			_, _ = w.Write([]byte(`{"ok":true,"result":{"url":"","has_custom_certificate":false,"pending_update_count":0}}`))
			return
		}
		deleteCalls.Add(1)
	}))
	defer server.Close()

	api, err := tgbot.New(testBotToken, tgbot.WithServerURL(server.URL), tgbot.WithSkipGetMe())
	if err != nil {
		t.Fatalf("create test bot: %v", err)
	}
	if err := clearWebhook(context.Background(), api); err != nil {
		t.Fatalf("clear absent webhook: %v", err)
	}
	if got := deleteCalls.Load(); got != 0 {
		t.Fatalf("deleteWebhook called %d times for an absent webhook", got)
	}
}

func TestHelpCoversEveryMenuCommand(t *testing.T) {
	for _, command := range defaultBotCommands() {
		if !strings.Contains(config.Help, "/"+command.Command) {
			t.Errorf("help text does not mention /%s", command.Command)
		}
	}
}

func TestInfoKeyboard(t *testing.T) {
	kb := infoKeyboard(42, "0123456789abcdef0123456789abcdef01234567")
	if kb == nil || len(kb.InlineKeyboard) != 1 || len(kb.InlineKeyboard[0]) != 3 {
		t.Fatalf("expected 1 row of 3 buttons, got %v", kb)
	}

	buttons := kb.InlineKeyboard[0]
	if buttons[0].CallbackData != "cmd:stop:42:0123456789abcdef" {
		t.Errorf("unexpected pause callback %q", buttons[0].CallbackData)
	}
	if buttons[1].CallbackData != "cmd:start:42:0123456789abcdef" {
		t.Errorf("unexpected resume callback %q", buttons[1].CallbackData)
	}
	if buttons[2].CallbackData != "cmd:del:42:0123456789abcdef" {
		t.Errorf("unexpected delete callback %q", buttons[2].CallbackData)
	}
}

func TestDeleteConfirmationKeyboard(t *testing.T) {
	kb := deleteConfirmationKeyboard(42, "ABCDEF0123456789ABCDEF0123456789ABCDEF01")
	if kb == nil || len(kb.InlineKeyboard) != 1 || len(kb.InlineKeyboard[0]) != 2 {
		t.Fatalf("expected a confirmation row with two buttons, got %v", kb)
	}

	buttons := kb.InlineKeyboard[0]
	if buttons[0].CallbackData != "cmd:confirm-del:42:abcdef0123456789" {
		t.Errorf("unexpected confirmation callback %q", buttons[0].CallbackData)
	}
	if buttons[1].CallbackData != "cmd:cancel:42:abcdef0123456789" {
		t.Errorf("unexpected cancel callback %q", buttons[1].CallbackData)
	}
}

func TestCallbackDeleteRequiresMatchingConfirmation(t *testing.T) {
	ft := newFakeTelegram(t)
	fts := newFakeTransmission(t)
	fts.torrents = transmission.Torrents{{
		ID:         7,
		Name:       "Test Torrent",
		HashString: "0123456789abcdef0123456789abcdef01234567",
	}}
	client, err := transmission.New(fts.URL+"/transmission/rpc", "", "")
	if err != nil {
		t.Fatalf("failed to create transmission client: %v", err)
	}

	b := newTestBot(t, ft.newBot(t), client)
	b.Config.Masters = config.MasterSlice{"12345"}
	callback := func(data string) *models.CallbackQuery {
		return &models.CallbackQuery{
			ID:   "callback-id",
			From: models.User{ID: 12345},
			Data: data,
			Message: models.MaybeInaccessibleMessage{
				Type: models.MaybeInaccessibleMessageTypeMessage,
				Message: &models.Message{
					ID:   99,
					Date: 1,
					Chat: models.Chat{ID: 42, Type: models.ChatTypePrivate},
				},
			},
		}
	}

	b.handleCallbackQuery(context.Background(), callback("cmd:del:7:0123456789abcdef"))
	if got := fts.methodCount("torrent-remove"); got != 0 {
		t.Fatalf("delete button removed the torrent before confirmation (%d calls)", got)
	}
	if got := ft.markupEdits(); got != 1 {
		t.Fatalf("expected confirmation keyboard edit, got %d", got)
	}

	b.handleCallbackQuery(context.Background(), callback("cmd:confirm-del:7:stale-fingerprint"))
	if got := fts.methodCount("torrent-remove"); got != 0 {
		t.Fatalf("stale confirmation removed the torrent (%d calls)", got)
	}

	b.handleCallbackQuery(context.Background(), callback("cmd:confirm-del:7:0123456789abcdef"))
	if got := fts.methodCount("torrent-remove"); got != 1 {
		t.Fatalf("expected one confirmed torrent removal, got %d", got)
	}
	if got := ft.markupEdits(); got != 2 {
		t.Fatalf("expected confirmation and cleanup keyboard edits, got %d", got)
	}

	answers := ft.callbackAnswers()
	if len(answers) != 3 || answers[0] != "Confirm deletion below" ||
		answers[1] != "Torrent changed; run /info again" || answers[2] != "Deleted: Test Torrent" {
		t.Fatalf("unexpected callback answers: %v", answers)
	}
}

func TestLegacyDeleteCallbackExpiresSafely(t *testing.T) {
	ft := newFakeTelegram(t)
	fts := newFakeTransmission(t)
	client, err := transmission.New(fts.URL+"/transmission/rpc", "", "")
	if err != nil {
		t.Fatalf("failed to create transmission client: %v", err)
	}
	b := newTestBot(t, ft.newBot(t), client)
	b.Config.Masters = config.MasterSlice{"12345"}

	b.handleCallbackQuery(context.Background(), &models.CallbackQuery{
		ID:   "callback-id",
		From: models.User{ID: 12345},
		Data: "cmd:del:7",
	})

	if got := fts.methodCount("torrent-remove"); got != 0 {
		t.Fatalf("legacy callback removed a torrent (%d calls)", got)
	}
	if answers := ft.callbackAnswers(); len(answers) != 1 || answers[0] != "Delete button expired; run /info again" {
		t.Fatalf("unexpected callback answer: %v", answers)
	}
}

func TestRecoverMiddlewareContainsHandlerPanic(t *testing.T) {
	var logs bytes.Buffer
	b := &Bot{Logger: slog.New(slog.NewTextHandler(&logs, nil))}
	handler := b.recoverMiddleware(func(context.Context, *tgbot.Bot, *models.Update) {
		panic("malformed update")
	})

	handler(context.Background(), nil, &models.Update{ID: 123})

	logOutput := logs.String()
	if !strings.Contains(logOutput, "Recovered from Telegram update panic") ||
		!strings.Contains(logOutput, "update_id=123") {
		t.Fatalf("expected recovered panic to be logged, got %q", logOutput)
	}
}

func TestRecoverMiddlewareContainsHandlerPanicWithNilUpdate(t *testing.T) {
	var logs bytes.Buffer
	b := &Bot{Logger: slog.New(slog.NewTextHandler(&logs, nil))}
	handler := b.recoverMiddleware(func(context.Context, *tgbot.Bot, *models.Update) {
		panic("nil update")
	})

	handler(context.Background(), nil, nil)

	logOutput := logs.String()
	if !strings.Contains(logOutput, "Recovered from Telegram update panic") ||
		!strings.Contains(logOutput, "update_id=0") {
		t.Fatalf("expected nil-update panic to be contained, got %q", logOutput)
	}
}
