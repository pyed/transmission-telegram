package notify

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/pyed/transmission"
	"github.com/pyed/transmission-telegram/v2/internal/state"
)

// fakeTransmission is the watcher's view of Transmission, in memory.
type fakeTransmission struct {
	mu       sync.Mutex
	torrents transmission.Torrents
	// recent are the IDs "recently-active" returns; every torrent when nil.
	recent  []int
	removed []int
	err     error
	free    int64
	calls   []string
}

func (f *fakeTransmission) copies(keep func(*transmission.Torrent) bool) transmission.Torrents {
	var out transmission.Torrents
	for _, t := range f.torrents {
		if keep(t) {
			copied := *t
			out = append(out, &copied)
		}
	}
	return out
}

func (f *fakeTransmission) GetTorrentsByIDs(_ context.Context, ids []int, _ ...string) (transmission.Torrents, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "full")
	if f.err != nil {
		return nil, f.err
	}
	return f.copies(func(t *transmission.Torrent) bool { return ids == nil || slices.Contains(ids, t.ID) }), nil
}

func (f *fakeTransmission) GetRecentlyActive(context.Context, ...string) (transmission.Torrents, []int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "recent")
	if f.err != nil {
		return nil, nil, f.err
	}
	return f.copies(func(t *transmission.Torrent) bool { return f.recent == nil || slices.Contains(f.recent, t.ID) }), f.removed, nil
}

func (f *fakeTransmission) GetSession(context.Context) (*transmission.Session, error) {
	return &transmission.Session{DownloadDir: "/downloads"}, nil
}

func (f *fakeTransmission) FreeSpace(context.Context, string) (int64, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "free")
	return f.free, 0, nil
}

// set changes the fake's torrents under its lock.
func (f *fakeTransmission) set(change func(f *fakeTransmission)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	change(f)
}

// sent is a notification as the watcher sent it.
type sent struct {
	chatID int64
	text   string
}

// watcherTest is a watcher with a fake Transmission, a state in memory where
// master chat 42 is the last one used, and a clock moved by hand.
type watcherTest struct {
	*Watcher
	transmission *fakeTransmission
	now          time.Time
	sent         []sent
	// opened holds the callback data of each sent notification's button,
	// empty for one without.
	opened []string
	// fail, when set, is the error the next sends return.
	fail error
}

func newWatcherTest(t *testing.T, torrents ...*transmission.Torrent) *watcherTest {
	t.Helper()
	store, err := state.Load("")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(data *state.Data) { data.LastChat = 42 }); err != nil {
		t.Fatal(err)
	}
	wt := &watcherTest{transmission: &fakeTransmission{torrents: torrents}, now: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)}
	wt.Watcher = &Watcher{
		Transmission: wt.transmission,
		State:        store,
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		Send: func(_ context.Context, chatID int64, text string, markup models.ReplyMarkup) error {
			if wt.fail != nil {
				return wt.fail
			}
			wt.sent = append(wt.sent, sent{chatID, text})
			var opened string
			if keyboard, ok := markup.(*models.InlineKeyboardMarkup); ok {
				opened = keyboard.InlineKeyboard[0][0].CallbackData
			}
			wt.opened = append(wt.opened, opened)
			return nil
		},
		Open: func(id int, hash string) models.ReplyMarkup {
			return &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{{
				{Text: "open", CallbackData: fmt.Sprintf("open:%d:%s", id, hash[:4])},
			}}}
		},
		now: func() time.Time { return wt.now },
	}
	return wt
}

// step moves the clock one interval on and checks.
func (wt *watcherTest) step() {
	wt.now = wt.now.Add(defaultInterval)
	wt.check(context.Background())
}

func (wt *watcherTest) texts() []string {
	var texts []string
	for _, message := range wt.sent {
		texts = append(texts, message.text)
	}
	return texts
}

func torrent(id int, name string, done float64) *transmission.Torrent {
	return &transmission.Torrent{
		ID: id, Name: name, HashString: fmt.Sprintf("%040x", id*7919),
		Status: transmission.StatusDownloading, PercentDone: done, MetadataPercentComplete: 1,
	}
}

