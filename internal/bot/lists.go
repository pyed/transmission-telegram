package bot

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/pyed/transmission"
)

// list lists every torrent, or with an argument only those with a tracker
// whose announce URL contains it.
func (b *Bot) list(ctx context.Context, r *request) {
	b.replyList(ctx, r, "list")
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
	if _, ok := b.countArgument(ctx, r, "latest"); ok {
		b.replyList(ctx, r, "latest")
	}
}

// search lists the torrents whose names contain the query, ignoring case.
func (b *Bot) search(ctx context.Context, r *request) {
	if len(r.args) == 0 {
		b.reply(ctx, r, "<b>search:</b> needs an argument")
		return
	}
	b.replyList(ctx, r, "search")
}

// downs lists the torrents downloading or waiting to.
func (b *Bot) downs(ctx context.Context, r *request) {
	b.replyList(ctx, r, "downs")
}

// seeding lists the torrents seeding or waiting to.
func (b *Bot) seeding(ctx context.Context, r *request) {
	b.replyList(ctx, r, "seeding")
}

// paused lists the stopped torrents.
func (b *Bot) paused(ctx context.Context, r *request) {
	b.replyList(ctx, r, "paused")
}

// checking lists the torrents being verified or waiting to be.
func (b *Bot) checking(ctx context.Context, r *request) {
	b.replyList(ctx, r, "checking")
}

// errors lists the torrents with errors, each with its error.
func (b *Bot) errors(ctx context.Context, r *request) {
	b.replyList(ctx, r, "errors")
}

// listing makes a list command's listing from Transmission's torrents now;
// a list's page and Back buttons make it again, so they show fresh data.
func (b *Bot) listing(ctx context.Context, chatID int64, command string, args []string) (listing, error) {
	torrents, err := b.torrents(ctx, chatID)
	if err != nil {
		return listing{}, err
	}
	keep := func(accept func(*transmission.Torrent) bool) transmission.Torrents {
		var kept transmission.Torrents
		for _, t := range torrents {
			if accept(t) {
				kept = append(kept, t)
			}
		}
		return kept
	}
	hasStatus := func(statuses ...int) transmission.Torrents {
		return keep(func(t *transmission.Torrent) bool { return slices.Contains(statuses, t.Status) })
	}
	switch command {
	case "list":
		if len(args) == 0 {
			return listing{torrents: torrents, none: "No torrents"}, nil
		}
		query := strings.ToLower(args[0])
		return listing{
			torrents: keep(func(t *transmission.Torrent) bool { return strings.Contains(strings.ToLower(t.GetTrackers()), query) }),
			none:     "No torrent has a tracker matching <b>" + esc(args[0]) + "</b>",
		}, nil
	case "latest":
		n := 5
		if len(args) > 0 {
			if given, err := strconv.Atoi(args[0]); err == nil && given > 0 {
				n = given
			}
		}
		sortTorrents(torrents, "rev age")
		return listing{torrents: torrents[:min(n, len(torrents))], none: "No torrents"}, nil
	case "search":
		query := strings.Join(args, " ")
		lowered := strings.ToLower(query)
		return listing{
			torrents: keep(func(t *transmission.Torrent) bool { return strings.Contains(strings.ToLower(t.Name), lowered) }),
			none:     "No torrent names match <b>" + esc(query) + "</b>",
		}, nil
	case "downs":
		return listing{torrents: hasStatus(transmission.StatusDownloading, transmission.StatusDownloadPending), none: "No downloads"}, nil
	case "seeding":
		return listing{torrents: hasStatus(transmission.StatusSeeding, transmission.StatusSeedPending), none: "No torrents seeding"}, nil
	case "paused":
		return listing{torrents: hasStatus(transmission.StatusStopped), none: "No paused torrents"}, nil
	case "checking":
		return listing{torrents: hasStatus(transmission.StatusChecking, transmission.StatusCheckPending), none: "No torrents verifying"}, nil
	case "labels":
		if len(args) == 0 || args[0] == "-" {
			return listing{torrents: keep(func(t *transmission.Torrent) bool { return len(t.Labels) == 0 }), none: "Every torrent has a label"}, nil
		}
		label := strings.Join(args, " ")
		return listing{
			torrents: keep(func(t *transmission.Torrent) bool {
				return slices.ContainsFunc(t.Labels, func(l string) bool { return strings.EqualFold(l, label) })
			}),
			none: "No torrent has the label <b>" + esc(label) + "</b>",
		}, nil
	case "errors":
		return listing{
			torrents: keep(func(t *transmission.Torrent) bool { return t.Error != transmission.ErrorNone }),
			line:     func(t *transmission.Torrent) string { return torrentLine(t) + "\n    " + esc(t.ErrorString) },
			none:     "No errors",
		}, nil
	}
	return listing{}, fmt.Errorf("no list is called %q", command)
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
