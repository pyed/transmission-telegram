package bot

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/pyed/transmission"
)

// labelBot is a bot whose Transmission has labels: torrents 1 and 2 are
// movies, 2 also hd, and 3 has none.
func labelBot(t *testing.T, rpcVersion string) (*Bot, *fakeTelegram, *fakeTransmission) {
	t.Helper()
	ft := newFakeTelegram(t)
	fts := newFakeTransmission(t)
	fts.session = `{"version":"test","rpc-version":` + rpcVersion + `}`
	fts.torrents = transmission.Torrents{
		{ID: 1, Name: "One", Labels: []string{"movies"}},
		{ID: 2, Name: "Two", Labels: []string{"Movies", "hd"}},
		{ID: 3, Name: "Three"},
	}
	client, err := transmission.New(fts.URL+"/transmission/rpc", "", "")
	if err != nil {
		t.Fatalf("failed to create transmission client: %v", err)
	}
	return newTestBot(t, ft.newBot(t), client), ft, fts
}

// /labels counts torrents per label, most used first, and lists a label's
// torrents, ignoring case, or those without one.
func TestLabels(t *testing.T) {
	b, ft, _ := labelBot(t, "17")
	ctx := context.Background()
	b.labels(ctx, req())
	b.labels(ctx, req("MOVIES"))
	b.labels(ctx, req("-"))
	b.labels(ctx, req("<none>"))

	texts := ft.sentTexts()
	if len(texts) != 4 || !strings.HasPrefix(texts[0], "<b>1</b> hd\n<b>1</b> Movies\n<b>1</b> movies\n<b>1</b> <i>without a label</i>") {
		t.Fatalf("/labels = %q", texts[0])
	}
	if !strings.Contains(texts[1], "<code>1</code>") || !strings.Contains(texts[1], "<code>2</code>") || strings.Contains(texts[1], "<code>3</code>") {
		t.Fatalf("/labels MOVIES = %q", texts[1])
	}
	if !strings.Contains(texts[2], "<code>3</code>") || strings.Contains(texts[2], "<code>1</code>") {
		t.Fatalf("/labels - = %q", texts[2])
	}
	if texts[3] != "No torrent has the label <b>&lt;none&gt;</b>" {
		t.Fatalf("/labels <none> = %q", texts[3])
	}
}

// /setlabel sets the named torrents' labels in one request, and - removes
// them, which needs an empty list of labels.
func TestSetlabel(t *testing.T) {
	b, ft, fts := labelBot(t, "17")
	ctx := context.Background()
	b.setlabel(ctx, req("1", "3", "9", "hd", "new"))
	b.setlabel(ctx, req("2", "-"))
	b.setlabel(ctx, req("2", "a,b"))
	b.setlabel(ctx, req("hd"))

	want := []string{
		`{"method":"torrent-set","arguments":{"ids":[1,3],"labels":["hd","new"]}}`,
		`{"method":"torrent-set","arguments":{"ids":[2],"labels":[]}}`,
	}
	if got := fts.requestBodies("torrent-set"); !slices.Equal(got, want) {
		t.Fatalf("torrent-set requests = %q", got)
	}
	texts := ft.sentTexts()
	if len(texts) != 4 || texts[0] != "<b>Labelled hd, new:</b>\n<code>1</code> One\n<code>3</code> Three\n⚠️ No torrent has the ID 9" ||
		texts[1] != "<b>Labels removed:</b>\n<code>2</code> Two" || texts[2] != "<b>setlabel:</b> a label may not contain a comma" ||
		!strings.HasPrefix(texts[3], "<b>setlabel:</b> give") {
		t.Fatalf("replies = %q", texts)
	}
}

// Transmission before 3.00 has no labels, which the reply says.
func TestLabelsNeedTransmission3(t *testing.T) {
	b, ft, fts := labelBot(t, "15")
	b.labels(context.Background(), req())
	b.setlabel(context.Background(), req("1", "hd"))
	if got := fts.methodCount("torrent-set"); got != 0 {
		t.Fatalf("labels were set on an old Transmission (%d requests)", got)
	}
	for _, text := range ft.sentTexts() {
		if !strings.Contains(text, "labels need Transmission 3.00 or newer") {
			t.Fatalf("reply = %q", text)
		}
	}
}
