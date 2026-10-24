package bot

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/pyed/transmission"
	"github.com/pyed/transmission-telegram/v2/internal/config"
	"github.com/pyed/transmission-telegram/v2/internal/state"
)

// statusBot is a bot whose master is 12345, with three torrents, one of
// them failing, and a download limit, at 14:05.
func statusBot(t *testing.T) (*Bot, *fakeTelegram, *fakeTransmission) {
	t.Helper()
	ft := newFakeTelegram(t)
	fts := newFakeTransmission(t)
	fts.session = `{"version":"test","download-dir":"/downloads","speed-limit-down":500,"speed-limit-down-enabled":true,` +
		`"speed-limit-up":20,"alt-speed-down":40,"alt-speed-up":10}`
	fts.stats = `{"downloadSpeed":2000000,"uploadSpeed":3000,"current-stats":{"downloadedBytes":5000000000,"uploadedBytes":7000000}}`
	fts.answers = map[string]string{"free-space": `{"path":"/downloads","size-bytes":1200000000000}`}
	fts.torrents = transmission.Torrents{
		{ID: 1, Status: transmission.StatusDownloading},
		{ID: 2, Status: transmission.StatusSeeding, Error: transmission.ErrorTrackerError},
		{ID: 3, Status: transmission.StatusStopped},
	}
	client, err := transmission.New(fts.URL+"/transmission/rpc", "", "")
	if err != nil {
		t.Fatalf("failed to create transmission client: %v", err)
	}
	b := newTestBot(t, ft.newBot(t), client)
	b.Config.Masters = config.MasterSlice{"12345"}
	b.now = func() time.Time { return time.Date(2026, 10, 25, 14, 5, 0, 0, time.Local) }
	return b, ft, fts
}

// savedStatus is chat 42's status message, 0 for none.
func savedStatus(b *Bot) int {
	var messageID int
	b.State.Read(func(data *state.Data) { messageID = data.Status[42] })
	return messageID
}

func TestRenderStatus(t *testing.T) {
	b, _, fts := statusBot(t)
	want := "📊 <b>Transmission</b>\n" +
		"↓ 2.0 MB/s · limit 500 KB/s · 5.0 GB this session\n" +
		"↑ 3.0 kB/s · no limit · 7.0 MB this session\n" +
		"Torrents: 3 · 1 downloading · 1 seeding · 1 with errors\n" +
		"Free space: 1.2 TB\n" +
		"<i>Updated 14:05</i>"
	if got := b.renderStatus(context.Background()); got != want {
		t.Fatalf("status =\n%s\nwant\n%s", got, want)
	}

	// In turtle mode, its limits are the ones that count.
	fts.set(func() {
		fts.session = `{"version":"test","speed-limit-down":500,"speed-limit-down-enabled":true,` +
			`"alt-speed-enabled":true,"alt-speed-down":40,"alt-speed-up":10}`
		fts.torrents = fts.torrents[:1]
	})
	got := b.renderStatus(context.Background())
	for _, line := range []string{"· 🐢 limit 40 KB/s ·", "· 🐢 limit 10 KB/s ·", "Torrents: 1 · 1 downloading · 0 seeding\n"} {
		if !strings.Contains(got, line) {
			t.Errorf("turtle-mode status lacks %q:\n%s", line, got)
		}
	}

	// Free space Transmission cannot tell is left out.
	fts.set(func() { fts.answers = map[string]string{"free-space": `{"size-bytes":-1}`} })
	if got := b.renderStatus(context.Background()); strings.Contains(got, "Free space") {
		t.Errorf("status = %q", got)
	}
}

func TestRenderStatusWhenTransmissionIsDown(t *testing.T) {
	ft := newFakeTelegram(t)
	// New gives a client even when Transmission does not answer.
	client, _ := transmission.New("http://127.0.0.1:1/transmission/rpc", "user", "secret")
	b := newTestBot(t, ft.newBot(t), client)
	got := b.renderStatus(context.Background())
	if !strings.Contains(got, "🔴 Transmission is not answering: ") || !strings.Contains(got, "Updated ") || strings.Contains(got, "secret") {
		t.Fatalf("status = %q", got)
	}
	checkTelegramHTML(t, got)
}

// /status pins a message that describes Transmission, without notifying
// anyone, and remembers it; a new /status replaces the chat's last one.
func TestStatusPinsAndReplaces(t *testing.T) {
	b, ft, _ := statusBot(t)
	ctx := context.Background()
	b.status(ctx, req())
	if texts := ft.sentTexts(); len(texts) != 1 || texts[0] != b.renderStatus(ctx) {
		t.Fatalf("sent %q", texts)
	}
	if pins := ft.sentPins(); !slices.Equal(pins, []string{"1 true"}) {
		t.Fatalf("pins = %q", pins)
	}
	if got := savedStatus(b); got != 1 {
		t.Fatalf("saved status = %d", got)
	}

	b.status(ctx, req())
	if edits := ft.editsOf("1"); len(edits) != 1 || edits[0] != "📊 This status moved to a newer message." {
		t.Fatalf("the old status became %q", edits)
	}
	if got := ft.callCount("unpinChatMessage"); got != 1 {
		t.Fatalf("unpins = %d", got)
	}
	if pins := ft.sentPins(); !slices.Equal(pins, []string{"1 true", "2 true"}) {
		t.Fatalf("pins = %q", pins)
	}
	if got := savedStatus(b); got != 2 {
		t.Fatalf("saved status = %d", got)
	}
}

