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

// digestZone is the bot's time zone in digest tests.
var digestZone = time.FixedZone("CET", 3600)

// digestBot is a bot whose master is 12345, at 08:30 on Wednesday 21
// October 2026, when Transmission has uploaded 10 GB and downloaded 20 GB
// in all. Setting *now moves the bot's clock.
func digestBot(t *testing.T) (*Bot, *fakeTelegram, *fakeTransmission, *time.Time) {
	t.Helper()
	ft := newFakeTelegram(t)
	fts := newFakeTransmission(t)
	fts.session = `{"version":"test","download-dir":"/downloads"}`
	fts.stats = `{"cumulative-stats":{"uploadedBytes":10000000000,"downloadedBytes":20000000000},` +
		`"current-stats":{"uploadedBytes":1000,"downloadedBytes":2000,"secondsActive":3600}}`
	fts.answers = map[string]string{"free-space": `{"path":"/downloads","size-bytes":500000000000}`}
	client, err := transmission.New(fts.URL+"/transmission/rpc", "", "")
	if err != nil {
		t.Fatalf("failed to create transmission client: %v", err)
	}
	b := newTestBot(t, ft.newBot(t), client)
	b.Config.Masters = config.MasterSlice{"12345"}
	now := time.Date(2026, 10, 21, 8, 30, 0, 0, digestZone)
	b.now = func() time.Time { return now }
	return b, ft, fts, &now
}

// savedDigest is chat 42's digest, and whether it has one.
func savedDigest(b *Bot) (state.Digest, bool) {
	var d state.Digest
	var ok bool
	b.State.Read(func(data *state.Data) { d, ok = data.Digest[42] })
	return d, ok
}

func TestLastDue(t *testing.T) {
	at := func(month time.Month, day, hour, minute int) time.Time {
		return time.Date(2026, month, day, hour, minute, 0, 0, digestZone)
	}
	for _, test := range []struct {
		time, every string
		now, want   time.Time
	}{
		{"08:00", "", at(10, 21, 8, 30), at(10, 21, 8, 0)},
		{"08:30", "", at(10, 21, 8, 30), at(10, 21, 8, 30)},
		{"09:00", "", at(10, 21, 8, 30), at(10, 20, 9, 0)},
		{"09:00", "", at(10, 1, 8, 30), at(9, 30, 9, 0)},
		{"08:00", "weekly", at(10, 21, 8, 30), at(10, 19, 8, 0)},
		{"08:00", "weekly", at(10, 19, 7, 0), at(10, 12, 8, 0)},
		{"08:00", "weekly", at(10, 25, 23, 0), at(10, 19, 8, 0)},
		{"08:00", "monthly", at(10, 21, 8, 30), at(10, 1, 8, 0)},
		{"08:00", "monthly", at(10, 1, 7, 59), at(9, 1, 8, 0)},
		{"08:00", "monthly", time.Date(2027, 1, 1, 7, 0, 0, 0, digestZone), at(12, 1, 8, 0)},
	} {
		d := state.Digest{Time: test.time, Every: test.every}
		if got := lastDue(d, test.now); !got.Equal(test.want) {
			t.Errorf("lastDue(%s %s, %s) = %s, want %s", test.time, digestPeriod(d), test.now, got, test.want)
		}
	}
}

func TestDigestCommand(t *testing.T) {
	b, ft, _, now := digestBot(t)
	ctx := context.Background()
	b.digest(ctx, req())
	b.digest(ctx, req("8:05", "Weekly"))
	d, _ := savedDigest(b)
	want := state.Digest{Time: "08:05", Every: "weekly", LastDue: "2026-10-19", Since: now.Unix(), Uploaded: 10000000000, Downloaded: 20000000000}
	if d != want {
		t.Fatalf("saved %+v\nwant %+v", d, want)
	}
	b.digest(ctx, req())
	b.digest(ctx, req("08:00", "daily"))
	if d, _ := savedDigest(b); d.Every != "" || d.LastDue != "2026-10-21" {
		t.Fatalf("saved %+v", d)
	}
	for _, args := range [][]string{{"25:00"}, {"08:00", "yearly"}, {"08:00", "weekly", "x"}, {"off", "now"}, {"soon"}} {
		b.digest(ctx, req(args...))
	}
	b.digest(ctx, req("OFF"))
	b.digest(ctx, req("off"))
	if _, ok := savedDigest(b); ok {
		t.Fatal("the digest is still on")
	}

	wantTexts := []string{
		"This chat gets no digest. Set one with <code>/digest HH:MM [daily|weekly|monthly]</code>, as in <code>/digest 08:00</code>.",
		"The weekly digest will come on Mondays at 08:05. Times are in the bot's time zone, CET. Send <code>/digest now</code> to see one.",
		"The weekly digest comes on Mondays at 08:05. Times are in the bot's time zone, CET.",
		"The daily digest will come every day at 08:00. Times are in the bot's time zone, CET. Send <code>/digest now</code> to see one.",
		digestUsage, digestUsage, digestUsage, digestUsage, digestUsage,
		"The digest is off.",
		"This chat gets no digest.",
	}
	if texts := ft.sentTexts(); !slices.Equal(texts, wantTexts) {
		t.Fatalf("sent %q\nwant %q", texts, wantTexts)
	}
}

