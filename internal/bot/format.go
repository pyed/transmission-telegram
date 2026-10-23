package bot

import (
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dustin/go-humanize"
	"github.com/pyed/transmission"
)

// progressBar generates a visual progress bar of the given width (e.g. [█████░░░░░]).
func progressBar(percent float64, width int) string {
	if width <= 0 {
		width = 10
	}
	if percent < 0 {
		percent = 0
	}
	if percent > 1.0 {
		percent = 1.0
	}

	filled := int(percent * float64(width))
	if filled > width {
		filled = width
	}

	return "[" + strings.Repeat("█", filled) + strings.Repeat("░", width-filled) + "]"
}

func trackerHosts(trackers []transmission.Tracker) []string {
	unique := make(map[string]struct{}, len(trackers))
	for _, tracker := range trackers {
		u, err := url.Parse(strings.TrimSpace(tracker.Announce))
		if err != nil {
			continue
		}
		host := strings.ToLower(u.Hostname())
		if host != "" {
			unique[host] = struct{}{}
		}
	}

	hosts := make([]string, 0, len(unique))
	for host := range unique {
		hosts = append(hosts, host)
	}
	sort.Strings(hosts)
	return hosts
}

// statusIcon shows a torrent's state at a glance.
func statusIcon(t *transmission.Torrent) string {
	if t.Error != transmission.ErrorNone {
		return "⚠️"
	}
	switch t.Status {
	case transmission.StatusStopped:
		return "⏸"
	case transmission.StatusCheckPending, transmission.StatusChecking:
		return "🔍"
	case transmission.StatusDownloadPending, transmission.StatusSeedPending:
		return "⏳"
	case transmission.StatusDownloading:
		return "⬇️"
	case transmission.StatusSeeding:
		return "⬆️"
	}
	return "❔"
}

// percent formats a fraction as a percentage with one decimal: "45.3%".
func percent(fraction float64) string {
	return strconv.FormatFloat(fraction*100, 'f', 1, 64) + "%"
}

// size formats a number of bytes: "1.2 GB".
func size(bytes uint64) string {
	return humanize.Bytes(bytes)
}

// rate formats a speed in bytes per second: "3.4 MB/s".
func rate(bytesPerSecond uint64) string {
	return humanize.Bytes(bytesPerSecond) + "/s"
}

// ratio formats an upload ratio. Transmission reports -1 when there is
// none yet and -2 for an infinite one.
func ratio(value float64) string {
	switch {
	case value == -2:
		return "∞"
	case value < 0:
		return "–"
	}
	return strconv.FormatFloat(value, 'f', 2, 64)
}

// eta formats Transmission's estimate in seconds; it reports -1 when there
// is none, as for a torrent that is not downloading, and -2 when it cannot
// tell.
func eta(seconds time.Duration) string {
	switch {
	case seconds == -2:
		return "∞"
	case seconds < 0:
		return "–"
	}
	return (seconds * time.Second).String()
}

// date formats a Unix time in the bot's time zone.
func date(unix int64) string {
	if unix <= 0 {
		return "–"
	}
	return time.Unix(unix, 0).Format("2006-01-02 15:04")
}

// torrentLine is a torrent in a list: the ID that commands take, its state,
// how far it is while not done, and its name.
func torrentLine(t *transmission.Torrent) string {
	line := fmt.Sprintf("<code>%d</code> %s ", t.ID, statusIcon(t))
	if t.PercentDone < 1 {
		line += percent(t.PercentDone) + " "
	}
	return line + esc(t.Name)
}

// speeds shows a torrent's transfer, or dashes once a message showing it is
// no longer updated.
func speeds(t *transmission.Torrent, live bool) string {
	if !live {
		return "↓ – · ↑ –"
	}
	return "↓ " + rate(t.RateDownload) + " · ↑ " + rate(t.RateUpload)
}

// torrentBlock is a torrent with its progress and transfer, as head, tail
// and active show it.
func torrentBlock(t *transmission.Torrent, live bool) string {
	return fmt.Sprintf("<code>%d</code> %s <b>%s</b>\n%s %s · %s · %s of %s\n%s · ratio %s",
		t.ID, statusIcon(t), esc(t.Name),
		progressBar(t.PercentDone, 10), percent(t.PercentDone), esc(t.TorrentStatus()), size(t.Have()), size(t.SizeWhenDone),
		speeds(t, live), ratio(t.UploadRatio))
}

// torrentCard is everything info shows about a torrent.
func torrentCard(t *transmission.Torrent, live bool) string {
	trackers := strings.Join(trackerHosts(t.Trackers), " ")
	if trackers == "" {
		trackers = "–"
	}
	estimate := eta(t.Eta)
	if !live {
		estimate = "–"
	}
	card := fmt.Sprintf("<code>%d</code> %s <b>%s</b>\n%s %s · %s · %s of %s\n%s · ratio %s\n"+
		"Downloaded %s · Uploaded %s\nAdded %s · ETA %s\nTrackers: <code>%s</code>",
		t.ID, statusIcon(t), esc(t.Name),
		progressBar(t.PercentDone, 10), percent(t.PercentDone), esc(t.TorrentStatus()), size(t.Have()), size(t.SizeWhenDone),
		speeds(t, live), ratio(t.UploadRatio),
		size(t.DownloadedEver), size(t.UploadedEver),
		date(t.AddedDate), estimate,
		esc(trackers))
	if t.Error != transmission.ErrorNone && t.ErrorString != "" {
		card += "\n⚠️ " + esc(t.ErrorString)
	}
	return card
}
