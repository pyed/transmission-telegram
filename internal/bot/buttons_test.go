package bot

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/go-telegram/bot/models"
	"github.com/pyed/transmission"
	"github.com/pyed/transmission-telegram/v2/internal/config"
)

const testHash = "0123456789abcdef0123456789abcdef01234567"

// buttonBot is a bot whose master is 12345, with torrent 7 in Transmission.
func buttonBot(t *testing.T) (*Bot, *fakeTelegram, *fakeTransmission) {
	t.Helper()
	ft := newFakeTelegram(t)
	fts := newFakeTransmission(t)
	fts.torrents = transmission.Torrents{{ID: 7, Name: "Test <Torrent>", HashString: testHash, Status: transmission.StatusDownloading}}
	client, err := transmission.New(fts.URL+"/transmission/rpc", "", "")
	if err != nil {
		t.Fatalf("failed to create transmission client: %v", err)
	}
	b := newTestBot(t, ft.newBot(t), client)
	b.Config.Masters = config.MasterSlice{"12345"}
	return b, ft, fts
}

// pressFrom is a press by user from of the button data, under message 99 of
// chat 42.
func pressFrom(from int64, data string) *models.CallbackQuery {
	return &models.CallbackQuery{
		ID:   "callback-id",
		From: models.User{ID: from},
		Data: data,
		Message: models.MaybeInaccessibleMessage{
			Type:    models.MaybeInaccessibleMessageTypeMessage,
			Message: &models.Message{ID: 99, Date: 1, Chat: models.Chat{ID: 42, Type: models.ChatTypePrivate}},
		},
	}
}

func TestCardKeyboard(t *testing.T) {
	torrent := &transmission.Torrent{ID: 42, HashString: testHash}
	kb := cardKeyboard(torrent, false)
	var data []string
	for _, row := range kb.InlineKeyboard {
		for _, button := range row {
			data = append(data, button.CallbackData)
		}
	}
	want := "a:stop:42:0123456789abcdef a:start:42:0123456789abcdef a:refresh:42:0123456789abcdef " +
		"a:verify:42:0123456789abcdef a:reannounce:42:0123456789abcdef fl:42:0123456789abcdef:0 " +
		"a:remove:42:0123456789abcdef a:removedata:42:0123456789abcdef"
	if got := strings.Join(data, " "); got != want {
		t.Fatalf("buttons = %s\nwant %s", got, want)
	}
	for _, button := range data {
		if len(button) > 64 {
			t.Errorf("%q is longer than Telegram's 64 bytes", button)
		}
	}
	back := cardKeyboard(torrent, true).InlineKeyboard
	if last := back[len(back)-1]; len(last) != 1 || last[0].CallbackData != "back" {
		t.Fatalf("a card opened from a list has no Back: %v", last)
	}
}

// Removal asks first, under the card; only a confirmation for the same
// torrent removes it, and with its data only when that was asked.
func TestRemovalNeedsMatchingConfirmation(t *testing.T) {
	b, ft, fts := buttonBot(t)
	ctx := context.Background()

	b.handleCallbackQuery(ctx, pressFrom(12345, "a:removedata:7:0123456789abcdef"))
	if got := fts.methodCount("torrent-remove"); got != 0 {
		t.Fatalf("the button removed the torrent before confirmation (%d calls)", got)
	}
	if edits := ft.editsOf("99"); len(edits) != 1 || !strings.Contains(edits[0], "delete its data?") {
		t.Fatalf("expected the question under the card, got %q", edits)
	}

	b.handleCallbackQuery(ctx, pressFrom(12345, "y:removedata:7:fedcba9876543210"))
	if got := fts.methodCount("torrent-remove"); got != 0 {
		t.Fatalf("a confirmation for another torrent removed this one (%d calls)", got)
	}
	b.handleCallbackQuery(ctx, pressFrom(12345, "y:removedata:7:0123456789abcdef"))
	bodies := fts.requestBodies("torrent-remove")
	if len(bodies) != 1 || !strings.Contains(bodies[0], `"delete-local-data":true`) {
		t.Fatalf("torrent-remove requests = %q", bodies)
	}
	answers := ft.callbackAnswers()
	if len(answers) != 3 || answers[1] != "Torrent changed; run /info again" || answers[2] != "Removed" {
		t.Fatalf("answers = %q", answers)
	}
	if edits := ft.editsOf("99"); len(edits) != 2 || edits[1] != "💣 <b>Removed with its data:</b> <code>7</code> Test &lt;Torrent&gt;" {
		t.Fatalf("edits = %q", edits)
	}
}