// A torrent that finishes is announced once, with its ID and size, in the
// chat a master used last; torrents done before are not.
func TestCompletionsAreAnnouncedOnce(t *testing.T) {
	downloading := torrent(1, "Debian <12>", 0.5)
	downloading.SizeWhenDone = 4_000_000_000
	wt := newWatcherTest(t, downloading, torrent(2, "Done already", 1))

	wt.step()
	if len(wt.sent) != 0 {
		t.Fatalf("announced torrents at the first look: %q", wt.texts())
	}
	wt.transmission.set(func(f *fakeTransmission) { f.torrents[0].PercentDone = 1 })
	wt.step()
	wt.step()
	if want := []sent{{42, "✅ <b>Finished:</b> <code>1</code> Debian &lt;12&gt; (4.0 GB)"}}; !slices.Equal(wt.sent, want) {
		t.Fatalf("sent %v, want %v", wt.sent, want)
	}
	if want := []string{"open:1:" + downloading.HashString[:4]}; !slices.Equal(wt.opened, want) {
		t.Fatalf("buttons %q, want %q", wt.opened, want)
	}
}

// Without a chat to tell, nothing is sent, and the completion is not saved
// up for later.
func TestNoChatNoNotifications(t *testing.T) {
	wt := newWatcherTest(t, torrent(1, "a", 0.5))
	if err := wt.State.Update(func(data *state.Data) { data.LastChat = 0 }); err != nil {
		t.Fatal(err)
	}
	wt.step()
	wt.transmission.set(func(f *fakeTransmission) { f.torrents[0].PercentDone = 1 })
	wt.step()
	if len(wt.sent) != 0 || len(wt.pending) != 0 {
		t.Fatalf("sent %q with no chat to tell, %d pending", wt.texts(), len(wt.pending))
	}
}

// Many torrents finishing at once, as after a recheck, come in one message.
func TestManyCompletionsComeTogether(t *testing.T) {
	var torrents []*transmission.Torrent
	for id := 1; id <= 5; id++ {
		torrents = append(torrents, torrent(id, fmt.Sprintf("t%d", id), 0.9))
	}
	wt := newWatcherTest(t, torrents...)
	wt.step()
	wt.transmission.set(func(f *fakeTransmission) {
		for _, t := range f.torrents {
			t.PercentDone = 1
		}
	})
	wt.step()
	texts := wt.texts()
	if len(texts) != 1 || !strings.HasPrefix(texts[0], "✅ <b>5 torrents finished:</b>\n<code>1</code> t1") {
		t.Fatalf("sent %q", texts)
	}
	if wt.opened[0] != "" {
		t.Fatalf("a notification of several torrents has a button: %q", wt.opened[0])
	}
}

// Torrents that finished while the bot was down are announced when it is
// back, and nothing else is.
func TestCompletionsWhileAwayAreAnnounced(t *testing.T) {
	first := newWatcherTest(t, torrent(1, "Finishes while away", 0.5), torrent(2, "Still going", 0.2), torrent(3, "Done", 1))
	first.step()
	var saved []string
	first.State.Read(func(data *state.Data) { saved = slices.Clone(data.Incomplete) })
	if len(saved) != 2 {
		t.Fatalf("saved %d incomplete torrents, want 2", len(saved))
	}

	again := newWatcherTest(t, torrent(1, "Finishes while away", 1), torrent(2, "Still going", 0.3), torrent(3, "Done", 1))
	if err := again.State.Update(func(data *state.Data) { data.Incomplete = saved }); err != nil {
		t.Fatal(err)
	}
	again.step()
	again.step()
	texts := again.texts()
	if len(texts) != 1 || texts[0] != "✅ <b>Finished while the bot was away:</b>\n<code>1</code> Finishes while away" {
		t.Fatalf("sent %q", texts)
	}
}

// Verifying a finished torrent can show it as unfinished for a while; that
// is no new completion. Neither is a magnet link before its metadata.
func TestVerifyingAndMetadataAreNotCompletions(t *testing.T) {
	checked := torrent(1, "checked", 1)
	magnet := torrent(2, "magnet", 0)
	magnet.MetadataPercentComplete = 0.2
	wt := newWatcherTest(t, checked, magnet)
	wt.step()
	wt.transmission.set(func(f *fakeTransmission) {
		f.torrents[0].Status = transmission.StatusChecking
		f.torrents[0].PercentDone = 0.3
		// Before its metadata, a magnet's progress means nothing.
		f.torrents[1].MetadataPercentComplete = 0.6
		f.torrents[1].PercentDone = 1
	})
	wt.step()
	wt.transmission.set(func(f *fakeTransmission) {
		f.torrents[0].Status = transmission.StatusSeeding
		f.torrents[0].PercentDone = 1
		f.torrents[1].MetadataPercentComplete = 1
		f.torrents[1].PercentDone = 0
	})
	wt.step()
	if len(wt.sent) != 0 {
		t.Fatalf("sent %q", wt.texts())
	}
	wt.transmission.set(func(f *fakeTransmission) { f.torrents[1].PercentDone = 1 })
	wt.step()
	if texts := wt.texts(); len(texts) != 1 || !strings.Contains(texts[0], "magnet") {
		t.Fatalf("the magnet's completion: %q", texts)
	}
}

