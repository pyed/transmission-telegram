package bot

import (
	"context"
	"errors"
	"fmt"
	"strconv"
)

// info sends a card for each torrent ID, with buttons, and keeps the last
// one up to date for a while.
func (b *Bot) info(ctx context.Context, r *request) {
	if len(r.args) == 0 {
		b.reply(ctx, r, "<b>info:</b> needs a torrent ID number")
		return
	}

	for _, id := range r.args {
		torrentID, err := strconv.Atoi(id)
		if err != nil {
			b.reply(ctx, r, "<b>info:</b> "+esc(id)+" is not a number")
			continue
		}
		t, err := b.cardTorrent(ctx, torrentID)
		switch {
		case errors.Is(err, errNoTorrent):
			b.reply(ctx, r, fmt.Sprintf("<b>info:</b> can't find a torrent with an ID of %d", torrentID))
		case err != nil:
			b.replyError(ctx, r, "info", err)
		default:
			b.sendCard(ctx, r.chatID, t)
		}
	}
}
