package bot

import (
	"context"
	"errors"
	"strconv"
	"strings"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/pyed/transmission"
	"github.com/pyed/transmission-telegram/v2/internal/redact"
)

// press is a button press: the query, the message the button is under, and
// its callback data, split at colons into a verb and its arguments.
type press struct {
	query     *models.CallbackQuery
	chatID    int64
	messageID int
	verb      string
	args      []string
}

func newPress(cb *models.CallbackQuery) *press {
	p := &press{query: cb}
	switch {
	case cb.Message.Message != nil:
		p.chatID, p.messageID = cb.Message.Message.Chat.ID, cb.Message.Message.ID
	case cb.Message.InaccessibleMessage != nil:
		p.chatID, p.messageID = cb.Message.InaccessibleMessage.Chat.ID, cb.Message.InaccessibleMessage.MessageID
	}
	parts := strings.Split(cb.Data, ":")
	p.verb, p.args = parts[0], parts[1:]
	return p
}

// handleCallbackQuery handles a button press. Only masters may press, and
// every press is answered, which ends Telegram's waiting animation.
// Callback data is <verb>:<arguments>; torrents are named by ID and
// fingerprint (torrentFingerprint), so a button never acts on another
// torrent that came to have its ID.
func (b *Bot) handleCallbackQuery(ctx context.Context, cb *models.CallbackQuery) {
	if !b.Config.Masters.Contains(cb.From.Username, cb.From.ID) {
		b.answerCallback(ctx, cb.ID, "Unauthorized", true)
		return
	}
	p := newPress(cb)
	// A pressed card is not edited by its live view any more, which would
	// undo what the press shows, such as a question to confirm.
	b.stopLive(p.chatID, p.messageID)
	switch p.verb {
	case "a":
		b.cardAction(ctx, p)
	case "y":
		b.confirmRemoval(ctx, p)
	case "n":
		b.cancelRemoval(ctx, p)
	case "t":
		b.openCard(ctx, p)
	case "pg":
		b.turnPage(ctx, p)
	case "back":
		b.goBack(ctx, p)
	case "dd":
		b.confirmDeletion(ctx, p)
	case "fl", "fo", "fs", "fa", "c":
		b.filesPress(ctx, p)
	case "sr":
		b.refreshStatus(ctx, p)
	case "cmd":
		b.legacyPress(ctx, p)
	default:
		b.answerCallback(ctx, cb.ID, "This button no longer works; send the command again", true)
	}
}

// legacyPress handles the buttons of v2.0 and v2.1 cards:
// cmd:<stop|start|del|confirm-del|cancel>:<id>[:<fingerprint>]. v2.0's
// buttons have no fingerprint; its pause and resume buttons still work, but
// its delete buttons have expired, as they could act on another torrent.
func (b *Bot) legacyPress(ctx context.Context, p *press) {
	if len(p.args) == 2 && (p.args[0] == "stop" || p.args[0] == "start") {
		id, err := strconv.Atoi(p.args[1])
		if err != nil {
			b.answerCallback(ctx, p.query.ID, "Invalid torrent ID", false)
			return
		}
		t, err := b.cardTorrent(ctx, id)
		if err != nil {
			b.answerCallback(ctx, p.query.ID, "Torrent no longer exists", true)
			return
		}
		p.args = append(p.args, torrentFingerprint(t.HashString))
	}
	if len(p.args) != 3 {
		b.answerCallback(ctx, p.query.ID, "Delete button expired; run /info again", true)
		return
	}
	action, id, fp := p.args[0], p.args[1], p.args[2]
	switch action {
	case "stop", "start":
		p.args = []string{action, id, fp}
		b.cardAction(ctx, p)
	case "del":
		p.args = []string{"remove", id, fp}
		b.cardAction(ctx, p)
	case "confirm-del":
		p.args = []string{"remove", id, fp}
		b.confirmRemoval(ctx, p)
	case "cancel":
		p.args = []string{id, fp}
		b.cancelRemoval(ctx, p)
	default:
		b.answerCallback(ctx, p.query.ID, "This button no longer works; send /info again", true)
	}
}

// openCard shows a torrent's card: t:<id>:<fingerprint>. Pressed under a
// list, the list becomes the card, with a button back; pressed under a
// notification, the card comes as a new message.
func (b *Bot) openCard(ctx context.Context, p *press) {
	if len(p.args) != 2 {
		b.answerCallback(ctx, p.query.ID, "This button no longer works", true)
		return
	}
	t, ok := b.pressedTorrent(ctx, p, p.args[0], p.args[1])
	if !ok {
		return
	}
	b.answerCallback(ctx, p.query.ID, "", false)
	if b.hasScreen(p) {
		b.editPressed(ctx, p, torrentCard(t, false), cardKeyboard(t, true))
		return
	}
	b.sendCard(ctx, p.chatID, t)
}

// pressedTorrent finds the torrent a button names. When it is gone, or its
// ID now names another torrent, it answers the press itself and returns
// false.
func (b *Bot) pressedTorrent(ctx context.Context, p *press, idArg, fingerprint string) (*transmission.Torrent, bool) {
	id, err := strconv.Atoi(idArg)
	if err != nil {
		b.answerCallback(ctx, p.query.ID, "Invalid torrent ID", false)
		return nil, false
	}
	t, err := b.cardTorrent(ctx, id)
	switch {
	case errors.Is(err, errNoTorrent):
		b.answerCallback(ctx, p.query.ID, "Torrent no longer exists", true)
		return nil, false
	case err != nil:
		b.answerCallback(ctx, p.query.ID, "Transmission failed: "+redact.Error(err), true)
		return nil, false
	case torrentFingerprint(t.HashString) != fingerprint:
		b.answerCallback(ctx, p.query.ID, "Torrent changed; run /info again", true)
		return nil, false
	}
	return t, true
}

// editPressed shows text and markup in the pressed message.
func (b *Bot) editPressed(ctx context.Context, p *press, text string, markup models.ReplyMarkup) {
	if p.messageID == 0 {
		return
	}
	if markup == nil {
		markup = &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{}}
	}
	if err := b.edit(ctx, p.chatID, p.messageID, text, markup); err != nil {
		b.Logger.Warn("Could not update the pressed message", "chat_id", p.chatID, "message_id", p.messageID, "error", err)
	}
}

func (b *Bot) answerCallback(ctx context.Context, callbackID, text string, alert bool) {
	if _, err := b.API.AnswerCallbackQuery(ctx, &tgbot.AnswerCallbackQueryParams{
		CallbackQueryID: callbackID,
		Text:            text,
		ShowAlert:       alert,
	}); err != nil {
		b.Logger.Warn("AnswerCallbackQuery failed", "error", err)
	}
}
