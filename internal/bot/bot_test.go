package bot

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/pyed/transmission"
	"github.com/pyed/transmission-telegram/v2/internal/config"
	"github.com/pyed/transmission-telegram/v2/internal/state"
)

// The chat a master used last is saved, so notifications still reach it
// after a restart, and written only when it changes.
func TestTheLastChatIsSaved(t *testing.T) {
	ft := newFakeTelegram(t)
	b := newTestBot(t, ft.newBot(t), nil)
	b.Config.Masters = config.MasterSlice{"12345"}
	path := filepath.Join(t.TempDir(), "state.json")
	store, err := state.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	b.State = store

	b.handleUpdate(context.Background(), nil, messageIn(111, "/help"))
	first, err := os.Stat(path)
	if err != nil {
		t.Fatalf("the last chat was not saved: %v", err)
	}
	reloaded, err := state.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	reloaded.Read(func(data *state.Data) {
		if data.LastChat != 111 {
			t.Fatalf("saved last chat = %d, want 111", data.LastChat)
		}
	})

	if err := os.Chtimes(path, first.ModTime().Add(-time.Hour), first.ModTime().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	b.handleUpdate(context.Background(), nil, messageIn(111, "/help"))
	if again, _ := os.Stat(path); !again.ModTime().Before(first.ModTime()) {
		t.Fatal("the state was written again for the same chat")
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
	b.State.Read(func(data *state.Data) {
		if data.LastChat != 0 {
			t.Fatalf("another bot's command moved notifications to chat %d", data.LastChat)
		}
	})

	b.handleUpdate(context.Background(), nil, message("/stop@mytransmissionbot all"))
	b.handleUpdate(context.Background(), nil, message("/stop all"))
	if got := fts.methodCount("torrent-stop"); got != 2 {
		t.Fatalf("expected both of this bot's commands to stop torrents, got %d calls", got)
	}
}

// A bot that starts before the network is up, as at boot, waits for
// Telegram instead of exiting: server errors and pages that are not
// Telegram's JSON are retried.
func TestNewWaitsForTelegram(t *testing.T) {
	shortenStartupDelay(t)
	ft := newFakeTelegram(t)
	ft.script("getMe",
		`{"ok":false,"error_code":502,"description":"Bad Gateway"}`,
		`<html>502 Bad Gateway</html>`,
	)
	cfg := &config.Config{BotToken: testBotToken}
	if _, err := New(context.Background(), cfg, nil, memoryState(t), slog.New(slog.NewTextHandler(io.Discard, nil)), tgbot.WithServerURL(ft.URL)); err != nil {
		t.Fatalf("New gave up on a passing failure: %v", err)
	}
	if got := ft.callCount("getMe"); got != 3 {
		t.Fatalf("expected two failed getMe calls and a third that worked, got %d calls", got)
	}
}

// A token Telegram refuses cannot start working by itself, so New stops at
// once and says what to check.
func TestNewStopsWhenTelegramRefusesTheToken(t *testing.T) {
	shortenStartupDelay(t)
	ft := newFakeTelegram(t)
	ft.script("getMe", `{"ok":false,"error_code":401,"description":"Unauthorized"}`)
	cfg := &config.Config{BotToken: testBotToken}
	_, err := New(context.Background(), cfg, nil, memoryState(t), slog.New(slog.NewTextHandler(io.Discard, nil)), tgbot.WithServerURL(ft.URL))
	if err == nil || !strings.Contains(err.Error(), "refused the bot token") {
		t.Fatalf("expected a refused-token error, got %v", err)
	}
	if got := ft.callCount("getMe"); got != 1 {
		t.Fatalf("retried a refused token: %d getMe calls", got)
	}
}

// Shutting down while Telegram is unreachable ends the wait.
func TestNewStopsWaitingWhenCanceled(t *testing.T) {
	ft := newFakeTelegram(t)
	ft.script("getMe", `{"ok":false,"error_code":502,"description":"Bad Gateway"}`)
	ctx, cancel := context.WithCancel(context.Background())
	restore := startupDelay
	startupDelay = func(int) time.Duration {
		cancel()
		return time.Hour
	}
	t.Cleanup(func() { startupDelay = restore })
	cfg := &config.Config{BotToken: testBotToken}
	done := make(chan error, 1)
	go func() {
		_, err := New(ctx, cfg, nil, memoryState(t), slog.New(slog.NewTextHandler(io.Discard, nil)), tgbot.WithServerURL(ft.URL))
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected the cancellation, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("New kept waiting after shutdown")
	}
}

func shortenStartupDelay(t *testing.T) {
	t.Helper()
	restore := startupDelay
	startupDelay = func(int) time.Duration { return time.Millisecond }
	t.Cleanup(func() { startupDelay = restore })
}

// New asks Telegram who the bot is once and keeps its username, which the
// mention check needs.
func TestNewKeepsTheBotUsername(t *testing.T) {
	ft := newFakeTelegram(t)
	cfg := &config.Config{BotToken: testBotToken, Masters: config.MasterSlice{"12345"}}
	b, err := New(context.Background(), cfg, nil, memoryState(t), slog.New(slog.NewTextHandler(io.Discard, nil)), tgbot.WithServerURL(ft.URL))
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	if b.username != testBotUsername {
		t.Fatalf("username = %q, want %q", b.username, testBotUsername)
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