// Between full lists every five minutes, the watcher asks only for the
// torrents active lately, and lists everything again after a gap longer than
// "recently-active" covers.
func TestRecentlyActiveBetweenFullLists(t *testing.T) {
	wt := newWatcherTest(t, torrent(1, "a", 0.5))
	for range 3 {
		wt.step()
	}
	wt.now = wt.now.Add(fullSyncInterval)
	wt.step()
	wt.step()
	wt.now = wt.now.Add(time.Minute)
	wt.step()
	want := []string{"full", "recent", "recent", "full", "recent", "full"}
	if !slices.Equal(wt.transmission.calls, want) {
		t.Fatalf("requests = %v, want %v", wt.transmission.calls, want)
	}
}

// A torrent that finishes between full lists is seen through
// "recently-active", and one removed is forgotten.
func TestCompletionsSeenAmongRecentlyActive(t *testing.T) {
	wt := newWatcherTest(t, torrent(1, "a", 0.5), torrent(2, "b", 0.5))
	wt.step()
	wt.transmission.set(func(f *fakeTransmission) {
		f.recent = []int{1}
		f.torrents[0].PercentDone = 1
		f.removed = []int{2}
		f.torrents = f.torrents[:1]
	})
	wt.step()
	if texts := wt.texts(); len(texts) != 1 || !strings.Contains(texts[0], "<code>1</code> a") {
		t.Fatalf("sent %q", texts)
	}
	if _, known := wt.done[torrent(2, "b", 0).HashString]; known {
		t.Fatal("the removed torrent is still known")
	}
}

// Chats hear of an outage once Transmission has not answered three checks
// in a row, once, and of the recovery once.
func TestOutageAndRecoveryAreAnnouncedOnce(t *testing.T) {
	wt := newWatcherTest(t)
	wt.transmission.set(func(f *fakeTransmission) { f.err = errors.New("connection refused") })
	for range outageFailureThreshold + 2 {
		wt.step()
	}
	wt.transmission.set(func(f *fakeTransmission) { f.err = nil })
	wt.step()
	wt.step()
	want := []string{outageMessage, recoveryMessage}
	if texts := wt.texts(); !slices.Equal(texts, want) {
		t.Fatalf("sent %q, want %q", texts, want)
	}
}

func TestBriefOutagesAreNotAnnounced(t *testing.T) {
	wt := newWatcherTest(t)
	wt.transmission.set(func(f *fakeTransmission) { f.err = errors.New("temporary failure") })
	for range outageFailureThreshold - 1 {
		wt.step()
	}
	wt.transmission.set(func(f *fakeTransmission) { f.err = nil })
	wt.step()
	if len(wt.sent) != 0 {
		t.Fatalf("a brief outage was announced: %q", wt.texts())
	}
}

// New errors go to the chats that chose them, grouped by message, once per
// error; tracker warnings are not errors.
func TestNewErrorsAreAnnouncedOnce(t *testing.T) {
	wt := newWatcherTest(t, torrent(1, "a", 1), torrent(2, "b", 1), torrent(3, "c", 1))
	if err := wt.State.Update(func(data *state.Data) { data.Notify = map[int64][]string{7: {Errors}} }); err != nil {
		t.Fatal(err)
	}
	wt.step()
	fail := func(t *transmission.Torrent, code int, message string) {
		t.Error, t.ErrorString = code, message
	}
	wt.transmission.set(func(f *fakeTransmission) {
		fail(f.torrents[0], transmission.ErrorTrackerError, "Unregistered torrent")
		fail(f.torrents[1], transmission.ErrorTrackerError, "Unregistered torrent")
		fail(f.torrents[2], transmission.ErrorTrackerWarning, "Could not connect to tracker")
	})
	wt.step()
	wt.step()
	wt.transmission.set(func(f *fakeTransmission) { fail(f.torrents[0], transmission.ErrorNone, "") })
	wt.step()
	wt.transmission.set(func(f *fakeTransmission) { fail(f.torrents[0], transmission.ErrorLocalError, "No space left") })
	wt.step()
	want := []sent{
		{7, "⚠️ <b>2 torrents have an error:</b> <i>Unregistered torrent</i>\n<code>1</code> a\n<code>2</code> b"},
		{7, "⚠️ <b>Error:</b> <code>1</code> a\n<i>No space left</i>"},
	}
	if !slices.Equal(wt.sent, want) {
		t.Fatalf("sent %v\nwant %v", wt.sent, want)
	}
	if want := []string{"", "open:1:" + torrent(1, "a", 1).HashString[:4]}; !slices.Equal(wt.opened, want) {
		t.Fatalf("buttons %q, want %q", wt.opened, want)
	}
}

