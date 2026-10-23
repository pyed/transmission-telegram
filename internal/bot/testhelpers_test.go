package bot

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	tgbot "github.com/go-telegram/bot"
	"github.com/pyed/transmission"
	"github.com/pyed/transmission-telegram/v2/internal/config"
)

// testBotToken is a fake token used only in tests; it is not a real secret.
const testBotToken = "123456:TEST-TOKEN"

// fakeTelegram is a minimal in-process Telegram Bot API server for tests.
type fakeTelegram struct {
	*httptest.Server
	mu              sync.Mutex
	texts           []string
	callbackTexts   []string
	markupEditCalls int
	textEditCalls   int
	fileBytes       []byte
}

// newFakeTelegram starts a fake Telegram Bot API server.
func newFakeTelegram(t *testing.T) *fakeTelegram {
	t.Helper()
	f := &fakeTelegram{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// File downloads: GET /file/bot<token>/<filePath>
		if strings.HasPrefix(r.URL.Path, "/file/") {
			_, _ = w.Write(f.fileBytes)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/sendMessage"):
			f.mu.Lock()
			f.texts = append(f.texts, r.FormValue("text"))
			f.mu.Unlock()
			_, _ = w.Write([]byte(`{"ok":true,"result":{"id":1,"chat":{"id":1}}}`))
		case strings.HasSuffix(r.URL.Path, "/answerCallbackQuery"):
			f.mu.Lock()
			f.callbackTexts = append(f.callbackTexts, r.FormValue("text"))
			f.mu.Unlock()
			_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
		case strings.HasSuffix(r.URL.Path, "/editMessageReplyMarkup"):
			f.mu.Lock()
			f.markupEditCalls++
			f.mu.Unlock()
			_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":1,"date":1,"chat":{"id":42,"type":"private"}}}`))
		case strings.HasSuffix(r.URL.Path, "/editMessageText"):
			f.mu.Lock()
			f.textEditCalls++
			f.mu.Unlock()
			_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":1,"date":1,"chat":{"id":42,"type":"private"}}}`))
		case strings.HasSuffix(r.URL.Path, "/getFile"):
			_, _ = w.Write([]byte(`{"ok":true,"result":{"file_id":"test-file-id","file_path":"bots/123456/upload.torrent"}}`))
		default:
			// sendChatAction, editMessageText, deleteWebhook, etc.
			_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
		}
	}))
	t.Cleanup(f.Close)
	return f
}

// newBot returns a *tgbot.Bot wired to this fake server.
func (f *fakeTelegram) newBot(t *testing.T) *tgbot.Bot {
	t.Helper()
	api, err := tgbot.New(testBotToken, tgbot.WithServerURL(f.Server.URL), tgbot.WithSkipGetMe())
	if err != nil {
		t.Fatalf("failed to create test bot: %v", err)
	}
	return api
}

// sentTexts returns the texts of all sendMessage calls, in order.
func (f *fakeTelegram) sentTexts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.texts...)
}

func (f *fakeTelegram) callbackAnswers() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.callbackTexts...)
}

func (f *fakeTelegram) markupEdits() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.markupEditCalls
}

func (f *fakeTelegram) textEdits() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.textEditCalls
}

// newTestBot builds a Bot wired to a fake Telegram server and (optionally)
// a Transmission client, for testing command handlers directly.
func newTestBot(t *testing.T, api *tgbot.Bot, client *transmission.TransmissionClient) *Bot {
	t.Helper()
	return &Bot{
		API:    api,
		Client: client,
		Config: &config.Config{},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		mdReplacer: strings.NewReplacer(
			"*", "•",
			"[", "(",
			"]", ")",
			"_", "-",
			"`", "'",
		),
	}
}