// digestTorrents are torrents of all sorts, as of 08:30 on 21 October:
// one done long ago, one added and done since, one added since and not
// done, and three failing, two with the same error.
func digestTorrents(since time.Time) transmission.Torrents {
	before, after := since.Add(-48*time.Hour).Unix(), since.Unix()
	return transmission.Torrents{
		{ID: 1, Name: "Old", Status: transmission.StatusSeeding, AddedDate: before, DoneDate: before},
		{ID: 2, Name: "Film", Status: transmission.StatusSeeding, Labels: []string{"movies"}, AddedDate: after + 300, DoneDate: after + 900},
		{ID: 3, Name: "Show", Status: transmission.StatusDownloading, AddedDate: after + 1200},
		{ID: 4, Name: "Broken & bad", Status: transmission.StatusStopped, AddedDate: before,
			Error: transmission.ErrorLocalError, ErrorString: "No data found! <x>"},
		{ID: 5, Name: "Tracked", Status: transmission.StatusSeeding, AddedDate: before,
			Error: transmission.ErrorTrackerError, ErrorString: "Tracker gave HTTP response code 404 (Not Found)"},
		{ID: 6, Name: "Tracked too", Status: transmission.StatusSeeding, AddedDate: before,
			Error: transmission.ErrorTrackerError, ErrorString: "Tracker gave HTTP response code 404 (Not Found)"},
	}
}

// A digest comes when it is due, once, and covers the time since the last
// one: what finished and was added, the traffic, the torrents, their errors,
// and free space.
func TestDigestIsSentWhenDue(t *testing.T) {
	b, ft, fts, now := digestBot(t)
	ctx := context.Background()
	b.digest(ctx, req("09:00"))
	since := *now
	fts.set(func() {
		fts.torrents = digestTorrents(since)
		fts.stats = `{"cumulative-stats":{"uploadedBytes":13000000000,"downloadedBytes":25000000000}}`
	})
	b.checkDigest(ctx)
	if texts := ft.sentTexts(); len(texts) != 1 {
		t.Fatalf("a digest came before it was due: %q", texts)
	}

	*now = time.Date(2026, 10, 21, 9, 0, 0, 0, digestZone)
	b.checkDigest(ctx)
	*now = now.Add(time.Minute)
	b.checkDigest(ctx)
	texts := ft.sentTexts()[1:]
	want := "📰 <b>Daily digest</b>, Wed 21 Oct\n" +
		"<i>Since Wed 21 Oct 08:30</i>\n" +
		"\n" +
		"Completed: <b>1</b> (movies 1)\n" +
		"Added: <b>2</b> (movies 1, no label 1)\n" +
		"\n" +
		"Uploaded: <b>3.0 GB</b>\n" +
		"Downloaded: <b>5.0 GB</b>\n" +
		"\n" +
		"Torrents: <b>6</b> (1 downloading, 4 seeding, 1 paused, 3 with errors)\n" +
		"Errors:\n" +
		"• <i>Tracker gave HTTP response code 404 (Not Found)</i>: 2 torrents\n" +
		"• <i>No data found! &lt;x&gt;</i>: <code>4</code> Broken &amp; bad\n" +
		"\n" +
		"Free space: <b>500 GB</b>"
	if len(texts) != 1 || texts[0] != want {
		t.Fatalf("digests = %q\nwant %q", texts, want)
	}
	checkTelegramHTML(t, texts[0])
	d, _ := savedDigest(b)
	if d.LastDue != "2026-10-21" || d.Since != time.Date(2026, 10, 21, 9, 0, 0, 0, digestZone).Unix() ||
		d.Uploaded != 13000000000 || d.Downloaded != 25000000000 {
		t.Fatalf("saved %+v", d)
	}
}

