package bot

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	tgbot "github.com/go-telegram/bot"
	"github.com/pyed/transmission"
	"github.com/pyed/transmission-telegram/v2/internal/state"
)

// A digest is a summary a chat gets every day, week, or month: what
// finished and what was added, what Transmission transferred, its torrents
// and their errors, and free space. The traffic comes from Transmission's
// cumulative statistics, which it keeps across its restarts.

const (
	// digestUsage tells how /digest is used.
	digestUsage = "<b>digest:</b> use <code>/digest HH:MM [daily|weekly|monthly]</code>, " +
		"<code>/digest now</code>, or <code>/digest off</code>"
	// maxDigestErrors is how many different errors a digest names.
	maxDigestErrors = 5
	// maxBreakdownLabels is how many labels a digest names in a count.
	maxBreakdownLabels = 8
)

// digestPeriods are how often a digest can come, as /digest takes them.
var digestPeriods = []string{"daily", "weekly", "monthly"}

// digestFields are the torrent fields a digest needs.
var digestFields = []string{"id", "name", "status", "error", "errorString", "addedDate", "doneDate", "labels"}

// digestPeriod is how often d comes: daily, weekly, or monthly.
func digestPeriod(d state.Digest) string {
	return cmp.Or(d.Every, "daily")
}

// digestWhen says when d comes, as in "on Mondays at 08:00".
func digestWhen(d state.Digest) string {
	switch digestPeriod(d) {
	case "weekly":
		return "on Mondays at " + d.Time
	case "monthly":
		return "on the 1st of each month at " + d.Time
	}
	return "every day at " + d.Time
}

// lastDue is the latest time, at or before now, when d was due: each day
// at its time, on Mondays for weekly digests, or on the 1st of the month
// for monthly ones.
func lastDue(d state.Digest, now time.Time) time.Time {
	minutes, _ := parseClock(d.Time)
	at := func(year int, month time.Month, day int) time.Time {
		return time.Date(year, month, day, minutes/60, minutes%60, 0, 0, now.Location())
	}
	switch digestPeriod(d) {
	case "weekly":
		monday := at(now.Year(), now.Month(), now.Day()-(int(now.Weekday())+6)%7)
		if monday.After(now) {
			return monday.AddDate(0, 0, -7)
		}
		return monday
	case "monthly":
		first := at(now.Year(), now.Month(), 1)
		if first.After(now) {
			return at(now.Year(), now.Month()-1, 1)
		}
		return first
	}
	today := at(now.Year(), now.Month(), now.Day())
	if today.After(now) {
		return today.AddDate(0, 0, -1)
	}
	return today
}

// digest answers /digest: HH:MM [daily|weekly|monthly] sets the chat's
// digest, now shows one at once, off ends it, and nothing says when it
// comes.
func (b *Bot) digest(ctx context.Context, r *request) {
	if len(r.args) > 2 {
		b.reply(ctx, r, digestUsage)
		return
	}
	now := b.timeNow()
	zone := "Times are in the bot's time zone, " + now.Format("MST") + "."
	var current state.Digest
	var has bool
	b.State.Read(func(data *state.Data) { current, has = data.Digest[r.chatID] })
	word := ""
	if len(r.args) > 0 {
		word = strings.ToLower(r.args[0])
	}
	switch {
	case len(r.args) == 0 && !has:
		b.reply(ctx, r, "This chat gets no digest. Set one with <code>/digest HH:MM [daily|weekly|monthly]</code>, as in <code>/digest 08:00</code>.")
	case len(r.args) == 0:
		b.reply(ctx, r, fmt.Sprintf("The %s digest comes %s. %s", digestPeriod(current), digestWhen(current), zone))
	case word == "off" && len(r.args) == 1:
		if !has {
			b.reply(ctx, r, "This chat gets no digest.")
			return
		}
		b.saveDigest(r.chatID, nil)
		b.reply(ctx, r, "The digest is off.")
	case word == "now" && len(r.args) == 1:
		text, _, err := b.buildDigest(ctx, now, current)
		if err != nil {
			b.replyError(ctx, r, "digest", err)
			return
		}
		b.reply(ctx, r, text)
	default:
		minutes, ok := parseClock(r.args[0])
		d := state.Digest{Time: clock(minutes)}
		if len(r.args) == 2 {
			d.Every = strings.ToLower(r.args[1])
		}
		if !ok || !slices.Contains(digestPeriods, digestPeriod(d)) {
			b.reply(ctx, r, digestUsage)
			return
		}
		if d.Every == "daily" {
			d.Every = ""
		}
		stats, err := b.Client.GetStats(ctx)
		if err != nil {
			b.replyError(ctx, r, "digest", err)
			return
		}
		d.Since = now.Unix()
		d.Uploaded, d.Downloaded = stats.CumulativeStats.UploadedBytes, stats.CumulativeStats.DownloadedBytes
		// The first digest comes when it is next due.
		d.LastDue = lastDue(d, now).Format(time.DateOnly)
		b.saveDigest(r.chatID, &d)
		b.reply(ctx, r, fmt.Sprintf("The %s digest will come %s. %s Send <code>/digest now</code> to see one.",
			digestPeriod(d), digestWhen(d), zone))
	}
}

