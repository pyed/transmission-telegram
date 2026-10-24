package bot

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/pyed/transmission"
)

// filesBot is a bot with torrent 7, of twelve files: the first skipped, the
// second at high priority, the rest normal.
func filesBot(t *testing.T) (*Bot, *fakeTelegram, *fakeTransmission) {
	t.Helper()
	b, ft, fts := buttonBot(t)
	torrent := fts.torrents[0]
	for i := range 12 {
		torrent.Files = append(torrent.Files, transmission.File{Name: fmt.Sprintf("Show/<E%02d>.mkv", i+1), Length: 1000, BytesCompleted: 250})
		torrent.FileStats = append(torrent.FileStats, transmission.FileStat{Wanted: i != 0, Priority: map[bool]int{true: transmission.PriorityHigh}[i == 1]})
	}
	return b, ft, fts
}

const fp = "0123456789abcdef"

// /files lists a page of files, each with its state, and buttons to open a
// file, turn the page, set every file, and go back to the card.
func TestFilesListsAPage(t *testing.T) {
	b, ft, _ := filesBot(t)
	b.handleUpdate(context.Background(), nil, message("/files 7"))
	texts := ft.sentTexts()
	if len(texts) != 1 {
		t.Fatalf("replies = %q", texts)
	}
	for _, want := range []string{
		"<b>1</b> ⬜ Show/&lt;E01&gt;.mkv · 1.0 kB (25.0%)", "<b>2</b> ⭐ Show/&lt;E02&gt;.mkv", "<b>3</b> ✅ Show/&lt;E03&gt;.mkv",
		"<i>Page 1 of 2 · 12 files</i>",
	} {
		if !strings.Contains(texts[0], want) {
			t.Fatalf("the files page does not show %q:\n%s", want, texts[0])
		}
	}
	if strings.Contains(texts[0], "E11") {
		t.Fatal("the first page shows the eleventh file")
	}
	torrent := (&transmission.Torrent{ID: 7, HashString: testHash, Name: "x"})
	torrent.Files = make([]transmission.File, 12)
	torrent.FileStats = make([]transmission.FileStat, 12)
	_, keyboard := filesPage(torrent, 0)
	rows := buttonData(keyboard)
	want := [][]string{
		{"fo:7:" + fp + ":0", "fo:7:" + fp + ":1", "fo:7:" + fp + ":2", "fo:7:" + fp + ":3", "fo:7:" + fp + ":4"},
		{"fo:7:" + fp + ":5", "fo:7:" + fp + ":6", "fo:7:" + fp + ":7", "fo:7:" + fp + ":8", "fo:7:" + fp + ":9"},
		{"fl:7:" + fp + ":1"},
		{"fa:7:" + fp + ":skip", "fa:7:" + fp + ":normal"},
		{"c:7:" + fp},
	}
	if !slices.EqualFunc(rows, want, slices.Equal[[]string]) {
		t.Fatalf("buttons = %v\nwant %v", rows, want)
	}
}

// A file's buttons skip it or set its priority, in Transmission's terms.
func TestFilesSetOneFile(t *testing.T) {
	b, ft, fts := filesBot(t)
	ctx := context.Background()
	b.handleUpdate(ctx, nil, message("/files 7"))

	b.handleCallbackQuery(ctx, pressOn(1, "fo:7:"+fp+":2"))
	b.handleCallbackQuery(ctx, pressOn(1, "fs:7:"+fp+":2:high"))
	b.handleCallbackQuery(ctx, pressOn(1, "fs:7:"+fp+":0:skip"))
	b.handleCallbackQuery(ctx, pressOn(1, "fs:7:"+fp+":5:low"))
	b.handleCallbackQuery(ctx, pressOn(1, "fa:7:"+fp+":normal"))

	want := []string{
		`{"method":"torrent-set","arguments":{"files-wanted":[2],"priority-high":[2],"ids":[7]}}`,
		`{"method":"torrent-set","arguments":{"files-unwanted":[0],"ids":[7]}}`,
		`{"method":"torrent-set","arguments":{"files-wanted":[5],"priority-low":[5],"ids":[7]}}`,
		`{"method":"torrent-set","arguments":{"files-wanted":[0,1,2,3,4,5,6,7,8,9,10,11],"priority-normal":[0,1,2,3,4,5,6,7,8,9,10,11],"ids":[7]}}`,
	}
	if got := fts.requestBodies("torrent-set"); !slices.Equal(got, want) {
		t.Fatalf("torrent-set requests:\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	edits := ft.editsOf("1")
	if len(edits) < 1 || !strings.Contains(edits[0], "File 3 of 12: <code>Show/&lt;E03&gt;.mkv</code>") {
		t.Fatalf("the file's card = %q", edits)
	}
	if answers := ft.callbackAnswers(); answers[1] != "Downloading at high priority" || answers[2] != "Skipped" {
		t.Fatalf("answers = %q", answers)
	}
}

// Pages turn, the card is a button away, and buttons for a file that is not
// there, or a torrent that changed, do nothing.
func TestFilesPagesAndChecks(t *testing.T) {
	b, ft, fts := filesBot(t)
	ctx := context.Background()
	b.handleUpdate(ctx, nil, message("/files 7"))

	b.handleCallbackQuery(ctx, pressOn(1, "fl:7:"+fp+":1"))
	b.handleCallbackQuery(ctx, pressOn(1, "fo:7:"+fp+":99"))
	b.handleCallbackQuery(ctx, pressOn(1, "fs:7:fedcba9876543210:0:skip"))
	b.handleCallbackQuery(ctx, pressOn(1, "c:7:"+fp))

	edits := ft.editsOf("1")
	if len(edits) != 2 || !strings.Contains(edits[0], "<b>12</b> ✅ Show/&lt;E12&gt;.mkv") || strings.Contains(edits[0], "E10") ||
		!strings.Contains(edits[1], "<b>Test &lt;Torrent&gt;</b>\n[") {
		t.Fatalf("edits = %q", edits)
	}
	answers := ft.callbackAnswers()
	if answers[1] != "That file is gone; send /files again" || answers[2] != "Torrent changed; send /files again" {
		t.Fatalf("answers = %q", answers)
	}
	if got := fts.methodCount("torrent-set"); got != 0 {
		t.Fatalf("a refused button changed files (%d calls)", got)
	}
}

// A magnet link's torrent has no files until its metadata arrives.
func TestFilesBeforeMetadata(t *testing.T) {
	text, keyboard := filesPage(&transmission.Torrent{ID: 3, Name: "magnet", HashString: testHash}, 0)
	if !strings.Contains(text, "still fetching") || len(keyboard.InlineKeyboard) != 1 {
		t.Fatalf("files before metadata = %q, %v", text, buttonData(keyboard))
	}
}
