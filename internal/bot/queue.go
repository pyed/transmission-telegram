package bot

import (
	"context"
	"fmt"
	"strings"
)

const queueUsage = "<b>queue:</b> give <code>top</code>, <code>up</code>, <code>down</code> or <code>bottom</code> " +
	"and the torrents' IDs, as in <code>/queue top 5</code>"

// queue shows the torrents downloading or waiting to, in the order
// Transmission runs them; with top, up, down or bottom and IDs, it first
// moves those torrents in the queue.
func (b *Bot) queue(ctx context.Context, r *request) {
	if len(r.args) == 0 {
		b.replyList(ctx, r, "queue")
		return
	}
	moves := map[string]func(context.Context, ...int) error{
		"top": b.Client.QueueMoveTop, "up": b.Client.QueueMoveUp,
		"down": b.Client.QueueMoveDown, "bottom": b.Client.QueueMoveBottom,
	}
	move, ok := moves[strings.ToLower(r.args[0])]
	if !ok || len(r.args) == 1 {
		b.reply(ctx, r, queueUsage)
		return
	}
	torrents, problems, ok := b.torrentsNamed(ctx, &request{chatID: r.chatID, args: r.args[1:]}, "queue")
	if !ok {
		return
	}
	if len(torrents) > 0 {
		if err := move(ctx, torrents.GetIDs()...); err != nil {
			b.replyError(ctx, r, "queue", err)
			return
		}
	}
	if len(problems) > 0 {
		b.reply(ctx, r, summary("", nil, problems))
	}
	b.replyList(ctx, &request{chatID: r.chatID, message: r.message}, "queue")
}

// port asks Transmission whether peers can reach its port, which it checks
// with a service of the Transmission project.
func (b *Bot) port(ctx context.Context, r *request) {
	session, err := b.Client.GetSession(ctx)
	if err != nil {
		b.replyError(ctx, r, "port", err)
		return
	}
	open, err := b.Client.PortTest(ctx)
	if err != nil {
		b.replyError(ctx, r, "port", err)
		return
	}
	if open {
		b.reply(ctx, r, fmt.Sprintf("✅ <b>Port %d is open:</b> peers can connect to Transmission.", session.PeerPort))
		return
	}
	b.reply(ctx, r, fmt.Sprintf("⚠️ <b>Port %d is closed:</b> peers cannot connect to Transmission, which slows "+
		"downloads and seeding. Forward the port in your router, or let Transmission do it with UPnP.", session.PeerPort))
}
