package bot

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/go-telegram/bot/models"
)

// liveView is a message being kept up to date; closing stop ends it, with
// a last edit unless quiet is set, and done is closed once it has ended.
type liveView struct {
	messageID int
	stop      chan struct{}
	done      chan struct{}
	quiet     atomic.Bool
}

// stopLiveWait bounds how long a press waits for a live view to end: one
// that is waiting for Transmission may take a while.
const stopLiveWait = 10 * time.Second

// startLive keeps messageID up to date in the background: update gives its
// text while it is live, and final its text once it no longer is. A chat
// has one live view at a time, which keeps the bot well within Telegram's
// limits on edits, so a new one ends the chat's last one, which then shows
// its final text.
func (b *Bot) startLive(ctx context.Context, chatID int64, messageID int, name string, update, final func() string, markup models.ReplyMarkup) {
	if b.Config.NoLive || messageID <= 0 || b.Config.Duration <= 0 {
		return
	}
	view := &liveView{messageID: messageID, stop: make(chan struct{}), done: make(chan struct{})}
	b.liveMu.Lock()
	if b.liveViews == nil {
		b.liveViews = make(map[int64]*liveView)
	}
	if previous := b.liveViews[chatID]; previous != nil {
		close(previous.stop)
	}
	b.liveViews[chatID] = view
	b.liveMu.Unlock()

	b.launch(ctx, "live "+name, func(ctx context.Context) {
		defer close(view.done)
		defer func() {
			b.liveMu.Lock()
			if b.liveViews[chatID] == view {
				delete(b.liveViews, chatID)
			}
			b.liveMu.Unlock()
		}()
		last := func() string {
			if view.quiet.Load() || final == nil {
				return ""
			}
			return final()
		}
		b.liveUpdate(ctx, view.stop, chatID, messageID, update, last, markup)
	})
}

// stopLive ends the live view of a chat's message, if it has one, without a
// last edit: a button pressed under it shows something else there. It waits
// for an edit the view has under way, which would otherwise land after what
// the press shows.
func (b *Bot) stopLive(chatID int64, messageID int) {
	b.liveMu.Lock()
	view := b.liveViews[chatID]
	if view == nil || view.messageID != messageID {
		b.liveMu.Unlock()
		return
	}
	view.quiet.Store(true)
	close(view.stop)
	delete(b.liveViews, chatID)
	b.liveMu.Unlock()

	timer := time.NewTimer(stopLiveWait)
	defer timer.Stop()
	select {
	case <-view.done:
	case <-timer.C:
	}
}

// liveUpdate edits a message every interval, Duration times, then once more
// with final's text, which it also shows at once when stop is closed.
func (b *Bot) liveUpdate(ctx context.Context, stop <-chan struct{}, chatID int64, msgID int, update, final func() string, markup models.ReplyMarkup) {
	interval := b.Config.Interval
	if interval <= 0 {
		interval = 5 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	finish := func() {
		if final == nil {
			return
		}
		if text := final(); text != "" {
			b.editLiveMessage(ctx, chatID, msgID, text, markup)
		}
	}
	for i := 0; i < b.Config.Duration; i++ {
		select {
		case <-ctx.Done():
			return
		case <-stop:
			finish()
			return
		case <-ticker.C:
		}

		if text := update(); text != "" {
			if !b.editLiveMessage(ctx, chatID, msgID, text, markup) {
				return
			}
		}
	}

	select {
	case <-ctx.Done():
		return
	case <-stop:
	case <-ticker.C:
	}
	finish()
}

func (b *Bot) editLiveMessage(ctx context.Context, chatID int64, msgID int, text string, markup models.ReplyMarkup) bool {
	err := b.edit(ctx, chatID, msgID, text, markup)
	if err == nil {
		return true
	}
	if ctx.Err() == nil {
		b.Logger.Warn("Stopping live update after edit failure", "error", err, "chat_id", chatID, "message_id", msgID)
	}
	return false
}

// fitBlocks joins blocks with sep, as many as fit one message: when some do
// not, the text ends saying how many were left out, so that a live view is
// always one message it can edit.
func fitBlocks(blocks []string, sep string) string {
	sepLength := renderedLength(sep)
	length := 0
	for i, block := range blocks {
		added := renderedLength(block)
		if i > 0 {
			added += sepLength
		}
		// What follows this block must still fit: a note of the rest.
		reserve := 0
		if rest := len(blocks) - i - 1; rest > 0 {
			reserve = sepLength + telegramLength(moreNote(rest))
		}
		if length+added+reserve > maxMessageLength {
			if i == 0 {
				return moreNote(len(blocks))
			}
			return strings.Join(blocks[:i], sep) + sep + moreNote(len(blocks)-i)
		}
		length += added
	}
	return strings.Join(blocks, sep)
}

func moreNote(n int) string {
	return fmt.Sprintf("…and %d more", n)
}
