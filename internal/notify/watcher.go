// Package notify watches Transmission and tells chats what happened:
// torrents that finished or got an error, Transmission going down and
// coming back, and disk space running low.
package notify

import (
	"context"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/dustin/go-humanize"
	tgbot "github.com/go-telegram/bot"
	"github.com/pyed/transmission"
	"github.com/pyed/transmission-telegram/v2/internal/redact"
	"github.com/pyed/transmission-telegram/v2/internal/state"
)

// The kinds of notification a chat can choose with /notify.
const (
	Completed = "completed"
	Outages   = "outages"
	Errors    = "errors"
	Disk      = "disk"
)

// Kinds are every kind of notification, in the order /notify shows them.
var Kinds = []string{Completed, Outages, Errors, Disk}

// DefaultKinds are the notifications a chat gets until one chooses with
// /notify.
var DefaultKinds = []string{Completed, Outages}

const (
	defaultInterval = 10 * time.Second
	// fullSyncInterval is how often the watcher lists every torrent, rather
	// than only those active lately, to set right anything it missed.
	fullSyncInterval = 5 * time.Minute
	// recentWindow is how far back Transmission's "recently-active" reaches
	// (a minute), less a margin: after a longer gap the watcher lists every
	// torrent.
	recentWindow = 50 * time.Second
	// outageFailureThreshold is how many checks in a row must fail before
	// chats hear that Transmission is down.
	outageFailureThreshold = 3
	// diskInterval is how often free space is checked.
	diskInterval = 5 * time.Minute
	// maxSingleEvents is the most completions announced one message each;
	// more in one check come together.
	maxSingleEvents = 3
	// maxListed is the most torrents one notification names.
	maxListed = 30
	// maxPending and pendingLifetime bound the notifications kept to send
	// again after Telegram failed to take them.
	maxPending      = 100
	pendingLifetime = 24 * time.Hour
)

const (
	outageMessage   = "⚠️ Transmission is unreachable. I’ll keep retrying."
	recoveryMessage = "✅ Transmission is reachable again."
)

// watchFields are the torrent fields the watcher needs: a few, rather than
// the dozens a list fetches, since it asks every few seconds.
var watchFields = []string{
	"id", "hashString", "name", "status", "percentDone", "metadataPercentComplete",
	"sizeWhenDone", "error", "errorString",
}

// Transmission is what the watcher asks Transmission;
// *transmission.Client has it.
type Transmission interface {
	GetTorrentsByIDs(ctx context.Context, ids []int, fields ...string) (transmission.Torrents, error)
	GetRecentlyActive(ctx context.Context, fields ...string) (transmission.Torrents, []int, error)
	GetSession(ctx context.Context) (*transmission.Session, error)
	FreeSpace(ctx context.Context, path string) (freeBytes, totalBytes int64, err error)
}

// Sender sends an HTML message to a chat.
type Sender func(ctx context.Context, chatID int64, text string) error

// Watcher checks Transmission every Interval and tells the chats that want
// them about what happened. Run it once.
type Watcher struct {
	Transmission Transmission
	State        *state.Store
	Send         Sender
	Logger       *slog.Logger
	// Interval is the time between checks; 10 seconds when zero.
	Interval time.Duration
	// LowDisk is the free space, in bytes, below which chats that want them
	// get a warning; 0 turns the warnings off.
	LowDisk uint64

	now func() time.Time // tests set the clock

	initialized bool
	ids         map[int]string    // torrent ID to info-hash
	done        map[string]bool   // info-hash to whether it is done
	failed      map[string]string // info-hash to the error last seen
	away        map[string]bool   // not done when the bot last looked, not seen since
	saved       []string          // the incomplete torrents as last saved
	lastCheck   time.Time
	lastFull    time.Time

	failures       int
	outageNotified bool

	diskLow  bool
	lastDisk time.Time

	pending []pendingMessage
}

// pendingMessage is a notification Telegram did not take, to try again.
type pendingMessage struct {
	chatID int64
	text   string
	since  time.Time
}

