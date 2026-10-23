package bot

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/go-telegram/bot/models"
	"github.com/pyed/transmission"
	"github.com/pyed/transmission-telegram/v2/internal/config"
)

// pressOn is master 12345's press of data under message messageID of chat 42.
func pressOn(messageID int, data string) *models.CallbackQuery {
	cb := pressFrom(12345, data)
	cb.Message.Message.ID = messageID
	return cb
}

// listBot is a bot with n torrents, IDs 1 to n.
func listBot(t *testing.T, n int) (*Bot, *fakeTelegram) {
	t.Helper()
	ft := newFakeTelegram(t)
	fts := newFakeTransmission(t)
	for id := 1; id <= n; id++ {
		fts.torrents = append(fts.torrents, &transmission.Torrent{
			ID: id, Name: fmt.Sprintf("torrent %02d", id), HashString: fmt.Sprintf("%040x", id), Status: transmission.StatusSeeding, PercentDone: 1,
		})
	}
	client, err := transmission.New(fts.URL+"/transmission/rpc", "", "")
	if err != nil {
		t.Fatalf("failed to create transmission client: %v", err)
	}
	b := newTestBot(t, ft.newBot(t), client)
	b.Config.Masters = config.MasterSlice{"12345"}
	return b, ft
}

// buttonData lists a keyboard's callback data, row by row.
func buttonData(markup *models.InlineKeyboardMarkup) [][]string {
	var rows [][]string
	for _, row := range markup.InlineKeyboard {
		var data []string
		for _, button := range row {
			data = append(data, button.CallbackData)
		}
		rows = append(rows, data)
	}
	return rows
}

// A long list comes ten torrents a page, with a button for each and buttons
// to turn the page; the pages show the list as it is when turned.
func TestListsComeInPages(t *testing.T) {
	b, ft := listBot(t, 23)
	ctx := context.Background()

	b.list(ctx, req())
	texts := ft.sentTexts()
	if len(texts) != 1 || strings.Count(texts[0], "<code>") != 10 || !strings.HasSuffix(texts[0], "<i>Page 1 of 3 · 23 torrents</i>") {
		t.Fatalf("first page = %q", texts)
	}
	text, keyboard := listPage(listing{torrents: mustListing(t, b, "list").torrents}, 0)
	if text != texts[0] {
		t.Fatal("listPage does not make what was sent")
	}
	rows := buttonData(keyboard)
	if len(rows) != 3 || len(rows[0]) != 5 || rows[0][0] != "t:1:"+torrentFingerprint(fmt.Sprintf("%040x", 1)) || rows[2][0] != "pg:1" || len(rows[2]) != 1 {
		t.Fatalf("first page's buttons = %v", rows)
	}

	b.handleCallbackQuery(ctx, pressOn(1, "pg:2"))
	edits := ft.editsOf("1")
	if len(edits) != 1 || !strings.Contains(edits[0], "<code>21</code>") || strings.Contains(edits[0], "<code>20</code>") ||
		!strings.Contains(edits[0], "Page 3 of 3") {
		t.Fatalf("third page = %q", edits)
	}
}

// A torrent's button turns the list into its card, and Back turns it into
// the list again, at the page it was on.
func TestCardsOpenFromListsAndGoBack(t *testing.T) {
	b, ft := listBot(t, 23)
	ctx := context.Background()
	b.list(ctx, req())
	b.handleCallbackQuery(ctx, pressOn(1, "pg:1"))

	b.handleCallbackQuery(ctx, pressOn(1, "t:12:"+torrentFingerprint(fmt.Sprintf("%040x", 12))))
	edits := ft.editsOf("1")
	if len(edits) != 2 || !strings.Contains(edits[1], "<b>torrent 12</b>") {
		t.Fatalf("the list did not become the card: %q", edits)
	}
	if texts := ft.sentTexts(); len(texts) != 1 {
		t.Fatalf("the card came as a new message: %q", texts)
	}

	b.handleCallbackQuery(ctx, pressOn(1, "back"))
	edits = ft.editsOf("1")
	if len(edits) != 3 || !strings.Contains(edits[2], "Page 2 of 3") {
		t.Fatalf("Back did not return to page 2: %q", edits[len(edits)-1])
	}
}

// A list that fits one page has its torrents' buttons but no pages.
func TestShortListsHaveNoPages(t *testing.T) {
	_, keyboard := listPage(listing{torrents: transmission.Torrents{{ID: 1}, {ID: 2}, {ID: 3}}}, 0)
	if rows := buttonData(keyboard); len(rows) != 1 || len(rows[0]) != 3 {
		t.Fatalf("buttons = %v", rows)
	}
}

// The bot remembers the last 500 lists; older ones answer that they are too
// old, and a torrent's button under one still opens its card, as a new
// message.
func TestForgottenListsExpire(t *testing.T) {
	b, ft := listBot(t, 3)
	ctx := context.Background()
	b.handleCallbackQuery(ctx, pressOn(55, "pg:1"))
	b.handleCallbackQuery(ctx, pressOn(55, "back"))
	b.handleCallbackQuery(ctx, pressOn(55, "t:2:"+torrentFingerprint(fmt.Sprintf("%040x", 2))))
	answers := ft.callbackAnswers()
	if len(answers) != 3 || answers[0] != "This list is too old; send the command again" || answers[1] != answers[0] {
		t.Fatalf("answers = %q", answers)
	}
	if texts := ft.sentTexts(); len(texts) != 1 || !strings.Contains(texts[0], "<b>torrent 02</b>") {
		t.Fatalf("the card did not come as a new message: %q", texts)
	}

	var store screenStore
	for i := range maxScreens + 1 {
		store.put(screenKey{42, i}, &screen{command: "list"})
	}
	if _, ok := store.get(screenKey{42, 0}); ok {
		t.Fatal("the oldest list is still remembered")
	}
	if _, ok := store.get(screenKey{42, maxScreens}); !ok || len(store.order) != maxScreens {
		t.Fatalf("remembering %d lists", len(store.order))
	}
}

// Each list command makes its own listing, and all of them make pages.
func TestEveryListCommandHasPages(t *testing.T) {
	b, ft := listBot(t, 12)
	for _, command := range []string{"list", "latest 12", "search torrent", "seeding"} {
		before := len(ft.sentTexts())
		b.handleUpdate(context.Background(), nil, message("/"+command))
		texts := ft.sentTexts()[before:]
		if len(texts) != 1 || !strings.Contains(texts[0], "Page 1 of 2 · 12 torrents") {
			t.Errorf("/%s = %q", command, texts)
		}
	}
	if first := mustListing(t, b, "latest").torrents; len(first) != 5 {
		t.Errorf("latest without a count lists %d", len(first))
	}
}

func mustListing(t *testing.T, b *Bot, command string, args ...string) listing {
	t.Helper()
	l, err := b.listing(context.Background(), 42, command, args)
	if err != nil {
		t.Fatal(err)
	}
	return l
}
