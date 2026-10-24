package bot

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/pyed/transmission"
	"github.com/pyed/transmission-telegram/v2/internal/redact"
	"github.com/pyed/transmission-telegram/v2/internal/state"
)

// A status message is one the bot pins in a chat and keeps up to date:
// speeds and limits, what Transmission transferred this session, torrents
// at work, and free space. The chats' status messages are saved in the
// state, so they stay up to date after a restart.

// statusInterval is how often status messages are brought up to date.
const statusInterval = time.Minute

// statusEndedNote is what a status message that is no longer updated is
// left saying, and what its Refresh button answers.
const statusEndedNote = "📊 This status is no longer updated. /status shows a new one."

func statusKeyboard() *models.InlineKeyboardMarkup {
	return &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{{{Text: "🔄 Refresh", CallbackData: "sr"}}}}
}

// timeNow is the time; tests set b.now.
func (b *Bot) timeNow() time.Time {
	if b.now != nil {
		return b.now()
	}
	return time.Now()
}

// status answers /status with a status message, pinned, in place of the
// chat's earlier one; /status off ends it.
func (b *Bot) status(ctx context.Context, r *request) {
	switch strings.ToLower(strings.Join(r.args, " ")) {
	case "":
	case "off":
		if !b.stopStatus(ctx, r.chatID, statusEndedNote) {
			b.reply(ctx, r, "<b>status:</b> this chat has no status message")
			return
		}
		b.reply(ctx, r, "The status message is no longer updated or pinned.")
		return
	default:
		b.reply(ctx, r, "<b>status:</b> use /status, or /status off")
		return
	}
	b.statusMu.Lock()
	defer b.statusMu.Unlock()
	b.stopStatusLocked(ctx, r.chatID, "📊 This status moved to a newer message.")
	messageID, err := b.send(ctx, r.chatID, b.renderStatus(ctx), statusKeyboard())
	if err != nil || messageID == 0 {
		return
	}
	if err := b.State.Update(func(data *state.Data) {
		if data.Status == nil {
			data.Status = make(map[int64]int)
		}
		data.Status[r.chatID] = messageID
	}); err != nil {
		b.Logger.Warn("Could not save the status message; it stays up to date until the bot stops", "error", err)
	}
	err = b.retryTelegram(ctx, func() error {
		_, err := b.API.PinChatMessage(ctx, &tgbot.PinChatMessageParams{ChatID: r.chatID, MessageID: messageID, DisableNotification: true})
		return err
	})
	if err != nil {
		b.reply(ctx, r, "<b>status:</b> it stays up to date, but could not be pinned: "+esc(redact.Error(err))+
			". In a group, the bot needs the right to pin messages.")
	}
}

// stopStatus ends a chat's status message, if it has one: it is unpinned
// and left saying note. It reports whether there was one.
func (b *Bot) stopStatus(ctx context.Context, chatID int64, note string) bool {
	b.statusMu.Lock()
	defer b.statusMu.Unlock()
	return b.stopStatusLocked(ctx, chatID, note)
}

func (b *Bot) stopStatusLocked(ctx context.Context, chatID int64, note string) bool {
	var messageID int
	var ok bool
	if err := b.State.Update(func(data *state.Data) {
		messageID, ok = data.Status[chatID]
		delete(data.Status, chatID)
	}); err != nil {
		b.Logger.Warn("Could not save the status message", "error", err)
	}
	if !ok {
		return false
	}
	noButtons := &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{}}
	if err := b.edit(ctx, chatID, messageID, note, noButtons); err != nil {
		b.Logger.Warn("Could not end the status message", "chat_id", chatID, "error", err)
	}
	err := b.retryTelegram(ctx, func() error {
		_, err := b.API.UnpinChatMessage(ctx, &tgbot.UnpinChatMessageParams{ChatID: chatID, MessageID: messageID})
		return err
	})
	if err != nil {
		b.Logger.Warn("Could not unpin the status message", "chat_id", chatID, "error", err)
	}
	return true
}

