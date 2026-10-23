package bot

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pyed/transmission"
)

func TestLiveViewSkipsMissingMessage(t *testing.T) {
	ft := newFakeTelegram(t)
	b := newTestBot(t, ft.newBot(t), nil)
	b.Config.Duration = 1
	b.Config.Interval = time.Millisecond
	var called atomic.Bool

	b.startLive(context.Background(), 42, 0, "test", func() string {
		called.Store(true)
		return "update"
	}, nil, nil)
	b.wg.Wait()

	if called.Load() || ft.textEdits() != 0 {
		t.Fatalf("live update ran for an unsent message: called=%v edits=%d", called.Load(), ft.textEdits())
	}
}

func TestLiveUpdateEditsAndFinalizes(t *testing.T) {
	ft := newFakeTelegram(t)
	b := newTestBot(t, ft.newBot(t), nil)
	b.Config.Duration = 2
	b.Config.Interval = time.Millisecond

	b.liveUpdate(context.Background(), nil, 42, 99, func() string { return "update" }, func() string { return "final" }, nil)

	if got := ft.textEdits(); got != 3 {
		t.Fatalf("expected two updates and one final edit, got %d", got)
	}
}

// A chat keeps one live view: a new one ends the last, which shows its
// final text and is edited no more.
func TestANewLiveViewEndsTheChatsLastOne(t *testing.T) {
	ft := newFakeTelegram(t)
	b := newTestBot(t, ft.newBot(t), nil)
	b.Config.Duration = 1000
	b.Config.Interval = 5 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer func() {
		cancel()
		b.wg.Wait()
	}()

	b.startLive(ctx, 42, 1, "first", func() string { return "first live" }, func() string { return "first final" }, nil)
	time.Sleep(50 * time.Millisecond)
	b.startLive(ctx, 42, 2, "second", func() string { return "second live" }, func() string { return "second final" }, nil)
	time.Sleep(50 * time.Millisecond)
	firstEdits := ft.editsOf("1")
	time.Sleep(50 * time.Millisecond)

	if len(firstEdits) == 0 || firstEdits[len(firstEdits)-1] != "first final" {
		t.Fatalf("the first view did not end with its final text: %q", firstEdits)
	}
	if later := ft.editsOf("1"); len(later) != len(firstEdits) {
		t.Fatalf("the first view was edited after it ended: %q", later[len(firstEdits):])
	}
	if second := ft.editsOf("2"); len(second) < 2 || second[len(second)-1] != "second live" {
		t.Fatalf("the second view is not live: %q", second)
	}
}

// /head of more torrents than one message holds is still one message, which
// its live view can edit, and says how many it leaves out.
func TestHeadOfManyTorrentsIsOneMessage(t *testing.T) {
	ft := newFakeTelegram(t)
	fts := newFakeTransmission(t)
	for i := range 100 {
		fts.torrents = append(fts.torrents, &transmission.Torrent{ID: i + 1, Name: strings.Repeat("n", 100), Status: transmission.StatusSeeding})
	}
	client, err := transmission.New(fts.URL+"/transmission/rpc", "", "")
	if err != nil {
		t.Fatalf("failed to create transmission client: %v", err)
	}
	b := newTestBot(t, ft.newBot(t), client)

	b.head(context.Background(), req("100"))
	texts := ft.sentTexts()
	if len(texts) != 1 || !strings.Contains(texts[0], "…and ") || renderedLength(texts[0]) > maxMessageLength {
		t.Fatalf("expected one message that leaves some out, got %d messages", len(texts))
	}
}

// Live views must stay one message, which they can edit: blocks that do not
// fit are left out, and the text says how many.
func TestFitBlocksKeepsToOneMessage(t *testing.T) {
	for _, block := range []string{
		"<code>1</code> <b>" + strings.Repeat("n", 140) + "</b>\nline two",
		// 126 characters shown: 32 of them, with separators, leave 2 of
		// the 4,096, too few for the note.
		"<code>1</code> " + strings.Repeat("n", 124),
	} {
		var blocks []string
		for range 100 {
			blocks = append(blocks, block)
		}
		text := fitBlocks(blocks, "\n\n")
		if n := renderedLength(text); n > maxMessageLength {
			t.Fatalf("fitted text is %d long", n)
		}
		shown := strings.Count(text, "<code>1</code>")
		if !strings.HasSuffix(text, "\n\n"+moreNote(100-shown)) || shown == 0 {
			t.Fatalf("expected %d blocks and a note of the other %d, got the end %q", shown, 100-shown, text[len(text)-40:])
		}
		// The next block would not have fitted with the note.
		if renderedLength(strings.Join(blocks[:shown+1], "\n\n")+"\n\n"+moreNote(100-shown-1)) <= maxMessageLength {
			t.Fatalf("left out a block that fitted: %d shown", shown)
		}

		if few := fitBlocks(blocks[:3], "\n\n"); few != strings.Join(blocks[:3], "\n\n") {
			t.Fatalf("blocks that fit were changed: %q", few)
		}
	}
}