// A digest missed while the bot was down comes as soon as it is back, once.
func TestDigestMissedWhileDown(t *testing.T) {
	b, ft, _, now := digestBot(t)
	ctx := context.Background()
	b.digest(ctx, req("09:00"))
	*now = now.AddDate(0, 0, 3)
	b.checkDigest(ctx)
	b.checkDigest(ctx)
	if texts := ft.sentTexts(); len(texts) != 2 || !strings.HasPrefix(texts[1], "📰 <b>Daily digest</b>, Sat 24 Oct\n<i>Since Wed 21 Oct 08:30</i>") {
		t.Fatalf("sent %q", texts)
	}
	if d, _ := savedDigest(b); d.LastDue != "2026-10-23" {
		t.Fatalf("saved %+v", d)
	}
}

// A digest Telegram fails to take is tried again a minute later; one it
// refuses is not; and a chat the bot can no longer post in gets none.
func TestDigestFailures(t *testing.T) {
	b, ft, _, now := digestBot(t)
	ctx := context.Background()
	b.digest(ctx, req("09:00"))
	set, _ := savedDigest(b)
	*now = time.Date(2026, 10, 21, 9, 0, 0, 0, digestZone)

	failure := `{"ok":false,"error_code":500,"description":"Internal Server Error"}`
	ft.script("sendMessage", failure, failure, failure, failure)
	b.checkDigest(ctx)
	if d, _ := savedDigest(b); d != set {
		t.Fatalf("after a failure, saved %+v", d)
	}
	b.checkDigest(ctx)
	if d, _ := savedDigest(b); d.LastDue != "2026-10-21" || len(ft.sentTexts()) != 2 {
		t.Fatalf("the digest was not tried again: %+v", d)
	}

	*now = now.AddDate(0, 0, 1)
	ft.script("sendMessage", `{"ok":false,"error_code":400,"description":"Bad Request: message is too long"}`)
	b.checkDigest(ctx)
	b.checkDigest(ctx)
	d, _ := savedDigest(b)
	if d.LastDue != "2026-10-22" || d.Since != time.Date(2026, 10, 21, 9, 0, 0, 0, digestZone).Unix() || ft.callCount("sendMessage") != 7 {
		t.Fatalf("after a refusal, saved %+v, sendMessage calls %d", d, ft.callCount("sendMessage"))
	}

	*now = now.AddDate(0, 0, 1)
	ft.script("sendMessage", `{"ok":false,"error_code":403,"description":"Forbidden: bot was blocked by the user"}`)
	b.checkDigest(ctx)
	if _, ok := savedDigest(b); ok {
		t.Fatal("a chat that blocked the bot keeps its digest")
	}
}

// When Transmission does not answer, no digest is made, and it comes when
// Transmission is back.
func TestDigestWhileTransmissionIsDown(t *testing.T) {
	b, ft, _, now := digestBot(t)
	ctx := context.Background()
	b.digest(ctx, req("09:00"))
	set, _ := savedDigest(b)
	*now = time.Date(2026, 10, 21, 9, 0, 0, 0, digestZone)
	b.Client, _ = transmission.New("http://127.0.0.1:1/transmission/rpc", "", "")
	b.checkDigest(ctx)
	if d, _ := savedDigest(b); d != set || len(ft.sentTexts()) != 1 {
		t.Fatalf("saved %+v, sent %q", d, ft.sentTexts())
	}
}

// /digest changed while a digest is being sent is kept.
func TestReplaceDigestKeepsAChange(t *testing.T) {
	b, _, _, _ := digestBot(t)
	changed := state.Digest{Time: "10:00"}
	b.saveDigest(42, &changed)
	b.replaceDigest(42, state.Digest{Time: "09:00"}, &state.Digest{Time: "09:00", LastDue: "2026-10-21"})
	b.replaceDigest(42, state.Digest{Time: "09:00"}, nil)
	if d, ok := savedDigest(b); !ok || d != changed {
		t.Fatalf("saved %+v, %v", d, ok)
	}
	b.replaceDigest(7, state.Digest{}, &changed)
	b.State.Read(func(data *state.Data) {
		if _, ok := data.Digest[7]; ok {
			t.Fatal("a digest was made for a chat without one")
		}
	})
}

