package bot

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/go-telegram/bot/models"
	"github.com/pyed/transmission"
	"github.com/pyed/transmission-telegram/v2/internal/redact"
)

// cardFields are the torrent fields a card shows.
var cardFields = []string{
	"id", "hashString", "name", "status", "error", "errorString", "percentDone", "metadataPercentComplete",
	"haveValid", "haveUnchecked", "sizeWhenDone", "rateDownload", "rateUpload", "uploadRatio", "eta",
	"downloadedEver", "uploadedEver", "addedDate", "downloadDir", "trackers", "labels",
	"peersConnected", "peersSendingToUs", "peersGettingFromUs",
}

// errNoTorrent is the answer for a torrent ID Transmission does not have.
var errNoTorrent = errors.New("no torrent has that ID")

// cardTorrent fetches what a card shows of one torrent.
func (b *Bot) cardTorrent(ctx context.Context, id int) (*transmission.Torrent, error) {
	torrents, err := b.Client.GetTorrentsByIDs(ctx, []int{id}, cardFields...)
	if err != nil {
		return nil, err
	}
	if len(torrents) == 0 {
		return nil, errNoTorrent
	}
	return torrents[0], nil
}

// torrentFingerprint is the start of a torrent's info-hash, which buttons
// carry with its ID: Transmission gives IDs anew when it restarts, so an old
// button could otherwise act on another torrent.
func torrentFingerprint(hash string) string {
	const fingerprintLength = 16
	hash = strings.ToLower(strings.TrimSpace(hash))
	if len(hash) > fingerprintLength {
		return hash[:fingerprintLength]
	}
	return hash
}

// cardKeyboard is a card's buttons, the same however the torrent changes,
// so that a live card keeps them: back returns to the list the card was
// opened from.
func cardKeyboard(t *transmission.Torrent, back bool) *models.InlineKeyboardMarkup {
	data := func(action string) string {
		return fmt.Sprintf("a:%s:%d:%s", action, t.ID, torrentFingerprint(t.HashString))
	}
	rows := [][]models.InlineKeyboardButton{
		{{Text: "⏸ Pause", CallbackData: data("stop")}, {Text: "▶ Resume", CallbackData: data("start")}, {Text: "🔄 Refresh", CallbackData: data("refresh")}},
		{{Text: "✔ Verify", CallbackData: data("verify")}, {Text: "📡 Reannounce", CallbackData: data("reannounce")}},
		{{Text: "🗑 Remove", CallbackData: data("remove")}, {Text: "💣 Remove + data", CallbackData: data("removedata")}},
	}
	if back {
		rows = append(rows, []models.InlineKeyboardButton{{Text: "⬅ Back", CallbackData: "back"}})
	}
	return &models.InlineKeyboardMarkup{InlineKeyboard: rows}
}

// removalKeyboard asks to confirm removing a torrent, with or without its
// data.
func removalKeyboard(t *transmission.Torrent, withData bool) *models.InlineKeyboardMarkup {
	fp := torrentFingerprint(t.HashString)
	yes := models.InlineKeyboardButton{Text: "⚠️ Yes, remove it", CallbackData: fmt.Sprintf("y:remove:%d:%s", t.ID, fp)}
	if withData {
		yes = models.InlineKeyboardButton{Text: "💣 Yes, delete its data", CallbackData: fmt.Sprintf("y:removedata:%d:%s", t.ID, fp)}
	}
	return &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{{
		yes, {Text: "Cancel", CallbackData: fmt.Sprintf("n:%d:%s", t.ID, fp)},
	}}}
}

// sendCard sends a torrent's card as a new message, with buttons, and keeps
// it up to date for a while.
func (b *Bot) sendCard(ctx context.Context, chatID int64, t *transmission.Torrent) {
	keyboard := cardKeyboard(t, false)
	messageID, _ := b.send(ctx, chatID, torrentCard(t, true), keyboard)
	b.liveCard(ctx, chatID, messageID, t.ID, keyboard)
}

// liveCard keeps a card up to date.
func (b *Bot) liveCard(ctx context.Context, chatID int64, messageID, id int, keyboard models.ReplyMarkup) {
	card := func(live bool) func() string {
		return func() string {
			t, err := b.cardTorrent(ctx, id)
			if err != nil {
				return "" // skip this update
			}
			return torrentCard(t, live)
		}
	}
	b.startLive(ctx, chatID, messageID, "torrent card", card(true), card(false), keyboard)
}