// watchStatus keeps status messages up to date until ctx ends, beginning
// with those saved before a restart.
func (b *Bot) watchStatus(ctx context.Context) {
	ticker := time.NewTicker(statusInterval)
	defer ticker.Stop()
	for {
		b.checkStatus(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// checkStatus brings every chat's status message up to date. With none, it
// asks Transmission nothing.
func (b *Bot) checkStatus(ctx context.Context) {
	var chats []int64
	b.State.Read(func(data *state.Data) { chats = slices.Sorted(maps.Keys(data.Status)) })
	if len(chats) == 0 {
		return
	}
	text := b.renderStatus(ctx)
	for _, chatID := range chats {
		b.updateStatus(ctx, chatID, text) // logs its own failure
	}
}

// updateStatus shows text in a chat's status message, if it still has one.
// When the message is gone, or the bot can no longer post in the chat, the
// chat's status ends.
func (b *Bot) updateStatus(ctx context.Context, chatID int64, text string) error {
	b.statusMu.Lock()
	defer b.statusMu.Unlock()
	var messageID int
	b.State.Read(func(data *state.Data) { messageID = data.Status[chatID] })
	if messageID == 0 {
		return nil
	}
	err := b.edit(ctx, chatID, messageID, text, statusKeyboard())
	switch {
	case err == nil:
	case statusGone(err):
		b.Logger.Info("Stopped updating a status message that is gone", "chat_id", chatID, "error", err)
		if saveErr := b.State.Update(func(data *state.Data) { delete(data.Status, chatID) }); saveErr != nil {
			b.Logger.Warn("Could not save the status message", "error", saveErr)
		}
	default:
		b.Logger.Warn("Could not update the status message", "chat_id", chatID, "error", err)
	}
	return err
}

// statusGone reports whether err says a status message can no longer be
// edited: it was deleted, or the bot can no longer post in its chat.
func statusGone(err error) bool {
	text := strings.ToLower(err.Error())
	return chatGone(err) || strings.Contains(text, "message to edit not found") ||
		strings.Contains(text, "message_id_invalid") || strings.Contains(text, "message can't be edited")
}

// refreshStatus brings a status message up to date at once: sr.
func (b *Bot) refreshStatus(ctx context.Context, p *press) {
	var messageID int
	b.State.Read(func(data *state.Data) { messageID = data.Status[p.chatID] })
	if messageID == 0 || messageID != p.messageID {
		b.answerCallback(ctx, p.query.ID, statusEndedNote, true)
		return
	}
	if err := b.updateStatus(ctx, p.chatID, b.renderStatus(ctx)); err != nil {
		b.answerCallback(ctx, p.query.ID, "Could not update it: "+redact.Error(err), true)
		return
	}
	b.answerCallback(ctx, p.query.ID, "Updated", false)
}

// statusFields are the torrent fields a status message counts torrents by.
var statusFields = []string{"id", "status", "error"}

// renderStatus describes Transmission now.
func (b *Bot) renderStatus(ctx context.Context) string {
	updated := "<i>Updated " + b.timeNow().Format("15:04") + "</i>"
	stats, err := b.Client.GetStats(ctx)
	var session *transmission.Session
	if err == nil {
		session, err = b.Client.GetSession(ctx)
	}
	var torrents transmission.Torrents
	if err == nil {
		torrents, err = b.Client.GetTorrentsByIDs(ctx, nil, statusFields...)
	}
	if err != nil {
		return "📊 <b>Transmission</b>\n🔴 Transmission is not answering: " + esc(redact.Error(err)) + "\n" + updated
	}

	// In turtle mode, its limits are the ones Transmission keeps to.
	limit := func(normal int64, enabled bool, turtle int64) string {
		switch {
		case session.AltSpeedEnabled:
			return fmt.Sprintf("🐢 limit %d KB/s", turtle)
		case enabled:
			return fmt.Sprintf("limit %d KB/s", normal)
		}
		return "no limit"
	}
	lines := []string{
		"📊 <b>Transmission</b>",
		fmt.Sprintf("↓ %s · %s · %s this session", rate(stats.DownloadSpeed),
			limit(session.SpeedLimitDown, session.SpeedLimitDownEnabled, session.AltSpeedDown), size(stats.CurrentStats.DownloadedBytes)),
		fmt.Sprintf("↑ %s · %s · %s this session", rate(stats.UploadSpeed),
			limit(session.SpeedLimitUp, session.SpeedLimitUpEnabled, session.AltSpeedUp), size(stats.CurrentStats.UploadedBytes)),
	}
	var downloading, seeding, errored int
	for _, t := range torrents {
		switch t.Status {
		case transmission.StatusDownloading:
			downloading++
		case transmission.StatusSeeding:
			seeding++
		}
		if t.Error != transmission.ErrorNone {
			errored++
		}
	}
	counts := fmt.Sprintf("Torrents: %d · %d downloading · %d seeding", len(torrents), downloading, seeding)
	if errored > 0 {
		counts += fmt.Sprintf(" · %d with errors", errored)
	}
	lines = append(lines, counts)
	if free, ok := b.freeSpace(ctx, session.DownloadDir); ok {
		lines = append(lines, "Free space: "+size(free))
	}
	return strings.Join(append(lines, updated), "\n")
}