// /digest now in a chat without a digest covers the last day, and the
// traffic of Transmission's session; so does a digest from before
// Transmission lost its statistics.
func TestDigestNow(t *testing.T) {
	b, ft, _, now := digestBot(t)
	ctx := context.Background()
	b.digest(ctx, req("now"))
	b.digest(ctx, req("09:00"))
	b.digest(ctx, req("now"))
	if err := b.State.Update(func(data *state.Data) {
		d := data.Digest[42]
		d.Uploaded = 99000000000
		data.Digest[42] = d
	}); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(time.Hour)
	b.digest(ctx, req("now"))

	// Transmission has run for an hour.
	texts := ft.sentTexts()
	for _, test := range []struct {
		text int
		want string
	}{
		{0, "📰 <b>Daily digest</b>, Wed 21 Oct\n<i>Since Tue 20 Oct 08:30</i>\n"},
		{0, "\nUploaded: <b>1.0 kB</b>\nDownloaded: <b>2.0 kB</b>\n<i>(since Wed 21 Oct 07:30, when Transmission started)</i>\n"},
		{2, "<i>Since Wed 21 Oct 08:30</i>\n"},
		{2, "\nUploaded: <b>0 B</b>\nDownloaded: <b>0 B</b>\n\nTorrents: <b>0</b>\n\nFree space: <b>500 GB</b>"},
		{3, "\nUploaded: <b>1.0 kB</b>\nDownloaded: <b>2.0 kB</b>\n<i>(since Wed 21 Oct 08:30, when Transmission started)</i>\n"},
	} {
		if !strings.Contains(texts[test.text], test.want) {
			t.Errorf("digest %d lacks %q:\n%s", test.text, test.want, texts[test.text])
		}
	}
}

// A digest names the five most common errors, each once.
func TestErrorGroups(t *testing.T) {
	var failing transmission.Torrents
	for i, message := range []string{"a", "b", "b", "c", "", "d", "e", "f"} {
		failing = append(failing, &transmission.Torrent{ID: i + 1, Name: "t", Error: transmission.ErrorLocalError, ErrorString: message})
	}
	want := []string{
		"• <i>b</i>: 2 torrents",
		"• <i>no message</i>: <code>5</code> t",
		"• <i>a</i>: <code>1</code> t",
		"• <i>c</i>: <code>4</code> t",
		"• <i>d</i>: <code>6</code> t",
		"• and 2 other errors",
	}
	if got := errorGroups(failing); !slices.Equal(got, want) {
		t.Fatalf("errorGroups = %q\nwant %q", got, want)
	}
}

func TestLabelBreakdown(t *testing.T) {
	labelled := func(labels ...string) *transmission.Torrent { return &transmission.Torrent{Labels: labels} }
	many := transmission.Torrents{labelled("a", "<b>"), labelled("a"), labelled()}
	for _, label := range []string{"c", "d", "e", "f", "g", "h", "i", "j"} {
		many = append(many, labelled(label))
	}
	for _, test := range []struct {
		torrents transmission.Torrents
		want     string
	}{
		{nil, ""},
		{transmission.Torrents{labelled(), labelled()}, ""},
		{transmission.Torrents{labelled("tv"), labelled("Movies", "tv"), labelled()}, " (tv 2, Movies 1, no label 1)"},
		{many, " (a 2, &lt;b&gt; 1, c 1, d 1, e 1, f 1, g 1, h 1, 2 more labels, no label 1)"},
	} {
		if got := labelBreakdown(test.torrents); got != test.want {
			t.Errorf("labelBreakdown = %q, want %q", got, test.want)
		}
	}
}

// After a restart, a digest that came due while the bot was down comes at
// once.
func TestStartSendsDueDigests(t *testing.T) {
	b, ft, _, _ := digestBot(t)
	if err := b.State.Update(func(data *state.Data) {
		data.Digest = map[int64]state.Digest{42: {Time: "08:00", LastDue: "2026-10-01"}}
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() {
		b.Start(ctx)
		close(stopped)
	}()
	ft.waitForSent(t, 1)
	cancel()
	<-stopped
}
