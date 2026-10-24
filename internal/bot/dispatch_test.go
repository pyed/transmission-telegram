package bot

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-telegram/bot/models"
	"github.com/pyed/transmission"
	"github.com/pyed/transmission-telegram/v2/internal/config"
)

// Chats are handled side by side, and each chat's updates in order: a
// command waiting for a busy Transmission holds up only its own chat, and a
// command that takes a while shows the bot typing.
func TestChatsAreHandledSideBySideAndInOrder(t *testing.T) {
	ft := newFakeTelegram(t)
	fts := newFakeTransmission(t)
	client, err := transmission.New(fts.URL+"/transmission/rpc", "", "")
	if err != nil {
		t.Fatalf("failed to create transmission client: %v", err)
	}
	b := newTestBot(t, ft.newBot(t), client)
	b.Config.Masters = config.MasterSlice{"12345"}
	b.typingDelay = 100 * time.Millisecond

	stall := make(chan struct{})
	var released sync.Once
	release := func() { released.Do(func() { close(stall) }) }
	fts.setStall(stall)
	ctx, cancel := context.WithCancel(context.Background())
	defer func() {
		release() // before waiting for the handlers, should the test fail
		cancel()
		b.wg.Wait()
	}()

	b.dispatch(ctx, nil, messageIn(111, "/list"))
	b.dispatch(ctx, nil, messageIn(111, "/help"))
	b.dispatch(ctx, nil, messageIn(222, "/help"))

	ft.waitForSent(t, 1)
	if chats := ft.sentChats(); chats[0] != "222" {
		t.Fatalf("while chat 111 waits for Transmission, chat %s was answered", chats[0])
	}
	// Chat 111's /help, which needs nothing from Transmission, waits its turn.
	time.Sleep(300 * time.Millisecond)
	if texts := ft.sentTexts(); len(texts) != 1 {
		t.Fatalf("chat 111 was answered before Transmission answered: %q", texts)
	}

	release()
	ft.waitForSent(t, 3)
	texts, chats := ft.sentTexts(), ft.sentChats()
	if chats[1] != "111" || texts[1] != "No torrents" {
		t.Fatalf("first in chat 111 = %s %q, want the /list reply", chats[1], texts[1])
	}
	if chats[2] != "111" || !strings.Contains(texts[2], "Torrent lists") {
		t.Fatalf("second in chat 111 = %s %q, want /help", chats[2], texts[2])
	}

	cancel()
	b.wg.Wait()
	if got := ft.callCount("sendChatAction"); got != 1 {
		t.Fatalf("the bot showed itself typing %d times, want once, for the command that waited", got)
	}
}

// Updates from strangers are handled at once, without a queue of their own.
func TestStrangersGetNoQueue(t *testing.T) {
	ft := newFakeTelegram(t)
	b := newTestBot(t, ft.newBot(t), nil)
	b.Config.Masters = config.MasterSlice{"12345"}
	stranger := &models.Update{Message: &models.Message{
		Text: "/help", From: &models.User{ID: 999}, Chat: models.Chat{ID: 999, Type: models.ChatTypePrivate},
	}}

	b.dispatch(context.Background(), nil, stranger)
	b.queuesMu.Lock()
	defer b.queuesMu.Unlock()
	if len(b.queues) != 0 {
		t.Fatalf("a stranger's message made %d queues", len(b.queues))
	}
	if texts := ft.sentTexts(); len(texts) != 0 {
		t.Fatalf("answered a stranger: %q", texts)
	}
}

// A command that panics is logged, and its chat goes on being answered.
func TestAPanicDoesNotStopItsChat(t *testing.T) {
	ft := newFakeTelegram(t)
	b := newTestBot(t, ft.newBot(t), nil)
	b.Config.Masters = config.MasterSlice{"12345"}
	b.commands["boom"] = func(context.Context, *request) { panic("boom") }
	ctx, cancel := context.WithCancel(context.Background())
	defer func() {
		cancel()
		b.wg.Wait()
	}()

	b.dispatch(ctx, nil, messageIn(111, "/boom"))
	b.dispatch(ctx, nil, messageIn(111, "/help"))
	ft.waitForSent(t, 1)
	if text := ft.sentTexts()[0]; !strings.Contains(text, "Torrent lists") {
		t.Fatalf("after the panic, chat 111 got %q", text)
	}
}