// Once a chat has chosen with /notify, notifications follow the choices,
// and the last chat used gets none by default.
func TestChatsFollowTheirChoices(t *testing.T) {
	data := &state.Data{LastChat: 42}
	if got := Chats(data, Completed); !slices.Equal(got, []int64{42}) {
		t.Fatalf("by default, completions go to %v", got)
	}
	if got := Chats(data, Errors); got != nil {
		t.Fatalf("by default, errors go to %v", got)
	}
	data.Notify = map[int64][]string{9: {Completed, Errors}, 8: {Completed}, 7: nil}
	if got := Chats(data, Completed); !slices.Equal(got, []int64{8, 9}) {
		t.Fatalf("completions go to %v", got)
	}
	if got := KindsOf(data, 42); got != nil {
		t.Fatalf("the last chat still gets %v", got)
	}
}

// A notification Telegram does not take is sent again at the next check,
// in order, and one Telegram refuses is not.
func TestFailedNotificationsAreSentAgain(t *testing.T) {
	wt := newWatcherTest(t, torrent(1, "a", 0.5), torrent(2, "b", 0.5))
	wt.step()
	wt.fail = errors.New("error do request for method sendMessage: connection reset")
	wt.transmission.set(func(f *fakeTransmission) { f.torrents[0].PercentDone = 1 })
	wt.step()
	wt.step()
	if len(wt.sent) != 0 || len(wt.pending) != 1 {
		t.Fatalf("while Telegram fails: sent %q, %d pending", wt.texts(), len(wt.pending))
	}

	wt.fail = nil
	wt.transmission.set(func(f *fakeTransmission) { f.torrents[1].PercentDone = 1 })
	wt.step()
	texts := wt.texts()
	if len(texts) != 2 || !strings.Contains(texts[0], " a") || !strings.Contains(texts[1], " b") {
		t.Fatalf("sent %q, want a's notification and then b's", texts)
	}

	if !slices.Equal(wt.opened, []string{"open:1:" + torrent(1, "a", 0).HashString[:4], "open:2:" + torrent(2, "b", 0).HashString[:4]}) {
		t.Fatalf("a notification sent again lost its button: %q", wt.opened)
	}

	wt.fail = fmt.Errorf("%w, Forbidden: bot was blocked by the user", tgbot.ErrorForbidden)
	wt.announce(context.Background(), Completed, "refused", nil)
	if len(wt.pending) != 0 {
		t.Fatal("a refused notification was kept to send again")
	}
}

// Free space below the limit is told once, to the chats that want it, and
// again only after it has recovered.
func TestLowDiskSpace(t *testing.T) {
	wt := newWatcherTest(t)
	wt.LowDisk = 5_000_000_000
	wt.transmission.free = 1_000_000_000
	wt.step()
	if len(wt.sent) != 0 || slices.Contains(wt.transmission.calls, "free") {
		t.Fatal("free space was checked with no chat wanting it")
	}
	if err := wt.State.Update(func(data *state.Data) { data.Notify = map[int64][]string{42: {Disk}} }); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		wt.now = wt.now.Add(diskInterval)
		wt.step()
	}
	wt.transmission.set(func(f *fakeTransmission) { f.free = 6_000_000_000 })
	wt.now = wt.now.Add(diskInterval)
	wt.step()
	wt.transmission.set(func(f *fakeTransmission) { f.free = 2_000_000_000 })
	wt.now = wt.now.Add(diskInterval)
	wt.step()
	want := "💾 <b>Low disk space:</b> 1.0 GB free in <code>/downloads</code>, below 5.0 GB"
	if texts := wt.texts(); len(texts) != 2 || texts[0] != want || !strings.HasPrefix(texts[1], "💾 <b>Low disk space:</b> 2.0 GB") {
		t.Fatalf("sent %q", texts)
	}
}

// Run looks at once, so that what happened while the bot was away is told
// promptly, and stops with its context.
func TestRunChecksAtOnceAndStops(t *testing.T) {
	wt := newWatcherTest(t)
	wt.Interval = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		wt.Run(ctx)
		close(done)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		wt.transmission.mu.Lock()
		calls := len(wt.transmission.calls)
		wt.transmission.mu.Unlock()
		if calls > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Run did not check at once")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop")
	}
}
