package bot

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/pyed/transmission"
	"github.com/pyed/transmission-telegram/v2/internal/config"
	"github.com/pyed/transmission-telegram/v2/internal/state"
)

// testBotToken is a fake token used only in tests; it is not a real secret.
const testBotToken = "123456:TEST-TOKEN"

// testBotUsername is the username the fake Telegram server gives the bot.
const testBotUsername = "MyTransmissionBot"

// fakeTelegram is a minimal in-process Telegram Bot API server for tests.
type fakeTelegram struct {
	*httptest.Server
	mu            sync.Mutex
	texts         []string
	chats         []string
	parseModes    []string
	documents     []string
	callbackTexts []string
	textEditCalls int
	edits         [][2]string // message ID and text of each text edit
	fileBytes     []byte
	lastMessageID int

	// answers holds bodies for a method's next calls, in order, given
	// instead of its usual answer; calls counts each method's calls.
	answers map[string][]string
	calls   map[string]int
}

// script makes the next calls of method answer with bodies, in order.
func (f *fakeTelegram) script(method string, bodies ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answers[method] = append(f.answers[method], bodies...)
}

// callCount returns how many times method was called.
func (f *fakeTelegram) callCount(method string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[method]
}

// newFakeTelegram starts a fake Telegram Bot API server.
func newFakeTelegram(t *testing.T) *fakeTelegram {
	t.Helper()
	f := &fakeTelegram{answers: make(map[string][]string), calls: make(map[string]int)}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// File downloads: GET /file/bot<token>/<filePath>
		if strings.HasPrefix(r.URL.Path, "/file/") {
			_, _ = w.Write(f.fileBytes)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		f.mu.Lock()
		f.calls[method]++
		var scripted string
		if answers := f.answers[method]; len(answers) > 0 {
			scripted, f.answers[method] = answers[0], answers[1:]
		}
		f.mu.Unlock()
		if scripted != "" {
			_, _ = w.Write([]byte(scripted))
			return
		}

		switch method {
		case "sendMessage":
			f.mu.Lock()
			f.texts = append(f.texts, r.FormValue("text"))
			f.chats = append(f.chats, r.FormValue("chat_id"))
			f.parseModes = append(f.parseModes, r.FormValue("parse_mode"))
			f.lastMessageID++
			id := f.lastMessageID
			f.mu.Unlock()
			_, _ = fmt.Fprintf(w, `{"ok":true,"result":{"message_id":%d,"date":1,"chat":{"id":42,"type":"private"}}}`, id)
		case "sendDocument":
			file, _, err := r.FormFile("document")
			if err != nil {
				t.Errorf("sendDocument without a document: %v", err)
				return
			}
			content, _ := io.ReadAll(file)
			f.mu.Lock()
			f.documents = append(f.documents, string(content))
			f.mu.Unlock()
			_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":99,"date":1,"chat":{"id":42,"type":"private"}}}`))
		case "answerCallbackQuery":
			f.mu.Lock()
			f.callbackTexts = append(f.callbackTexts, r.FormValue("text"))
			f.mu.Unlock()
			_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
		case "editMessageReplyMarkup":
			_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":1,"date":1,"chat":{"id":42,"type":"private"}}}`))
		case "editMessageText":
			f.mu.Lock()
			f.textEditCalls++
			f.edits = append(f.edits, [2]string{r.FormValue("message_id"), r.FormValue("text")})
			f.mu.Unlock()
			_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":1,"date":1,"chat":{"id":42,"type":"private"}}}`))
		case "getFile":
			_, _ = w.Write([]byte(`{"ok":true,"result":{"file_id":"test-file-id","file_path":"bots/123456/upload.torrent"}}`))
		case "getMe":
			_, _ = w.Write([]byte(`{"ok":true,"result":{"id":123456,"is_bot":true,"first_name":"Transmission","username":"` + testBotUsername + `"}}`))
		case "getMyCommands":
			_, _ = w.Write([]byte(`{"ok":true,"result":[]}`))
		case "getWebhookInfo":
			_, _ = w.Write([]byte(`{"ok":true,"result":{"url":"","has_custom_certificate":false,"pending_update_count":0}}`))
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

// editsOf returns the texts messageID was edited to, in order.
func (f *fakeTelegram) editsOf(messageID string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var texts []string
	for _, edit := range f.edits {
		if edit[0] == messageID {
			texts = append(texts, edit[1])
		}
	}
	return texts
}

// sentChats returns the chat of each message sent, in order.
func (f *fakeTelegram) sentChats() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.chats...)
}

// waitForSent waits until n messages have been sent.
func (f *fakeTelegram) waitForSent(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for len(f.sentTexts()) < n {
		if time.Now().After(deadline) {
			t.Fatalf("waited for %d messages, %d were sent: %q", n, len(f.sentTexts()), f.sentTexts())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// sentParseModes returns the parse mode of each message sent, in order.
func (f *fakeTelegram) sentParseModes() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.parseModes...)
}

// sentDocuments returns the contents of the files sent, in order.
func (f *fakeTelegram) sentDocuments() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.documents...)
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

func (f *fakeTelegram) textEdits() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.textEditCalls
}

// newTestBot builds a Bot wired to a fake Telegram server and (optionally)
// a Transmission client, for testing command handlers directly.
func newTestBot(t *testing.T, api *tgbot.Bot, client *transmission.TransmissionClient) *Bot {
	t.Helper()
	b := &Bot{
		API:    api,
		Client: client,
		Config: &config.Config{},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		State:  memoryState(t),

		retryDelay: time.Millisecond,
	}
	b.registerCommands()
	return b
}

// memoryState is a state store kept in memory.
func memoryState(t *testing.T) *state.Store {
	t.Helper()
	store, err := state.Load("")
	if err != nil {
		t.Fatal(err)
	}
	return store
}

// message is a text message from user 12345 in chat 42.
func message(text string) *models.Update {
	return messageIn(42, text)
}

// messageIn is a text message from user 12345 in chatID.
func messageIn(chatID int64, text string) *models.Update {
	return &models.Update{Message: &models.Message{
		ID:   1,
		Text: text,
		From: &models.User{ID: 12345},
		Chat: models.Chat{ID: chatID, Type: models.ChatTypeSupergroup},
	}}
}
