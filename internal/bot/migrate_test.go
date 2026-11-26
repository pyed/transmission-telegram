package bot

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/pyed/transmission-telegram/v2/internal/state"
)

// upgraded is Telegram's answer for a group that became supergroup -1009.
const upgraded = `{"ok":false,"error_code":400,"description":"Bad Request: group chat was upgraded to a supergroup chat","parameters":{"migrate_to_chat_id":-1009}}`

// A message to a group that became a supergroup goes to the new chat, and
// what the bot remembers of the group moves there, but its status message.
func TestMessagesFollowAGroupToItsSupergroup(t *testing.T) {
	ft := newFakeTelegram(t)
	b := newTestBot(t, ft.newBot(t), nil)
	if err := b.State.Update(func(data *state.Data) {
		data.LastChat = -4001
		data.Notify = map[state.Place][]string{at(-4001): {"completed"}}
		data.Sorts = map[int64]string{-4001: "rev size"}
		data.Status = map[state.Place]int{at(-4001): 5}
	}); err != nil {
		t.Fatal(err)
	}
	ft.script("sendMessage", upgraded)
	if err := b.SendHTML(context.Background(), at(-4001), "✅ <b>Finished:</b> x", nil); err != nil {
		t.Fatalf("SendHTML = %v", err)
	}
	if chats := ft.sentChats(); !slices.Equal(chats, []string{"-1009"}) {
		t.Fatalf("sent to %q", chats)
	}
	b.State.Read(func(data *state.Data) {
		if data.LastChat != -1009 || data.Notify[at(-1009)][0] != "completed" || data.Sorts[-1009] != "rev size" ||
			len(data.Notify) != 1 || len(data.Status) != 0 {
			t.Fatalf("state after the move: %+v", data)
		}
	})

	// A long reply, attached as a file, follows the group too.
	ft.script("sendDocument", upgraded)
	if to, err := b.sendFile(context.Background(), at(-4001), "long"); err != nil || to != at(-1009) || len(ft.sentDocuments()) != 1 {
		t.Fatalf("sendFile = %v, %v; documents %d", to, err, len(ft.sentDocuments()))
	}
}

// A digest due in a group that became a supergroup comes once, in the new
// chat, and the next one is due there as usual.
func TestDigestsFollowAGroupToItsSupergroup(t *testing.T) {
	b, ft, _, now := digestBot(t)
	ctx := context.Background()
	b.digest(ctx, req("09:00"))
	if err := b.State.Update(func(data *state.Data) { data.MoveChat(42, -4001) }); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(time.Hour)
	ft.script("sendMessage", upgraded)
	b.checkDigest(ctx)
	b.checkDigest(ctx)
	if chats := ft.sentChats(); !slices.Equal(chats, []string{"42", "-1009"}) {
		t.Fatalf("sent to %q", chats)
	}
	b.State.Read(func(data *state.Data) {
		if d, ok := data.Digest[at(-1009)]; !ok || d.LastDue != "2026-10-21" || len(data.Digest) != 1 {
			t.Fatalf("digests after the move: %+v", data.Digest)
		}
	})
}
