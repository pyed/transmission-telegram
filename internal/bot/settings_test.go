package bot

import (
	"context"
	"strings"
	"testing"

	"github.com/pyed/transmission"
)

// req is a command with args from chat 42.
func req(args ...string) *request {
	return &request{chatID: 42, args: args}
}

// TestSortRevWithoutKeyDoesNotPanic is a regression test: "/sort rev" used
// to panic with an index-out-of-range on args[0] after stripping "rev",
// which killed the whole process (no recover in the handler path). It must
// instead reply with the usage help.
func TestSortRevWithoutKeyDoesNotPanic(t *testing.T) {
	ft := newFakeTelegram(t)
	b := newTestBot(t, ft.newBot(t), nil)

	b.sort(context.Background(), req("rev"))

	texts := ft.sentTexts()
	if len(texts) == 0 {
		t.Fatal("expected a usage reply, got none")
	}
	if last := texts[len(texts)-1]; last != sortUsage {
		t.Errorf("expected usage message %q, got %q", sortUsage, last)
	}
}

// TestSortRevSizeStillWorks guards against over-correction: a valid
// "/sort rev size" must still apply the reversed sort.
func TestSortRevSizeStillWorks(t *testing.T) {
	ft := newFakeTelegram(t)
	fts := newFakeTransmission(t)
	client, err := transmission.New(fts.URL+"/transmission/rpc", "", "")
	if err != nil {
		t.Fatalf("failed to create transmission client: %v", err)
	}
	b := newTestBot(t, ft.newBot(t), client)

	b.sort(context.Background(), req("rev", "size"))

	texts := ft.sentTexts()
	if len(texts) == 0 {
		t.Fatal("expected a confirmation reply, got none")
	}
	if last := texts[len(texts)-1]; last != "<b>sort:</b> reversed size" {
		t.Errorf("unexpected reply %q", last)
	}
}

func TestTrackersCountsDistinctTorrentsInStableOrder(t *testing.T) {
	ft := newFakeTelegram(t)
	fts := newFakeTransmission(t)
	fts.torrents = transmission.Torrents{
		{
			ID: 1,
			Trackers: []transmission.Tracker{
				{Announce: "https://tracker.example.com/announce"},
				{Announce: "udp://tracker.example.com:6969/announce"},
				{Announce: "https://z.example.net/announce"},
			},
		},
		{
			ID: 2,
			Trackers: []transmission.Tracker{
				{Announce: "http://tracker.example.com:80/announce"},
			},
		},
	}
	client, err := transmission.New(fts.URL+"/transmission/rpc", "", "")
	if err != nil {
		t.Fatalf("failed to create transmission client: %v", err)
	}
	b := newTestBot(t, ft.newBot(t), client)

	b.trackers(context.Background(), req())

	texts := ft.sentTexts()
	if len(texts) == 0 {
		t.Fatal("expected tracker response")
	}
	const want = "2 - tracker.example.com\n1 - z.example.net"
	if got := texts[len(texts)-1]; got != want {
		t.Fatalf("tracker response = %q, want %q", got, want)
	}
}

func TestPathCommandsPreserveSpaces(t *testing.T) {
	ft := newFakeTelegram(t)
	fts := newFakeTransmission(t)
	client, err := transmission.New(fts.URL+"/transmission/rpc", "", "")
	if err != nil {
		t.Fatalf("failed to create transmission client: %v", err)
	}
	b := newTestBot(t, ft.newBot(t), client)

	b.downloaddir(context.Background(), req("/media/My", "Downloads"))
	b.free(context.Background(), req("/media/My", "Downloads"))

	for _, method := range []string{"session-set", "free-space"} {
		bodies := fts.requestBodies(method)
		if len(bodies) != 1 || !strings.Contains(bodies[0], "/media/My Downloads") {
			t.Fatalf("%s did not preserve spaced path: %v", method, bodies)
		}
	}
}

func TestTurtleRejectsInvalidArgument(t *testing.T) {
	ft := newFakeTelegram(t)
	fts := newFakeTransmission(t)
	client, err := transmission.New(fts.URL+"/transmission/rpc", "", "")
	if err != nil {
		t.Fatalf("failed to create transmission client: %v", err)
	}
	b := newTestBot(t, ft.newBot(t), client)
	baselineSessionGets := fts.methodCount("session-get")

	b.turtle(context.Background(), req("maybe"))

	if got := fts.methodCount("session-get"); got != baselineSessionGets {
		t.Fatalf("invalid turtle argument performed an RPC: before=%d after=%d", baselineSessionGets, got)
	}
	texts := ft.sentTexts()
	if len(texts) == 0 || texts[len(texts)-1] != "<b>turtle:</b> expected on or off" {
		t.Fatalf("unexpected turtle response: %v", texts)
	}
}
