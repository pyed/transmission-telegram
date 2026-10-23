package bot

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/pyed/transmission"
)

// sortUsage is the usage message for the /sort command.
const sortUsage = "<b>sort</b> takes one of: id, name, age, size, progress, downspeed, upspeed, download, upload, ratio\n" +
	"Start with <b>rev</b> for the reverse order: <code>/sort rev size</code> lists the biggest torrents first."

// sortings maps each /sort method to its order and the reverse.
var sortings = map[string][2]transmission.Sorting{
	"id":        {transmission.SortID, transmission.SortRevID},
	"name":      {transmission.SortName, transmission.SortRevName},
	"age":       {transmission.SortAge, transmission.SortRevAge},
	"size":      {transmission.SortSize, transmission.SortRevSize},
	"progress":  {transmission.SortProgress, transmission.SortRevProgress},
	"downspeed": {transmission.SortDownSpeed, transmission.SortRevDownSpeed},
	"upspeed":   {transmission.SortUpSpeed, transmission.SortRevUpSpeed},
	"download":  {transmission.SortDownloaded, transmission.SortRevDownloaded},
	"upload":    {transmission.SortUploaded, transmission.SortRevUploaded},
	"ratio":     {transmission.SortRatio, transmission.SortRevRatio},
}

// sort changes the order lists show torrents in.
func (b *Bot) sort(ctx context.Context, r *request) {
	args := r.args
	if len(args) == 0 {
		b.reply(ctx, r, sortUsage)
		return
	}

	var reversed bool
	if strings.ToLower(args[0]) == "rev" {
		reversed = true
		args = args[1:]
		// "/sort rev" without a key: used to panic on args[0] below,
		// crashing the whole process. Reply with usage instead.
		if len(args) == 0 {
			b.reply(ctx, r, sortUsage)
			return
		}
	}

	method := strings.ToLower(args[0])
	orders, ok := sortings[method]
	if !ok {
		b.reply(ctx, r, "unknown sorting method")
		return
	}
	if reversed {
		b.Client.SetSort(orders[1])
		b.reply(ctx, r, "<b>sort:</b> reversed "+esc(method))
		return
	}
	b.Client.SetSort(orders[0])
	b.reply(ctx, r, "<b>sort:</b> "+esc(method))
}

// trackers lists the tracker hosts and how many torrents each has.
func (b *Bot) trackers(ctx context.Context, r *request) {
	torrents, err := b.Client.GetTorrents(ctx)
	if err != nil {
		b.replyError(ctx, r, "trackers", err)
		return
	}

	counts := make(map[string]int)
	for _, t := range torrents {
		for _, host := range trackerHosts(t.Trackers) {
			counts[host]++
		}
	}
	if len(counts) == 0 {
		b.reply(ctx, r, "No trackers!")
		return
	}

	hosts := make([]string, 0, len(counts))
	for host := range counts {
		hosts = append(hosts, host)
	}
	sort.Strings(hosts)
	lines := make([]string, len(hosts))
	for i, host := range hosts {
		lines[i] = fmt.Sprintf("%d - %s", counts[host], esc(host))
	}
	b.reply(ctx, r, strings.Join(lines, "\n"))
}

// downloaddir sets Transmission's default download directory.
func (b *Bot) downloaddir(ctx context.Context, r *request) {
	if len(r.args) < 1 {
		b.reply(ctx, r, "Please, specify a path for downloaddir")
		return
	}

	downloadDir := strings.Join(r.args, " ")
	if err := b.Client.SetDownloadDir(ctx, downloadDir); err != nil {
		b.replyError(ctx, r, "downloaddir", err)
		return
	}
	b.reply(ctx, r, "<b>downloaddir:</b> downloaddir has been successfully changed to <code>"+esc(downloadDir)+"</code>")
}

// downlimit sets the global download limit in kilobytes per second.
func (b *Bot) downlimit(ctx context.Context, r *request) {
	b.speedLimit(ctx, r, transmission.DownloadLimitType)
}

// uplimit sets the global upload limit in kilobytes per second.
func (b *Bot) uplimit(ctx context.Context, r *request) {
	b.speedLimit(ctx, r, transmission.UploadLimitType)
}

func (b *Bot) speedLimit(ctx context.Context, r *request, limitType transmission.SpeedLimitType) {
	if len(r.args) < 1 {
		b.reply(ctx, r, "Please, specify the limit")
		return
	}

	limit, err := strconv.ParseUint(r.args[0], 10, 32)
	if err != nil {
		b.reply(ctx, r, "Please, specify the limit as number of kilobytes")
		return
	}

	if err := b.Client.SetSpeedLimit(ctx, limitType, uint(limit)); err != nil {
		b.replyError(ctx, r, string(limitType), err)
		return
	}
	b.reply(ctx, r, fmt.Sprintf("<b>%s:</b> limit has been successfully changed to %d KB/s", limitType, limit))
}

// turtle toggles or sets alternative speed limits ("Turtle Mode").
func (b *Bot) turtle(ctx context.Context, r *request) {
	const usage = "<b>turtle:</b> expected on or off"
	var requested *bool
	if len(r.args) > 1 {
		b.reply(ctx, r, usage)
		return
	}
	if len(r.args) == 1 {
		var enabled bool
		switch strings.ToLower(r.args[0]) {
		case "on", "1", "true", "enable":
			enabled = true
		case "off", "0", "false", "disable":
			enabled = false
		default:
			b.reply(ctx, r, usage)
			return
		}
		requested = &enabled
	}

	sess, err := b.Client.GetSession(ctx)
	if err != nil {
		b.replyError(ctx, r, "turtle", err)
		return
	}

	enabled := !sess.AltSpeedEnabled
	if requested != nil {
		enabled = *requested
	}

	if err := b.Client.SetAltSpeedEnabled(ctx, enabled); err != nil {
		b.replyError(ctx, r, "turtle", err)
		return
	}

	if enabled {
		b.reply(ctx, r, fmt.Sprintf("🐢 <b>Turtle mode enabled</b> (limits: ↓ %d KB/s  ↑ %d KB/s)", sess.AltSpeedDown, sess.AltSpeedUp))
	} else {
		b.reply(ctx, r, "🐇 <b>Turtle mode disabled</b>")
	}
}

// free shows the free disk space where Transmission downloads, or at a path.
func (b *Bot) free(ctx context.Context, r *request) {
	path := strings.Join(r.args, " ")
	if path == "" {
		sess, err := b.Client.GetSession(ctx)
		if err != nil {
			b.replyError(ctx, r, "free", err)
			return
		}
		path = sess.DownloadDir
	}

	freeBytes, totalBytes, err := b.Client.FreeSpace(ctx, path)
	if err != nil {
		b.replyError(ctx, r, "free", err)
		return
	}

	msg := fmt.Sprintf("💾 <code>%s</code>\nFree space: <b>%s</b>", esc(path), size(uint64(max(freeBytes, 0))))
	if totalBytes > 0 {
		msg += fmt.Sprintf(" of <b>%s</b> (%.1f%% free)", size(uint64(totalBytes)), float64(freeBytes)/float64(totalBytes)*100)
	}
	b.reply(ctx, r, msg)
}
