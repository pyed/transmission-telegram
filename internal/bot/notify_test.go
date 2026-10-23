package bot

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/pyed/transmission-telegram/v2/internal/notify"
	"github.com/pyed/transmission-telegram/v2/internal/state"
)

// Until a chat chooses, /notify shows the default: the chat a master used
// last gets completions and outages.
func TestNotifyShowsTheDefault(t *testing.T) {
	ft := newFakeTelegram(t)
	b := newTestBot(t, ft.newBot(t), nil)
	b.Config.LowDisk = 5_000_000_000
	if err := b.State.Update(func(data *state.Data) { data.LastChat = 42 }); err != nil {
		t.Fatal(err)
	}

	b.notify(context.Background(), req())
	text := ft.sentTexts()[0]
	for _, want := range []string{"✅ <b>completed</b>", "✅ <b>outages</b>", "⬜ <b>errors</b>", "⬜ <b>disk</b> — free space falls below 5.0 GB", "this one"} {
		if !strings.Contains(text, want) {
			t.Errorf("/notify does not show %q:\n%s", want, text)
		}
	}
	b.State.Read(func(data *state.Data) {
		if data.Notify != nil {
			t.Fatalf("looking changed the choices: %v", data.Notify)
		}
	})
}

// The first chat to choose ends the default, but the chat that had it keeps
// its completions and outages; later choices change only their own chat.
func TestNotifyChoices(t *testing.T) {
	ft := newFakeTelegram(t)
	b := newTestBot(t, ft.newBot(t), nil)
	if err := b.State.Update(func(data *state.Data) { data.LastChat = 42 }); err != nil {
		t.Fatal(err)
	}
	in := func(chatID int64, args ...string) *request { return &request{chatID: chatID, args: args} }

	b.notify(context.Background(), in(7, "errors", "on"))
	b.notify(context.Background(), in(8, "ON"))
	b.notify(context.Background(), in(42, "outages", "off"))
	b.notify(context.Background(), in(8, "disk", "off"))
	b.notify(context.Background(), in(9, "off"))

	b.State.Read(func(data *state.Data) {
		want := map[int64][]string{
			42: {notify.Completed},
			7:  {notify.Errors},
			8:  {notify.Completed, notify.Outages, notify.Errors},
			9:  {},
		}
		if len(data.Notify) != len(want) {
			t.Fatalf("choices = %v, want %v", data.Notify, want)
		}
		for chatID, kinds := range want {
			if !slices.Equal(data.Notify[chatID], kinds) {
				t.Errorf("chat %d gets %v, want %v", chatID, data.Notify[chatID], kinds)
			}
		}
		if got := notify.Chats(data, notify.Errors); !slices.Equal(got, []int64{7, 8}) {
			t.Errorf("errors go to %v", got)
		}
	})
	if last := ft.sentTexts()[4]; !strings.Contains(last, "⬜ <b>completed</b>") || strings.Contains(last, "✅") {
		t.Errorf("after /notify off, the chat is shown %q", last)
	}
}

// A kind that does not exist changes nothing, and the reply names the kinds.
func TestNotifyRefusesUnknownKinds(t *testing.T) {
	ft := newFakeTelegram(t)
	b := newTestBot(t, ft.newBot(t), nil)
	b.notify(context.Background(), req("everything", "on"))
	b.State.Read(func(data *state.Data) {
		if data.Notify != nil {
			t.Fatalf("an unknown kind changed the choices: %v", data.Notify)
		}
	})
	if text := ft.sentTexts()[0]; !strings.Contains(text, "completed, outages, errors, disk") {
		t.Fatalf("reply = %q", text)
	}
}
