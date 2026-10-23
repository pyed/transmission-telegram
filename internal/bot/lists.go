package bot

import (
	"context"
	"strconv"
	"strings"

	"github.com/pyed/transmission"
)

// list lists every torrent, or with an argument only those with a tracker
// whose announce URL contains it.
func (b *Bot) list(ctx context.Context, r *request) {
	torrents, err := b.torrents(ctx, r.chatID)
	if err != nil {
		b.replyError(ctx, r, "list", err)
		return
	}
	var query string
	if len(r.args) > 0 {
		query = strings.ToLower(r.args[0])
	}
	var lines []string
	for _, t := range torrents {
		if query == "" || strings.Contains(strings.ToLower(t.GetTrackers()), query) {
			lines = append(lines, torrentLine(t))
		}
	}
	switch {
	case len(lines) > 0:
		b.reply(ctx, r, strings.Join(lines, "\n"))
	case query != "":
		b.reply(ctx, r, "No torrent has a tracker matching <b>"+esc(r.args[0])+"</b>")
	default:
		b.reply(ctx, r, "No torrents")
	}
}

// head shows the first n torrents (5 by default), updating them live.
func (b *Bot) head(ctx context.Context, r *request) {
	b.headTail(ctx, r, "head", false)
}

// tail shows the last n torrents (5 by default), updating them live.
func (b *Bot) tail(ctx context.Context, r *request) {
	b.headTail(ctx, r, "tail", true)
}

func (b *Bot) headTail(ctx context.Context, r *request, name string, fromEnd bool) {
	n, ok := b.countArgument(ctx, r, name)
	if !ok {
		return
	}
	render := func(live bool) (string, error) {
		torrents, err := b.torrents(ctx, r.chatID)
		if err != nil {
			return "", err
		}
		count := min(n, len(torrents))
		if fromEnd {
			torrents = torrents[len(torrents)-count:]
		} else {
			torrents = torrents[:count]
		}
		return torrentBlocks(torrents, live), nil
	}
	b.replyLive(ctx, r, name, "No torrents", render)
}

// latest lists the n torrents added last (5 by default), newest first.
func (b *Bot) latest(ctx context.Context, r *request) {
	n, ok := b.countArgument(ctx, r, "latest")
	if !ok {
		return
	}
	torrents, err := b.torrents(ctx, r.chatID)
	if err != nil {
		b.replyError(ctx, r, "latest", err)
		return
	}
	torrents.SortAge(true)
	b.replyLines(ctx, r, torrents[:min(n, len(torrents))], "No torrents")
}

// search lists the torrents whose names contain the query, ignoring case.
func (b *Bot) search(ctx context.Context, r *request) {
	if len(r.args) == 0 {
		b.reply(ctx, r, "<b>search:</b> needs an argument")
		return
	}
	query := strings.Join(r.args, " ")
	torrents, err := b.torrents(ctx, r.chatID)
	if err != nil {
		b.replyError(ctx, r, "search", err)
		return
	}
	lowered := strings.ToLower(query)
	var matches transmission.Torrents
	for _, t := range torrents {
		if strings.Contains(strings.ToLower(t.Name), lowered) {
			matches = append(matches, t)
		}
	}
	b.replyLines(ctx, r, matches, "No torrent names match <b>"+esc(query)+"</b>")
}

// downs lists the torrents downloading or waiting to.
func (b *Bot) downs(ctx context.Context, r *request) {
	b.replyFiltered(ctx, r, "downs", "No downloads", func(t *transmission.Torrent) bool {
		return t.Status == transmission.StatusDownloading || t.Status == transmission.StatusDownloadPending
	})
}

// seeding lists the torrents seeding or waiting to.
func (b *Bot) seeding(ctx context.Context, r *request) {
	b.replyFiltered(ctx, r, "seeding", "No torrents seeding", func(t *transmission.Torrent) bool {
		return t.Status == transmission.StatusSeeding || t.Status == transmission.StatusSeedPending
	})
}

// paused lists the stopped torrents.
func (b *Bot) paused(ctx context.Context, r *request) {
	b.replyFiltered(ctx, r, "paused", "No paused torrents", func(t *transmission.Torrent) bool {
		return t.Status == transmission.StatusStopped
	})
}