// A status that cannot be pinned still stays up to date, and the chat is
// told why it is not pinned.
func TestStatusThatCannotBePinned(t *testing.T) {
	b, ft, _ := statusBot(t)
	ft.script("pinChatMessage", `{"ok":false,"error_code":400,"description":"Bad Request: not enough rights to manage pinned messages in the chat"}`)
	b.status(context.Background(), req())
	texts := ft.sentTexts()
	if len(texts) != 2 || !strings.Contains(texts[1], "could not be pinned: bad request, Bad Request: not enough rights") {
		t.Fatalf("sent %q", texts)
	}
	if got := savedStatus(b); got != 1 {
		t.Fatalf("saved status = %d", got)
	}
}

// Status messages are brought up to date; one that was deleted, or whose
// chat the bot left, is no longer updated, but one Telegram failed to edit
// for another reason is.
func TestStatusUpdatesUntilItIsGone(t *testing.T) {
	b, ft, fts := statusBot(t)
	ctx := context.Background()
	b.checkStatus(ctx)
	if got := fts.methodCount("session-stats"); got != 0 {
		t.Fatalf("with no status message, Transmission was asked %d times", got)
	}

	b.status(ctx, req())
	fts.set(func() { fts.stats = `{"downloadSpeed":1000}` })
	b.checkStatus(ctx)
	if edits := ft.editsOf("1"); len(edits) != 1 || !strings.HasPrefix(edits[0], "📊 <b>Transmission</b>\n↓ 1.0 kB/s ·") {
		t.Fatalf("edits = %q", edits)
	}

	ft.script("editMessageText", `{"ok":false,"error_code":400,"description":"Bad Request: something went wrong"}`)
	b.checkStatus(ctx)
	if got := savedStatus(b); got != 1 {
		t.Fatalf("after a passing failure, the saved status = %d", got)
	}

	for _, gone := range []string{
		`{"ok":false,"error_code":400,"description":"Bad Request: message to edit not found"}`,
		`{"ok":false,"error_code":403,"description":"Forbidden: bot was kicked from the group chat"}`,
		`{"ok":false,"error_code":400,"description":"Bad Request: group chat was upgraded to a supergroup chat","parameters":{"migrate_to_chat_id":-1001}}`,
	} {
		b.status(ctx, req())
		ft.script("editMessageText", gone)
		b.checkStatus(ctx)
		if got := savedStatus(b); got != 0 {
			t.Fatalf("after %s, the saved status = %d", gone, got)
		}
	}

	// A status that ended meanwhile is left alone.
	edits := ft.callCount("editMessageText")
	if err := b.updateStatus(ctx, 42, "x"); err != nil || ft.callCount("editMessageText") != edits {
		t.Fatalf("updating an ended status: %v", err)
	}
}

// After a restart, the status messages saved before it are brought up to
// date at once.
func TestStartUpdatesSavedStatus(t *testing.T) {
	b, ft, _ := statusBot(t)
	if err := b.State.Update(func(data *state.Data) { data.Status = map[int64]int{42: 7} }); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() {
		b.Start(ctx)
		close(stopped)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for len(ft.editsOf("7")) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the saved status message was not updated")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-stopped
}

// /status off ends the chat's status message; Refresh brings it up to date
// at once, and says it is no longer updated under any other message.
func TestStatusOffAndRefresh(t *testing.T) {
	b, ft, fts := statusBot(t)
	ctx := context.Background()
	b.status(ctx, req("<x>"))
	b.status(ctx, req())

	fts.set(func() { fts.stats = `{"downloadSpeed":1000}` })
	b.handleCallbackQuery(ctx, pressOn(2, "sr"))
	b.handleCallbackQuery(ctx, pressOn(5, "sr"))
	if answers := ft.callbackAnswers(); !slices.Equal(answers, []string{"Updated", statusEndedNote}) {
		t.Fatalf("answers = %q", answers)
	}
	if edits := ft.editsOf("2"); len(edits) != 1 || !strings.Contains(edits[0], "↓ 1.0 kB/s") {
		t.Fatalf("the refreshed status = %q", edits)
	}

	b.status(ctx, req("OFF"))
	b.status(ctx, req("off"))
	b.handleCallbackQuery(ctx, pressOn(2, "sr"))
	texts := ft.sentTexts()
	want := []string{"<b>status:</b> use /status, or /status off", texts[1],
		"The status message is no longer updated or pinned.", "<b>status:</b> this chat has no status message"}
	if !slices.Equal(texts, want) {
		t.Fatalf("sent %q", texts)
	}
	if edits := ft.editsOf("2"); edits[len(edits)-1] != statusEndedNote {
		t.Fatalf("the ended status = %q", edits)
	}
	if got := ft.callCount("unpinChatMessage"); got != 1 {
		t.Fatalf("unpins = %d", got)
	}
	if answers := ft.callbackAnswers(); answers[len(answers)-1] != statusEndedNote {
		t.Fatalf("answers = %q", answers)
	}
}
