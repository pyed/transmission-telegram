package bot

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/pyed/transmission"
)

func queueBot(t *testing.T) (*Bot, *fakeTelegram, *fakeTransmission) {
	t.Helper()
	ft := newFakeTelegram(t)
	fts := newFakeTransmission(t)
	fts.session = `{"version":"test","peer-port":51413}`
	fts.torrents = transmission.Torrents{
		{ID: 1, Name: "Running", Status: transmission.StatusDownloading, QueuePosition: 2},
		{ID: 2, Name: "First", Status: transmission.StatusDownloadPending, QueuePosition: 0},
		{ID: 3, Name: "Seeding", Status: transmission.StatusSeeding, PercentDone: 1, QueuePosition: 1},
		{ID: 4, Name: "Second", Status: transmission.StatusDownloadPending, QueuePosition: 1},
	}
	client, err := transmission.New(fts.URL+"/transmission/rpc", "", "")
	if err != nil {
		t.Fatalf("failed to create transmission client: %v", err)
	}
	return newTestBot(t, ft.newBot(t), client), ft, fts
}

// /queue lists downloads in the order Transmission runs them, and moves
// torrents in it.
func TestQueue(t *testing.T) {
	b, ft, fts := queueBot(t)
	ctx := context.Background()
	b.queue(ctx, req())
	b.queue(ctx, req("TOP", "1", "9"))
	b.queue(ctx, req("sideways", "1"))

	texts := ft.sentTexts()
	order := func(text string) string {
		var ids []string
		for _, line := range strings.Split(text, "\n") {
			ids = append(ids, strings.TrimPrefix(strings.SplitN(line, "</code>", 2)[0], "<code>"))
		}
		return strings.Join(ids, " ")
	}
	if len(texts) != 4 || order(texts[0]) != "2 4 1" || texts[1] != "⚠️ No torrent has the ID 9" || texts[3] != queueUsage {
		t.Fatalf("replies = %q", texts)
	}
	if got := fts.requestBodies("queue-move-top"); !slices.Equal(got, []string{`{"method":"queue-move-top","arguments":{"ids":[1]}}`}) {
		t.Fatalf("queue-move-top requests = %q", got)
	}
}

// /port says whether peers can reach Transmission's port.
func TestPort(t *testing.T) {
	b, ft, fts := queueBot(t)
	fts.answers = map[string]string{"port-test": `{"port-is-open":true}`}
	b.port(context.Background(), req())
	fts.set(func() { fts.answers["port-test"] = `{"port-is-open":false}` })
	b.port(context.Background(), req())
	texts := ft.sentTexts()
	if len(texts) != 2 || !strings.HasPrefix(texts[0], "✅ <b>Port 51413 is open:</b>") || !strings.HasPrefix(texts[1], "⚠️ <b>Port 51413 is closed:</b>") {
		t.Fatalf("replies = %q", texts)
	}
}
