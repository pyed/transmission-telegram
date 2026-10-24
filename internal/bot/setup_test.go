package bot

import (
	"context"
	"strings"
	"testing"

	"github.com/go-telegram/bot/models"
	"github.com/pyed/transmission-telegram/v2/internal/config"
)

// While the only master is the placeholder 1, the bot tells whoever writes
// to it their user ID, once; otherwise strangers get no answer.
func TestSetupModeTellsPeopleTheirID(t *testing.T) {
	ft := newFakeTelegram(t)
	b := newTestBot(t, ft.newBot(t), nil)
	stranger := &models.Update{Message: &models.Message{
		Text: "/help", From: &models.User{ID: 987654321}, Chat: models.Chat{ID: 987654321, Type: models.ChatTypePrivate},
	}}

	b.Config.Masters = config.MasterSlice{"12345"}
	b.handleUpdate(context.Background(), nil, stranger)
	if texts := ft.sentTexts(); len(texts) != 0 {
		t.Fatalf("a stranger was answered outside setup: %q", texts)
	}

	b.Config.Masters = config.MasterSlice{config.SetupMaster}
	b.handleUpdate(context.Background(), nil, stranger)
	b.handleUpdate(context.Background(), nil, stranger)
	texts := ft.sentTexts()
	if len(texts) != 1 || !strings.Contains(texts[0], "<code>987654321</code>") || !strings.Contains(texts[0], "-master=987654321") {
		t.Fatalf("setup replies = %q", texts)
	}
}

// /whoami shows a master their user ID and the chat's ID.
func TestWhoami(t *testing.T) {
	ft := newFakeTelegram(t)
	b := newTestBot(t, ft.newBot(t), nil)
	b.Config.Masters = config.MasterSlice{"12345"}
	b.handleUpdate(context.Background(), nil, messageIn(-100777, "/whoami"))
	if texts := ft.sentTexts(); len(texts) != 1 || texts[0] != "Your Telegram user ID: <code>12345</code>\nThis chat's ID: <code>-100777</code>" {
		t.Fatalf("/whoami = %q", texts)
	}
}