// Cancel goes back to the card, and nothing is removed.
func TestRemovalCanBeCancelled(t *testing.T) {
	b, ft, fts := buttonBot(t)
	b.handleCallbackQuery(context.Background(), pressFrom(12345, "a:remove:7:0123456789abcdef"))
	b.handleCallbackQuery(context.Background(), pressFrom(12345, "n:7:0123456789abcdef"))
	if got := fts.methodCount("torrent-remove"); got != 0 {
		t.Fatalf("cancelling removed the torrent (%d calls)", got)
	}
	if edits := ft.editsOf("99"); len(edits) != 2 || strings.Contains(edits[1], "Remove this torrent?") {
		t.Fatalf("edits = %q", edits)
	}
}

// After pausing or resuming from the card, the card shows the torrent as it
// is now.
func TestCardActionsRefreshTheCard(t *testing.T) {
	b, ft, fts := buttonBot(t)
	b.handleCallbackQuery(context.Background(), pressFrom(12345, "a:stop:7:0123456789abcdef"))
	if got := fts.methodCount("torrent-stop"); got != 1 {
		t.Fatalf("torrent-stop requests = %d", got)
	}
	if answers := ft.callbackAnswers(); len(answers) != 1 || answers[0] != "Paused" {
		t.Fatalf("answers = %q", answers)
	}
	if edits := ft.editsOf("99"); len(edits) != 1 || !strings.Contains(edits[0], "Test &lt;Torrent&gt;") {
		t.Fatalf("the card was not refreshed: %q", edits)
	}
}

// Only masters may press buttons, even under a master's message in a group.
func TestStrangersCannotPress(t *testing.T) {
	b, ft, fts := buttonBot(t)
	b.handleCallbackQuery(context.Background(), pressFrom(999, "a:stop:7:0123456789abcdef"))
	if got := fts.methodCount("torrent-stop"); got != 0 {
		t.Fatalf("a stranger stopped a torrent (%d calls)", got)
	}
	if answers := ft.callbackAnswers(); len(answers) != 1 || answers[0] != "Unauthorized" {
		t.Fatalf("answers = %q", answers)
	}
}

// The buttons of v2.1 cards still work, by the new rules; v2.0's delete
// buttons, which carry no fingerprint, have expired, but its pause and
// resume buttons work.
func TestEarlierVersionsButtons(t *testing.T) {
	b, ft, fts := buttonBot(t)
	ctx := context.Background()

	b.handleCallbackQuery(ctx, pressFrom(12345, "cmd:del:7"))
	b.handleCallbackQuery(ctx, pressFrom(12345, "cmd:del:7:0123456789abcdef"))
	b.handleCallbackQuery(ctx, pressFrom(12345, "cmd:confirm-del:7:0123456789abcdef"))
	b.handleCallbackQuery(ctx, pressFrom(12345, "cmd:stop:7"))
	b.handleCallbackQuery(ctx, pressFrom(12345, "cmd:start:7:0123456789abcdef"))

	if bodies := fts.requestBodies("torrent-remove"); len(bodies) != 1 || !strings.Contains(bodies[0], `"delete-local-data":false`) {
		t.Fatalf("torrent-remove requests = %q", bodies)
	}
	if fts.methodCount("torrent-stop") != 1 || fts.methodCount("torrent-start") != 1 {
		t.Fatalf("stop %d, start %d", fts.methodCount("torrent-stop"), fts.methodCount("torrent-start"))
	}
	answers := ft.callbackAnswers()
	if len(answers) != 5 || answers[0] != "Delete button expired; run /info again" || answers[2] != "Removed" {
		t.Fatalf("answers = %q", answers)
	}
}

