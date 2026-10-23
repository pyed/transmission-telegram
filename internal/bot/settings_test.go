package bot

import (
	"context"
	"reflect"
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

// /downlimit 0 and off turn the limit off, rather than set a limit of 0 KB/s
// that stalls every download, and leave its value for later; a number sets
// and enables it; without an argument the limit is shown.
func TestSpeedLimits(t *testing.T) {
	ft := newFakeTelegram(t)
	fts := newFakeTransmission(t)
	fts.session = `{"version":"test","speed-limit-down":300,"speed-limit-down-enabled":true,` +
		`"speed-limit-up":50,"speed-limit-up-enabled":false}`
	client, err := transmission.New(fts.URL+"/transmission/rpc", "", "")
	if err != nil {
		t.Fatalf("failed to create transmission client: %v", err)
	}
	b := newTestBot(t, ft.newBot(t), client)
	ctx := context.Background()

	b.downlimit(ctx, req("0"))
	b.downlimit(ctx, req("OFF"))
	b.uplimit(ctx, req("250"))
	b.uplimit(ctx, req("fast"))
	want := []string{
		`{"method":"session-set","arguments":{"speed-limit-down-enabled":false}}`,
		`{"method":"session-set","arguments":{"speed-limit-down-enabled":false}}`,
		`{"method":"session-set","arguments":{"speed-limit-up":250,"speed-limit-up-enabled":true}}`,
	}
	if got := fts.requestBodies("session-set"); !reflect.DeepEqual(got, want) {
		t.Fatalf("session-set requests:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	b.downlimit(ctx, req())
	b.uplimit(ctx, req())
	texts := ft.sentTexts()
	wantTexts := []string{
		"<b>Download limit:</b> off", "<b>Download limit:</b> off", "<b>Upload limit:</b> 250 KB/s",
		"<b>uplimit:</b> give the limit in KB/s, or off",
		"<b>Download limit:</b> 300 KB/s", "<b>Upload limit:</b> off",
	}
	if !reflect.DeepEqual(texts, wantTexts) {
		t.Fatalf("replies = %q\nwant %q", texts, wantTexts)
	}
}

// head, tail and latest take a count of 1 or more; a count of 0 or less
// used to list every torrent, live.
func TestListCountsMustBePositive(t *testing.T) {
	ft := newFakeTelegram(t)
	fts := newFakeTransmission(t)
	client, err := transmission.New(fts.URL+"/transmission/rpc", "", "")
	if err != nil {
		t.Fatalf("failed to create transmission client: %v", err)
	}
	b := newTestBot(t, ft.newBot(t), client)
	before := fts.methodCount("torrent-get")

	b.head(context.Background(), req("0"))
	b.tail(context.Background(), req("-5"))
	b.latest(context.Background(), req("five"))

	if got := fts.methodCount("torrent-get"); got != before {
		t.Fatalf("a refused count still listed torrents (%d requests)", got-before)
	}
	for i, name := range []string{"head", "tail", "latest"} {
		if texts := ft.sentTexts(); len(texts) != 3 || !strings.HasPrefix(texts[i], "<b>"+name+":</b> give how many") {
			t.Fatalf("replies = %q", texts)
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