// saveDigest sets a chat's digest, or removes it when d is nil.
func (b *Bot) saveDigest(chatID int64, d *state.Digest) {
	err := b.State.Update(func(data *state.Data) {
		if d == nil {
			delete(data.Digest, chatID)
			return
		}
		if data.Digest == nil {
			data.Digest = make(map[int64]state.Digest)
		}
		data.Digest[chatID] = *d
	})
	if err != nil {
		b.Logger.Warn("Could not save the digest; it lasts until the bot stops", "error", err)
	}
}

// watchDigest sends digests when they are due until ctx ends, looking every
// minute.
func (b *Bot) watchDigest(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		b.checkDigest(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// checkDigest sends each chat's digest once each time it is due, at its
// time or after, so that one missed while the bot was down comes as soon as
// it is back.
func (b *Bot) checkDigest(ctx context.Context) {
	now := b.timeNow()
	var digests map[int64]state.Digest
	b.State.Read(func(data *state.Data) { digests = maps.Clone(data.Digest) })
	for _, chatID := range slices.Sorted(maps.Keys(digests)) {
		was := digests[chatID]
		due := lastDue(was, now).Format(time.DateOnly)
		if was.LastDue >= due {
			continue
		}
		text, stats, err := b.buildDigest(ctx, now, was)
		if err != nil {
			b.Logger.Warn("Could not make the digests; trying again in a minute", "error", err)
			return
		}
		d := was
		_, err = b.send(ctx, chatID, text, nil)
		switch {
		case err == nil:
			d.LastDue, d.Since = due, now.Unix()
			d.Uploaded, d.Downloaded = stats.CumulativeStats.UploadedBytes, stats.CumulativeStats.DownloadedBytes
		case chatGone(err):
			b.Logger.Info("Stopped the digest of a chat the bot can no longer post in", "chat_id", chatID, "error", err)
			b.replaceDigest(chatID, was, nil)
			continue
		case errors.Is(err, tgbot.ErrorBadRequest):
			// Telegram would refuse it again. The next digest covers this
			// one's time too.
			b.Logger.Warn("Telegram refused a digest; it is not sent again", "chat_id", chatID, "error", err)
			d.LastDue = due
		default:
			continue // tried again in a minute
		}
		b.replaceDigest(chatID, was, &d)
	}
}

// replaceDigest replaces a chat's digest, was, with d, or removes it when d
// is nil, unless /digest changed it meanwhile.
func (b *Bot) replaceDigest(chatID int64, was state.Digest, d *state.Digest) {
	err := b.State.Update(func(data *state.Data) {
		if current, ok := data.Digest[chatID]; !ok || current != was {
			return
		}
		if d == nil {
			delete(data.Digest, chatID)
		} else {
			data.Digest[chatID] = *d
		}
	})
	if err != nil {
		b.Logger.Warn("Could not save the digest", "error", err)
	}
}

// buildDigest summarizes the time since d.Since, or the last day for a chat
// without a digest. It also returns Transmission's statistics, which the
// next digest counts from.
func (b *Bot) buildDigest(ctx context.Context, now time.Time, d state.Digest) (string, *transmission.Stats, error) {
	stats, err := b.Client.GetStats(ctx)
	if err != nil {
		return "", nil, err
	}
	torrents, err := b.Client.GetTorrentsByIDs(ctx, nil, digestFields...)
	if err != nil {
		return "", nil, err
	}
	const stamp = "Mon 2 Jan 15:04"
	since := time.Unix(d.Since, 0).In(now.Location())
	if d.Since == 0 {
		since = now.Add(-24 * time.Hour)
	}
	period := digestPeriod(d)
	lines := []string{
		fmt.Sprintf("📰 <b>%s digest</b>, %s", strings.ToUpper(period[:1])+period[1:], now.Format("Mon 2 Jan")),
		"<i>Since " + since.Format(stamp) + "</i>",
		"",
	}

	completed := keepTorrents(torrents, func(t *transmission.Torrent) bool { return t.DoneDate >= since.Unix() })
	added := keepTorrents(torrents, func(t *transmission.Torrent) bool { return t.AddedDate >= since.Unix() })
	lines = append(lines,
		fmt.Sprintf("Completed: <b>%d</b>%s", len(completed), labelBreakdown(completed)),
		fmt.Sprintf("Added: <b>%d</b>%s", len(added), labelBreakdown(added)),
		"")

	total := stats.CumulativeStats
	if d.Since != 0 && total.UploadedBytes >= d.Uploaded && total.DownloadedBytes >= d.Downloaded {
		lines = append(lines,
			"Uploaded: <b>"+size(total.UploadedBytes-d.Uploaded)+"</b>",
			"Downloaded: <b>"+size(total.DownloadedBytes-d.Downloaded)+"</b>")
	} else {
		// Nothing to count from: the chat has no digest yet, or
		// Transmission lost or reset its statistics since. Its session's
		// own count is what there is.
		started := now.Add(-stats.CurrentStats.SecondsActive * time.Second)
		lines = append(lines,
			"Uploaded: <b>"+size(stats.CurrentStats.UploadedBytes)+"</b>",
			"Downloaded: <b>"+size(stats.CurrentStats.DownloadedBytes)+"</b>",
			"<i>(since "+started.Format(stamp)+", when Transmission started)</i>")
	}

	failing := keepTorrents(torrents, func(t *transmission.Torrent) bool { return t.Error != transmission.ErrorNone })
	lines = append(lines, "", torrentStates(torrents, len(failing)))
	if len(failing) > 0 {
		lines = append(lines, "Errors:")
		lines = append(lines, errorGroups(failing)...)
	}

	if session, err := b.Client.GetSession(ctx); err == nil {
		if free, ok := b.freeSpace(ctx, session.DownloadDir); ok {
			lines = append(lines, "", "Free space: <b>"+size(free)+"</b>")
		}
	}
	return strings.Join(lines, "\n"), stats, nil
}

// keepTorrents returns the torrents accept accepts.
func keepTorrents(torrents transmission.Torrents, accept func(*transmission.Torrent) bool) transmission.Torrents {
	var kept transmission.Torrents
	for _, t := range torrents {
		if accept(t) {
			kept = append(kept, t)
		}
	}
	return kept
}

// torrentStates counts torrents by what they are doing, as in
// "Torrents: 12 (2 downloading, 10 seeding)".
func torrentStates(torrents transmission.Torrents, failing int) string {
	counts := make(map[int]int)
	for _, t := range torrents {
		counts[t.Status]++
	}
	var parts []string
	for _, state := range []struct {
		count int
		label string
	}{
		{counts[transmission.StatusDownloading], "downloading"},
		{counts[transmission.StatusSeeding], "seeding"},
		{counts[transmission.StatusStopped], "paused"},
		{counts[transmission.StatusChecking] + counts[transmission.StatusCheckPending], "verifying"},
		{counts[transmission.StatusDownloadPending] + counts[transmission.StatusSeedPending], "queued"},
		{failing, "with errors"},
	} {
		if state.count > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", state.count, state.label))
		}
	}
	text := fmt.Sprintf("Torrents: <b>%d</b>", len(torrents))
	if len(parts) > 0 {
		text += " (" + strings.Join(parts, ", ") + ")"
	}
	return text
}

// errorGroups lists torrents' errors, those with the same message
// together, the most common first.
func errorGroups(failing transmission.Torrents) []string {
	groups := make(map[string]transmission.Torrents)
	for _, t := range failing {
		groups[t.ErrorString] = append(groups[t.ErrorString], t)
	}
	messages := slices.SortedFunc(maps.Keys(groups), func(x, y string) int {
		return cmp.Or(cmp.Compare(len(groups[y]), len(groups[x])), cmp.Compare(x, y))
	})
	var lines []string
	for i, message := range messages {
		if i == maxDigestErrors {
			lines = append(lines, fmt.Sprintf("• and %d other errors", len(messages)-i))
			break
		}
		reason := "<i>" + esc(cmp.Or(message, "no message")) + "</i>"
		if group := groups[message]; len(group) == 1 {
			lines = append(lines, fmt.Sprintf("• %s: <code>%d</code> %s", reason, group[0].ID, esc(group[0].Name)))
		} else {
			lines = append(lines, fmt.Sprintf("• %s: %d torrents", reason, len(group)))
		}
	}
	return lines
}

// labelBreakdown counts torrents by label, as in " (movies 2, tv 1, no
// label 3)", or is empty when none of them has a label.
func labelBreakdown(torrents transmission.Torrents) string {
	counts, unlabelled := countLabels(torrents)
	if len(counts) == 0 {
		return ""
	}
	labels := byCount(counts)
	var parts []string
	for i, label := range labels {
		if i == maxBreakdownLabels {
			parts = append(parts, fmt.Sprintf("%d more labels", len(labels)-i))
			break
		}
		parts = append(parts, fmt.Sprintf("%s %d", esc(label), counts[label]))
	}
	if unlabelled > 0 {
		parts = append(parts, fmt.Sprintf("no label %d", unlabelled))
	}
	return " (" + strings.Join(parts, ", ") + ")"
}

// chatGone reports whether err says the bot can no longer post in a chat:
// it was blocked or removed, the chat no longer exists, or its group
// became a supergroup, which is a new chat.
func chatGone(err error) bool {
	text := strings.ToLower(err.Error())
	return errors.Is(err, tgbot.ErrorForbidden) || strings.Contains(text, "chat not found") ||
		strings.Contains(text, "upgraded to a supergroup")
}