// /deldata asks first, naming the torrents and their sizes; Yes deletes
// them with their data, once, and only those still the same torrents.
func TestDeldataAsksFirst(t *testing.T) {
	b, ft, fts := buttonBot(t)
	fts.torrents = append(fts.torrents, &transmission.Torrent{ID: 8, Name: "Other", HashString: "ffff" + testHash[4:], SizeWhenDone: 2_000_000_000})
	ctx := context.Background()

	b.handleUpdate(ctx, nil, message("/deldata 7 8 x"))
	if got := fts.methodCount("torrent-remove"); got != 0 {
		t.Fatalf("/deldata deleted before asking (%d calls)", got)
	}
	question := ft.sentTexts()[0]
	for _, want := range []string{"Delete these torrents and their data?", "<code>7</code> Test &lt;Torrent&gt;", "<code>8</code> Other (2.0 GB)", "⚠️ x is not a torrent ID"} {
		if !strings.Contains(question, want) {
			t.Fatalf("the question does not say %q:\n%s", want, question)
		}
	}

	// Torrent 8 is replaced by another torrent with its ID before the answer.
	fts.set(func() { fts.torrents[1].HashString = "eeee" + testHash[4:] })
	b.handleCallbackQuery(ctx, pressOn(1, "dd:yes"))
	b.handleCallbackQuery(ctx, pressOn(1, "dd:yes"))
	bodies := fts.requestBodies("torrent-remove")
	if len(bodies) != 1 || bodies[0] != `{"method":"torrent-remove","arguments":{"ids":[7],"delete-local-data":true}}` {
		t.Fatalf("torrent-remove requests = %q", bodies)
	}
	if edits := ft.editsOf("1"); len(edits) != 1 || !strings.Contains(edits[0], "<b>Deleted with data:</b>\n<code>7</code>") ||
		!strings.Contains(edits[0], "<code>8</code> is gone or is another torrent now") {
		t.Fatalf("edits = %q", edits)
	}
	if answers := ft.callbackAnswers(); len(answers) != 2 || answers[1] != "This question is too old; send /deldata again" {
		t.Fatalf("answers = %q", answers)
	}
}

// Cancel deletes nothing; "confirm" deletes at once, for scripts.
func TestDeldataCancelAndConfirm(t *testing.T) {
	b, ft, fts := buttonBot(t)
	ctx := context.Background()
	b.handleUpdate(ctx, nil, message("/deldata 7"))
	b.handleCallbackQuery(ctx, pressOn(1, "dd:no"))
	if got := fts.methodCount("torrent-remove"); got != 0 {
		t.Fatalf("Cancel deleted (%d calls)", got)
	}
	b.handleUpdate(ctx, nil, message("/deldata 7 CONFIRM"))
	if bodies := fts.requestBodies("torrent-remove"); len(bodies) != 1 || !strings.Contains(bodies[0], `"delete-local-data":true`) {
		t.Fatalf("torrent-remove requests = %q", bodies)
	}
	if texts := ft.sentTexts(); len(texts) != 2 || !strings.HasPrefix(texts[1], "<b>Deleted with data:</b>") {
		t.Fatalf("replies = %q", texts)
	}
}

// A button pressed under a live card ends the live view, which would
// otherwise put the card back over what the press showed.
func TestPressingALiveCardEndsItsLiveView(t *testing.T) {
	b, ft, _ := buttonBot(t)
	b.Config.Duration = 1000
	b.Config.Interval = 5 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer func() {
		cancel()
		b.wg.Wait()
	}()

	b.handleUpdate(ctx, nil, message("/info 7"))
	ft.waitForSent(t, 1)
	time.Sleep(30 * time.Millisecond)
	b.handleCallbackQuery(ctx, &models.CallbackQuery{
		ID: "callback-id", From: models.User{ID: 12345}, Data: "a:remove:7:0123456789abcdef",
		Message: models.MaybeInaccessibleMessage{
			Type:    models.MaybeInaccessibleMessageTypeMessage,
			Message: &models.Message{ID: 1, Date: 1, Chat: models.Chat{ID: 42, Type: models.ChatTypePrivate}},
		},
	})
	question := len(ft.editsOf("1"))
	time.Sleep(50 * time.Millisecond)
	edits := ft.editsOf("1")
	if len(edits) != question || !strings.Contains(edits[len(edits)-1], "Remove this torrent?") {
		t.Fatalf("the live view went on after the press: %d edits, last %q", len(edits), edits[len(edits)-1])
	}
}