// cardAction carries out a card's button: a:<action>:<id>:<fingerprint>.
func (b *Bot) cardAction(ctx context.Context, p *press) {
	if len(p.args) != 3 {
		b.answerCallback(ctx, p.query.ID, "This button no longer works; send /info again", true)
		return
	}
	t, ok := b.pressedTorrent(ctx, p, p.args[1], p.args[2])
	if !ok {
		return
	}
	var err error
	var done string
	switch action := p.args[0]; action {
	case "stop":
		err, done = b.Client.StopTorrents(ctx, t.ID), "Paused"
	case "start":
		err, done = b.Client.StartTorrents(ctx, t.ID), "Resumed"
	case "verify":
		err, done = b.Client.VerifyTorrents(ctx, t.ID), "Verifying"
	case "reannounce":
		err, done = b.Client.ReannounceTorrents(ctx, t.ID), "Asked the trackers"
	case "refresh":
	case "remove", "removedata":
		withData := action == "removedata"
		question := "\n\n⚠️ <b>Remove this torrent?</b> Its data stays."
		if withData {
			question = "\n\n💣 <b>Remove this torrent and delete its data?</b> The files cannot be got back."
		}
		b.answerCallback(ctx, p.query.ID, "", false)
		b.editPressed(ctx, p, torrentCard(t, false)+question, removalKeyboard(t, withData))
		return
	default:
		b.answerCallback(ctx, p.query.ID, "This button no longer works; send /info again", true)
		return
	}
	if err != nil {
		b.answerCallback(ctx, p.query.ID, "Transmission failed: "+redact.Error(err), true)
		return
	}
	b.answerCallback(ctx, p.query.ID, done, false)
	b.refreshCard(ctx, p, t.ID)
}

// refreshCard shows a torrent's card, as it is now, in the pressed message.
func (b *Bot) refreshCard(ctx context.Context, p *press, id int) {
	t, err := b.cardTorrent(ctx, id)
	if err != nil {
		b.editPressed(ctx, p, "<b>info:</b> "+esc(redact.Error(err)), nil)
		return
	}
	b.editPressed(ctx, p, torrentCard(t, false), cardKeyboard(t, b.hasScreen(p)))
}

// confirmRemoval removes a torrent once its removal is confirmed:
// y:<remove|removedata>:<id>:<fingerprint>.
func (b *Bot) confirmRemoval(ctx context.Context, p *press) {
	if len(p.args) != 3 || (p.args[0] != "remove" && p.args[0] != "removedata") {
		b.answerCallback(ctx, p.query.ID, "This button no longer works; send /info again", true)
		return
	}
	t, ok := b.pressedTorrent(ctx, p, p.args[1], p.args[2])
	if !ok {
		return
	}
	withData := p.args[0] == "removedata"
	if err := b.Client.RemoveTorrents(ctx, withData, t.ID); err != nil {
		b.answerCallback(ctx, p.query.ID, "Transmission failed: "+redact.Error(err), true)
		return
	}
	done := fmt.Sprintf("🗑 <b>Removed:</b> <code>%d</code> %s", t.ID, esc(t.Name))
	if withData {
		done = fmt.Sprintf("💣 <b>Removed with its data:</b> <code>%d</code> %s", t.ID, esc(t.Name))
	}
	b.answerCallback(ctx, p.query.ID, "Removed", false)
	var markup models.ReplyMarkup
	if b.hasScreen(p) {
		markup = &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{{{Text: "⬅ Back", CallbackData: "back"}}}}
	}
	b.editPressed(ctx, p, done, markup)
}

// cancelRemoval goes back to the card: n:<id>:<fingerprint>.
func (b *Bot) cancelRemoval(ctx context.Context, p *press) {
	if len(p.args) != 2 {
		b.answerCallback(ctx, p.query.ID, "This button no longer works; send /info again", true)
		return
	}
	t, ok := b.pressedTorrent(ctx, p, p.args[0], p.args[1])
	if !ok {
		return
	}
	b.answerCallback(ctx, p.query.ID, "Not removed", false)
	b.editPressed(ctx, p, torrentCard(t, false), cardKeyboard(t, b.hasScreen(p)))
}
