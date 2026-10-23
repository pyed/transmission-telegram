package bot

import (
	"context"
	"time"

	"github.com/go-telegram/bot/models"
)

// liveUpdate repeatedly edits a sent message with fresh data.
func (b *Bot) liveUpdate(ctx context.Context, chatID int64, msgID int, updateFn func() string, finalFn func() string) {
	b.liveUpdateWithKeyboard(ctx, chatID, msgID, updateFn, finalFn, nil)
}

// liveUpdateWithKeyboard repeatedly edits a sent message with fresh data and preserves/updates an inline keyboard.
func (b *Bot) liveUpdateWithKeyboard(ctx context.Context, chatID int64, msgID int, updateFn func() string, finalFn func() string, markup models.ReplyMarkup) {
	if b.Config.NoLive {
		return
	}
	if msgID <= 0 || b.Config.Duration <= 0 {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	interval := b.Config.Interval
	if interval <= 0 {
		interval = 5 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for i := 0; i < b.Config.Duration; i++ {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		if text := updateFn(); text != "" {
			if !b.editLiveMessage(ctx, chatID, msgID, text, markup) {
				return
			}
		}
	}

	if finalFn != nil {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		if text := finalFn(); text != "" {
			b.editLiveMessage(ctx, chatID, msgID, text, markup)
		}
	}
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