// checking lists the torrents being verified or waiting to be.
func (b *Bot) checking(ctx context.Context, r *request) {
	b.replyFiltered(ctx, r, "checking", "No torrents verifying", func(t *transmission.Torrent) bool {
		return t.Status == transmission.StatusChecking || t.Status == transmission.StatusCheckPending
	})
}

// errors lists the torrents with errors, each with its error.
func (b *Bot) errors(ctx context.Context, r *request) {
	torrents, err := b.torrents(ctx, r.chatID)
	if err != nil {
		b.replyError(ctx, r, "errors", err)
		return
	}
	var lines []string
	for _, t := range torrents {
		if t.Error != transmission.ErrorNone {
			lines = append(lines, torrentLine(t)+"\n    "+esc(t.ErrorString))
		}
	}
	if len(lines) == 0 {
		b.reply(ctx, r, "No errors")
		return
	}
	b.reply(ctx, r, strings.Join(lines, "\n"))
}

// active shows the torrents transferring now, updating them live.
func (b *Bot) active(ctx context.Context, r *request) {
	b.replyLive(ctx, r, "active", "No active torrents", func(live bool) (string, error) {
		torrents, err := b.torrents(ctx, r.chatID)
		if err != nil {
			return "", err
		}
		var transferring transmission.Torrents
		for _, t := range torrents {
			if t.RateDownload > 0 || t.RateUpload > 0 {
				transferring = append(transferring, t)
			}
		}
		return torrentBlocks(transferring, live), nil
	})
}

// countArgument reads the optional count head, tail and latest take; 5
// without one. A count below 1 is refused: it used to list every torrent.
func (b *Bot) countArgument(ctx context.Context, r *request, name string) (int, bool) {
	if len(r.args) == 0 {
		return 5, true
	}
	n, err := strconv.Atoi(r.args[0])
	if err != nil || n < 1 {
		b.reply(ctx, r, "<b>"+name+":</b> give how many torrents to show, 1 or more")
		return 0, false
	}
	return n, true
}

// replyFiltered lists the torrents keep accepts, or says none when there
// are none.
func (b *Bot) replyFiltered(ctx context.Context, r *request, name, none string, keep func(*transmission.Torrent) bool) {
	torrents, err := b.torrents(ctx, r.chatID)
	if err != nil {
		b.replyError(ctx, r, name, err)
		return
	}
	var kept transmission.Torrents
	for _, t := range torrents {
		if keep(t) {
			kept = append(kept, t)
		}
	}
	b.replyLines(ctx, r, kept, none)
}

// replyLines lists torrents a line each, or replies none when there are
// none.
func (b *Bot) replyLines(ctx context.Context, r *request, torrents transmission.Torrents, none string) {
	if len(torrents) == 0 {
		b.reply(ctx, r, none)
		return
	}
	lines := make([]string, len(torrents))
	for i, t := range torrents {
		lines[i] = torrentLine(t)
	}
	b.reply(ctx, r, strings.Join(lines, "\n"))
}

// torrentBlocks shows torrents a block each, with a blank line between, as
// many as fit one message.
func torrentBlocks(torrents transmission.Torrents, live bool) string {
	blocks := make([]string, len(torrents))
	for i, t := range torrents {
		blocks[i] = torrentBlock(t, live)
	}
	return fitBlocks(blocks, "\n\n")
}

// replyLive replies with what render shows and keeps the reply up to date
// in the background, so the chat's next command need not wait:
// render(true) while live, and render(false) for the last edit.
func (b *Bot) replyLive(ctx context.Context, r *request, name, none string, render func(live bool) (string, error)) {
	text, err := render(true)
	if err != nil {
		b.replyError(ctx, r, name, err)
		return
	}
	if text == "" {
		b.reply(ctx, r, none)
		return
	}
	messageID := b.reply(ctx, r, text)
	b.startLive(ctx, r.chatID, messageID, name, func() string {
		text, _ := render(true)
		return text
	}, func() string {
		text, _ := render(false)
		return text
	}, nil)
}
