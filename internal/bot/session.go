package bot

import (
	"context"
	"fmt"
	"strings"

	"github.com/pyed/transmission"
	"github.com/pyed/transmission-telegram/v2/internal/config"
)

// helpText is the reply to /help: config.Help, whose section headings are
// marked with asterisks, as HTML.
var helpText = func() string {
	lines := strings.Split(strings.TrimSpace(config.Help), "\n")
	for i, line := range lines {
		if len(line) > 2 && strings.HasPrefix(line, "*") && strings.HasSuffix(line, "*") {
			lines[i] = "<b>" + esc(strings.Trim(line, "*")) + "</b>"
		} else {
			lines[i] = esc(line)
		}
	}
	return strings.Join(lines, "\n")
}()

func (b *Bot) help(ctx context.Context, r *request) {
	b.reply(ctx, r, helpText)
}

// version shows Transmission's version, asked each time, and the bot's.
func (b *Bot) version(ctx context.Context, r *request) {
	session, err := b.Client.GetSession(ctx)
	if err != nil {
		b.replyError(ctx, r, "version", err)
		return
	}
	b.reply(ctx, r, fmt.Sprintf("Transmission <b>%s</b>\ntransmission-telegram <b>%s</b>", esc(session.Version), esc(config.Version)))
}

// stats shows Transmission's session and cumulative statistics.
func (b *Bot) stats(ctx context.Context, r *request) {
	stats, err := b.Client.GetStats(ctx)
	if err != nil {
		b.replyError(ctx, r, "stats", err)
		return
	}

	b.reply(ctx, r, fmt.Sprintf(`Total: <b>%d</b>
Active: <b>%d</b>
Paused: <b>%d</b>

<i>Current session</i>
Downloaded: <b>%s</b>
Uploaded: <b>%s</b>
Running time: <b>%s</b>

<i>Accumulated</i>
Sessions: <b>%d</b>
Downloaded: <b>%s</b>
Uploaded: <b>%s</b>
Total running time: <b>%s</b>`,
		stats.TorrentCount,
		stats.ActiveTorrentCount,
		stats.PausedTorrentCount,
		size(stats.CurrentStats.DownloadedBytes),
		size(stats.CurrentStats.UploadedBytes),
		stats.CurrentActiveTime(),
		stats.CumulativeStats.SessionCount,
		size(stats.CumulativeStats.DownloadedBytes),
		size(stats.CumulativeStats.UploadedBytes),
		stats.CumulativeActiveTime(),
	))
}

// speed shows the current download and upload speeds, updating them live.
func (b *Bot) speed(ctx context.Context, r *request) {
	b.replyLive(ctx, r, "speed", "", func(live bool) (string, error) {
		if !live {
			return "↓ –  ↑ –", nil
		}
		stats, err := b.Client.GetStats(ctx)
		if err != nil {
			return "", err
		}
		return "↓ " + rate(stats.DownloadSpeed) + "  ↑ " + rate(stats.UploadSpeed), nil
	})
}

// count shows how many torrents are in each state.
func (b *Bot) count(ctx context.Context, r *request) {
	torrents, err := b.Client.GetTorrents(ctx)
	if err != nil {
		b.replyError(ctx, r, "count", err)
		return
	}

	var downloading, seeding, stopped, checking, downloadingQ, seedingQ, checkingQ int
	for _, t := range torrents {
		switch t.Status {
		case transmission.StatusDownloading:
			downloading++
		case transmission.StatusSeeding:
			seeding++
		case transmission.StatusStopped:
			stopped++
		case transmission.StatusChecking:
			checking++
		case transmission.StatusDownloadPending:
			downloadingQ++
		case transmission.StatusSeedPending:
			seedingQ++
		case transmission.StatusCheckPending:
			checkingQ++
		}
	}

	b.reply(ctx, r, fmt.Sprintf("Downloading: %d\nSeeding: %d\nPaused: %d\nVerifying: %d\n\n<i>Waiting to</i>\nDownload: %d\nSeed: %d\nVerify: %d\n\nTotal: %d",
		downloading, seeding, stopped, checking, downloadingQ, seedingQ, checkingQ, len(torrents)))
}
