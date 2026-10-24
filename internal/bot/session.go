package bot

import (
	"context"
	"fmt"

	"github.com/pyed/transmission"
	"github.com/pyed/transmission-telegram/v2/internal/config"
)

// version shows Transmission's version, asked each time, and the bot's.
func (b *Bot) version(ctx context.Context, r *request) {
	session, err := b.Client.GetSession(ctx)
	if err != nil {
		b.replyError(ctx, r, "version", err)
		return
	}
	b.reply(ctx, r, fmt.Sprintf("Transmission <b>%s</b>\ntransmission-telegram <b>%s</b>", esc(session.Version), esc(config.Version)))
}

// whoami shows the sender's Telegram user ID, which -master takes, and the
// chat's.
func (b *Bot) whoami(ctx context.Context, r *request) {
	var userID int64
	if r.message != nil && r.message.From != nil {
		userID = r.message.From.ID
	}
	b.reply(ctx, r, fmt.Sprintf("Your Telegram user ID: <code>%d</code>\nThis chat's ID: <code>%d</code>", userID, r.chatID))
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
Ratio: <b>%s</b>
Running time: <b>%s</b>

<i>Accumulated</i>
Sessions: <b>%d</b>
Downloaded: <b>%s</b>
Uploaded: <b>%s</b>
Ratio: <b>%s</b>
Total running time: <b>%s</b>`,
		stats.TorrentCount,
		stats.ActiveTorrentCount,
		stats.PausedTorrentCount,
		size(stats.CurrentStats.DownloadedBytes),
		size(stats.CurrentStats.UploadedBytes),
		shareRatio(stats.CurrentStats.UploadedBytes, stats.CurrentStats.DownloadedBytes),
		stats.CurrentActiveTime(),
		stats.CumulativeStats.SessionCount,
		size(stats.CumulativeStats.DownloadedBytes),
		size(stats.CumulativeStats.UploadedBytes),
		shareRatio(stats.CumulativeStats.UploadedBytes, stats.CumulativeStats.DownloadedBytes),
		stats.CumulativeActiveTime(),
	))
}

// shareRatio is uploaded over downloaded, as Transmission works out its
// ratios; a dash before anything was downloaded.
func shareRatio(uploaded, downloaded uint64) string {
	if downloaded == 0 {
		return "–"
	}
	return ratio(float64(uploaded) / float64(downloaded))
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