// Run checks Transmission until ctx ends: once at once, so that what
// happened while the bot was away is told promptly, then every Interval.
func (w *Watcher) Run(ctx context.Context) {
	interval := w.Interval
	if interval <= 0 {
		interval = defaultInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		w.check(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (w *Watcher) clock() time.Time {
	if w.now != nil {
		return w.now()
	}
	return time.Now()
}

// check looks at Transmission once and announces what changed since the
// last look.
func (w *Watcher) check(ctx context.Context) {
	now := w.clock()
	w.resendPending(ctx, now)

	full := !w.initialized || now.Sub(w.lastFull) >= fullSyncInterval || now.Sub(w.lastCheck) > recentWindow
	var torrents transmission.Torrents
	var removed []int
	var err error
	if full {
		torrents, err = w.Transmission.GetTorrentsByIDs(ctx, nil, watchFields...)
	} else {
		torrents, removed, err = w.Transmission.GetRecentlyActive(ctx, watchFields...)
	}
	if err != nil {
		if ctx.Err() == nil {
			w.unanswered(ctx, err)
		}
		return
	}
	w.answered(ctx)
	w.lastCheck = now

	if !w.initialized {
		w.ids = make(map[int]string)
		w.done = make(map[string]bool)
		w.failed = make(map[string]string)
		w.away = make(map[string]bool)
		w.State.Read(func(data *state.Data) {
			w.saved = slices.Clone(data.Incomplete)
			for _, hash := range data.Incomplete {
				w.away[hash] = true
			}
		})
	}
	if full {
		w.lastFull = now
		w.forgetAllBut(torrents)
	}
	for _, id := range removed {
		w.forget(w.ids[id])
		delete(w.ids, id)
	}

	var completed, failing transmission.Torrents
	var finishedAway transmission.Torrents
	for _, t := range torrents {
		w.ids[t.ID] = t.HashString
		if t.Status == transmission.StatusChecking || t.Status == transmission.StatusCheckPending {
			// Progress means nothing until verifying ends.
			continue
		}
		isDone := t.MetadataPercentComplete >= 1 && t.PercentDone >= 1
		wasDone, known := w.done[t.HashString]
		w.done[t.HashString] = isDone
		switch {
		case w.away[t.HashString]:
			delete(w.away, t.HashString)
			if isDone {
				finishedAway = append(finishedAway, t)
			}
		case known && !wasDone && isDone:
			completed = append(completed, t)
		}

		if t.Error == transmission.ErrorTrackerError || t.Error == transmission.ErrorLocalError {
			if w.initialized && w.failed[t.HashString] != t.ErrorString {
				failing = append(failing, t)
			}
			w.failed[t.HashString] = t.ErrorString
		} else {
			delete(w.failed, t.HashString)
		}
	}
	w.initialized = true

	if len(finishedAway) > 0 {
		w.announce(ctx, Completed, listed("✅ <b>Finished while the bot was away:</b>", finishedAway))
	}
	w.announceCompleted(ctx, completed)
	w.announceErrors(ctx, failing)
	w.saveIncomplete()
	w.checkDisk(ctx, now)
}

// forgetAllBut forgets the torrents not in torrents, which are all there
// are.
func (w *Watcher) forgetAllBut(torrents transmission.Torrents) {
	present := make(map[string]bool, len(torrents))
	for _, t := range torrents {
		present[t.HashString] = true
	}
	for id, hash := range w.ids {
		if !present[hash] {
			delete(w.ids, id)
		}
	}
	for hash := range w.done {
		if !present[hash] {
			w.forget(hash)
		}
	}
}

func (w *Watcher) forget(hash string) {
	delete(w.done, hash)
	delete(w.failed, hash)
}

// announceCompleted tells chats about finished torrents: a message each,
// or one for them all when many finished together, as after a recheck.
func (w *Watcher) announceCompleted(ctx context.Context, completed transmission.Torrents) {
	if len(completed) > maxSingleEvents {
		w.announce(ctx, Completed, listed(fmt.Sprintf("✅ <b>%d torrents finished:</b>", len(completed)), completed))
		return
	}
	for _, t := range completed {
		text := fmt.Sprintf("✅ <b>Finished:</b> <code>%d</code> %s", t.ID, html.EscapeString(t.Name))
		if t.SizeWhenDone > 0 {
			text += " (" + humanize.Bytes(t.SizeWhenDone) + ")"
		}
		w.announce(ctx, Completed, text)
	}
}

// announceErrors tells chats about torrents with a new error, one message
// for each error message, so that a tracker failing every torrent makes one.
// Tracker warnings, such as a tracker not answering for a while, are not
// errors.
func (w *Watcher) announceErrors(ctx context.Context, failing transmission.Torrents) {
	var messages []string
	groups := make(map[string]transmission.Torrents)
	for _, t := range failing {
		if _, seen := groups[t.ErrorString]; !seen {
			messages = append(messages, t.ErrorString)
		}
		groups[t.ErrorString] = append(groups[t.ErrorString], t)
	}
	for _, message := range messages {
		group := groups[message]
		reason := "<i>" + html.EscapeString(message) + "</i>"
		if len(group) == 1 {
			w.announce(ctx, Errors, fmt.Sprintf("⚠️ <b>Error:</b> <code>%d</code> %s\n%s", group[0].ID, html.EscapeString(group[0].Name), reason))
			continue
		}
		w.announce(ctx, Errors, listed(fmt.Sprintf("⚠️ <b>%d torrents have an error:</b> %s", len(group), reason), group))
	}
}

// listed is a heading over torrents, a line each, as many as a notification
// names.
func listed(heading string, torrents transmission.Torrents) string {
	lines := []string{heading}
	for i, t := range torrents {
		if i == maxListed {
			lines = append(lines, fmt.Sprintf("…and %d more", len(torrents)-maxListed))
			break
		}
		lines = append(lines, fmt.Sprintf("<code>%d</code> %s", t.ID, html.EscapeString(t.Name)))
	}
	return strings.Join(lines, "\n")
}

// saveIncomplete saves which torrents are not done yet, when that changed,
// so that the next start can tell which finished while the bot was away.
// Torrents not seen since the bot started, as while they are verified, keep
// their place.
func (w *Watcher) saveIncomplete() {
	var incomplete []string
	for hash, done := range w.done {
		if !done {
			incomplete = append(incomplete, hash)
		}
	}
	for hash := range w.away {
		incomplete = append(incomplete, hash)
	}
	slices.Sort(incomplete)
	incomplete = slices.Compact(incomplete)
	if slices.Equal(incomplete, w.saved) {
		return
	}
	if err := w.State.Update(func(data *state.Data) { data.Incomplete = incomplete }); err != nil {
		w.Logger.Warn("Could not save which torrents are not done", "error", err)
	}
	w.saved = incomplete
}

// unanswered counts a failed check, and tells chats once Transmission has
// not answered several in a row.
func (w *Watcher) unanswered(ctx context.Context, err error) {
	w.failures++
	if w.failures == 1 {
		w.Logger.Warn("Notification watcher lost contact with Transmission", "error", redact.Error(err))
	}
	if w.failures >= outageFailureThreshold && !w.outageNotified {
		w.outageNotified = w.announce(ctx, Outages, html.EscapeString(outageMessage)) > 0
	}
}

// answered tells the chats that heard of an outage that it is over.
func (w *Watcher) answered(ctx context.Context) {
	if w.failures >= outageFailureThreshold {
		w.Logger.Info("Notification watcher reconnected to Transmission")
		if w.outageNotified {
			w.announce(ctx, Outages, html.EscapeString(recoveryMessage))
		}
	}
	w.failures = 0
	w.outageNotified = false
}

// checkDisk warns chats that want it when free space where Transmission
// downloads falls below LowDisk; once more only after it has recovered.
func (w *Watcher) checkDisk(ctx context.Context, now time.Time) {
	if w.LowDisk == 0 || now.Sub(w.lastDisk) < diskInterval || len(w.chatsFor(Disk)) == 0 {
		return
	}
	w.lastDisk = now
	session, err := w.Transmission.GetSession(ctx)
	if err != nil {
		return
	}
	free, _, err := w.Transmission.FreeSpace(ctx, session.DownloadDir)
	if err != nil || free < 0 {
		return
	}
	switch {
	case uint64(free) < w.LowDisk && !w.diskLow:
		w.diskLow = true
		w.announce(ctx, Disk, fmt.Sprintf("💾 <b>Low disk space:</b> %s free in <code>%s</code>, below %s",
			humanize.Bytes(uint64(free)), html.EscapeString(session.DownloadDir), humanize.Bytes(w.LowDisk)))
	case uint64(free) >= w.LowDisk+w.LowDisk/10:
		w.diskLow = false
	}
}

// announce sends text to the chats that want notifications of kind, and
// returns how many there were.
func (w *Watcher) announce(ctx context.Context, kind, text string) int {
	chats := w.chatsFor(kind)
	for _, chatID := range chats {
		if err := w.Send(ctx, chatID, text); err != nil {
			w.sendFailed(chatID, text, err, w.clock())
		}
	}
	return len(chats)
}

func (w *Watcher) chatsFor(kind string) []int64 {
	var chats []int64
	w.State.Read(func(data *state.Data) { chats = Chats(data, kind) })
	return chats
}

// sendFailed keeps a notification Telegram did not take, unless it refused
// it for good, to send again at the next check.
func (w *Watcher) sendFailed(chatID int64, text string, err error, since time.Time) {
	if refused(err) {
		w.Logger.Warn("Telegram refused a notification", "chat_id", chatID, "error", err)
		return
	}
	if len(w.pending) >= maxPending {
		w.Logger.Warn("Dropping a notification Telegram did not take: too many are waiting", "chat_id", chatID)
		return
	}
	w.pending = append(w.pending, pendingMessage{chatID: chatID, text: text, since: since})
}

// resendPending tries the notifications Telegram did not take again, in
// order, for up to a day.
func (w *Watcher) resendPending(ctx context.Context, now time.Time) {
	waiting := w.pending
	w.pending = nil
	for i, message := range waiting {
		if now.Sub(message.since) > pendingLifetime {
			continue
		}
		if err := w.Send(ctx, message.chatID, message.text); err != nil {
			if refused(err) {
				continue
			}
			// Telegram still does not answer: keep this and the rest.
			w.pending = append(w.pending, waiting[i:]...)
			return
		}
	}
}

// refused reports whether Telegram refused a message for good: the chat
// blocked the bot or is gone, or the message is wrong.
func refused(err error) bool {
	return errors.Is(err, tgbot.ErrorForbidden) || errors.Is(err, tgbot.ErrorBadRequest) ||
		errors.Is(err, tgbot.ErrorNotFound) || errors.Is(err, tgbot.ErrorUnauthorized) || errors.Is(err, context.Canceled)
}

// Chats returns the chats that get notifications of kind: those that chose
// it with /notify, or, until any chat has chosen, the chat a master used
// last, for the default kinds.
func Chats(data *state.Data, kind string) []int64 {
	if len(data.Notify) == 0 {
		if data.LastChat != 0 && slices.Contains(DefaultKinds, kind) {
			return []int64{data.LastChat}
		}
		return nil
	}
	var chats []int64
	for chatID, kinds := range data.Notify {
		if slices.Contains(kinds, kind) {
			chats = append(chats, chatID)
		}
	}
	slices.Sort(chats)
	return chats
}

// KindsOf returns the kinds of notification a chat gets.
func KindsOf(data *state.Data, chatID int64) []string {
	if len(data.Notify) == 0 {
		if chatID == data.LastChat {
			return slices.Clone(DefaultKinds)
		}
		return nil
	}
	return slices.Clone(data.Notify[chatID])
}
