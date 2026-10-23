package bot

import (
	"context"
	"fmt"
	"strconv"

	"github.com/go-telegram/bot/models"
)

// info sends a card for each torrent ID, with buttons, and keeps the cards
// up to date for a while.
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

		torrent, err := b.Client.GetTorrent(torrentID, ctx)
		if err != nil {
			b.reply(ctx, r, fmt.Sprintf("<b>info:</b> can't find a torrent with an ID of %d", torrentID))
			continue
		}

		keyboard := infoKeyboard(torrent.ID, torrent.HashString)
		messageID, _ := b.send(ctx, r.chatID, torrentCard(torrent, true), keyboard)

		// Keep the card live for 'duration * interval'.
		b.goSafe("live torrent info update", func() {
			card := func(live bool) func() string {
				return func() string {
					torrent, err := b.Client.GetTorrent(torrentID, ctx)
					if err != nil {
						return "" // skip this update
					}
					return torrentCard(torrent, live)
				}
			}
			b.liveUpdateWithKeyboard(ctx, r.chatID, messageID, card(true), card(false), keyboard)
		})
	}
}

// infoKeyboard returns inline action buttons for controlling a torrent. The
// callback includes a torrent fingerprint so buttons from an old message
// cannot affect a different torrent that later reuses the same numeric ID.
func infoKeyboard(torrentID int, hash string) *models.InlineKeyboardMarkup {
	fingerprint := torrentFingerprint(hash)
	return &models.InlineKeyboardMarkup{
		InlineKeyboard: [][]models.InlineKeyboardButton{
			{
				{Text: "⏸ Pause", CallbackData: fmt.Sprintf("cmd:stop:%d:%s", torrentID, fingerprint)},
				{Text: "▶ Resume", CallbackData: fmt.Sprintf("cmd:start:%d:%s", torrentID, fingerprint)},
				{Text: "🗑 Delete", CallbackData: fmt.Sprintf("cmd:del:%d:%s", torrentID, fingerprint)},
			},
		},
	}
}

func deleteConfirmationKeyboard(torrentID int, hash string) *models.InlineKeyboardMarkup {
	fingerprint := torrentFingerprint(hash)
	return &models.InlineKeyboardMarkup{
		InlineKeyboard: [][]models.InlineKeyboardButton{
			{
				{Text: "⚠️ Confirm delete", CallbackData: fmt.Sprintf("cmd:confirm-del:%d:%s", torrentID, fingerprint)},
				{Text: "Cancel", CallbackData: fmt.Sprintf("cmd:cancel:%d:%s", torrentID, fingerprint)},
			},
		},
	}
}
