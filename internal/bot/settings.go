package bot

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/pyed/transmission"
	"github.com/pyed/transmission-telegram/v2/internal/state"
)

// sortUsage is the usage message for the /sort command.
const sortUsage = "<b>sort</b> takes one of: id, name, age, size, progress, downspeed, upspeed, download, upload, ratio\n" +
	"Start with <b>rev</b> for the reverse order: <code>/sort rev size</code> lists the biggest torrents first."

// sort changes the order this chat's lists show torrents in; each chat has
// its own, kept in the state file.
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
	if _, ok := sortKeys[method]; !ok {
		b.reply(ctx, r, "unknown sorting method")
		return
	}
	order, shown := method, method
	if reversed {
		order, shown = "rev "+method, "reversed "+method
	}
	if err := b.State.Update(func(data *state.Data) {
		if data.Sorts == nil {
			data.Sorts = make(map[int64]string)
		}
		data.Sorts[r.chatID] = order
	}); err != nil {
		b.Logger.Warn("Could not save the sort order; it lasts until the bot stops", "error", err)
	}
	b.reply(ctx, r, "<b>sort:</b> "+esc(shown))
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

// downlimit shows or sets the global download limit, in KB/s; 0 or off
// removes it.
func (b *Bot) downlimit(ctx context.Context, r *request) {
	b.speedLimit(ctx, r, "downlimit", "Download limit", func(s *transmission.Session) (int64, bool) {
		return s.SpeedLimitDown, s.SpeedLimitDownEnabled
	}, func(limit *int64, enabled bool) transmission.SessionSetOptions {
		return transmission.SessionSetOptions{SpeedLimitDown: limit, SpeedLimitDownEnabled: &enabled}
	})
}

// uplimit shows or sets the global upload limit, in KB/s; 0 or off removes
// it.
func (b *Bot) uplimit(ctx context.Context, r *request) {
	b.speedLimit(ctx, r, "uplimit", "Upload limit", func(s *transmission.Session) (int64, bool) {
		return s.SpeedLimitUp, s.SpeedLimitUpEnabled
	}, func(limit *int64, enabled bool) transmission.SessionSetOptions {
		return transmission.SessionSetOptions{SpeedLimitUp: limit, SpeedLimitUpEnabled: &enabled}
	})
}

// speedLimit shows a global limit without an argument, and otherwise sets
// it. Transmission takes a limit of 0 as a limit, which stalls every
// transfer, so 0 turns the limit off, as off does; the limit's value is
// then left as it was, for when it is turned on again elsewhere.
func (b *Bot) speedLimit(ctx context.Context, r *request, name, label string,
	current func(*transmission.Session) (int64, bool), set func(*int64, bool) transmission.SessionSetOptions) {
	if len(r.args) == 0 {
		session, err := b.Client.GetSession(ctx)
		if err != nil {
			b.replyError(ctx, r, name, err)
			return
		}
		limit, enabled := current(session)
		b.reply(ctx, r, "<b>"+label+":</b> "+describeLimit(limit, enabled))
		return
	}

	var limit int64
	if arg := strings.ToLower(r.args[0]); arg != "off" {
		parsed, err := strconv.ParseUint(arg, 10, 31)
		if err != nil {
			b.reply(ctx, r, "<b>"+name+":</b> give the limit in KB/s, or off")
			return
		}
		limit = int64(parsed)
	}
	enabled := limit > 0
	value := &limit
	if !enabled {
		value = nil
	}
	if err := b.Client.SetSession(ctx, set(value, enabled)); err != nil {
		b.replyError(ctx, r, name, err)
		return
	}
	b.reply(ctx, r, "<b>"+label+":</b> "+describeLimit(limit, enabled))
}

func describeLimit(limit int64, enabled bool) string {
	if !enabled {
		return "off"
	}
	return fmt.Sprintf("%d KB/s", limit)
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
