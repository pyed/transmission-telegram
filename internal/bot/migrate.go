package bot

import (
	"errors"

	tgbot "github.com/go-telegram/bot"
	"github.com/pyed/transmission-telegram/v2/internal/state"
)

// A group that becomes a supergroup becomes a new chat, with a new ID.
// Telegram refuses what is sent to the old one, naming the new one, so the
// bot moves what it remembers of the old chat there and sends there.

// migratedTo returns the chat that err says a request's chat became.
func migratedTo(err error) (int64, bool) {
	var migrate *tgbot.MigrateError
	if errors.As(err, &migrate) && migrate.MigrateToChatID != 0 {
		return int64(migrate.MigrateToChatID), true
	}
	return 0, false
}

// followMigration moves what the bot remembers of chat to the chat it
// became, when err says it became one, and returns the new chat.
func (b *Bot) followMigration(chat any, err error) (int64, bool) {
	to, ok := migratedTo(err)
	if !ok {
		return 0, false
	}
	if from, isID := chat.(int64); isID {
		b.migrateChat(from, to)
	}
	return to, true
}

// migrateChat moves what the bot remembers of chat from to chat to.
func (b *Bot) migrateChat(from, to int64) {
	var remembered bool
	b.State.Read(func(data *state.Data) {
		_, notify := data.Notify[from]
		_, sorted := data.Sorts[from]
		_, digest := data.Digest[from]
		_, status := data.Status[from]
		remembered = data.LastChat == from || notify || sorted || digest || status
	})
	if !remembered {
		return
	}
	b.Logger.Info("A group became a supergroup, which is a new chat; moving what the bot remembers of it there",
		"from_chat_id", from, "to_chat_id", to)
	if err := b.State.Update(func(data *state.Data) { data.MoveChat(from, to) }); err != nil {
		b.Logger.Warn("Could not save the move to the new chat", "error", err)
	}
}
