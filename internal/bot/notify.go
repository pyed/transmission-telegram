package bot

import (
	"context"
	"slices"
	"strings"

	"github.com/pyed/transmission-telegram/v2/internal/notify"
	"github.com/pyed/transmission-telegram/v2/internal/state"
)

const notifyUsage = "Change them with /notify on, /notify off, or /notify &lt;kind&gt; on|off, as in /notify errors on."

// notify shows which notifications this chat gets, and changes them: on or
// off for every kind, or a kind on or off.
func (b *Bot) notify(ctx context.Context, r *request) {
	args := make([]string, len(r.args))
	for i, arg := range r.args {
		args[i] = strings.ToLower(arg)
	}
	var change func([]string) []string
	switch {
	case len(args) == 0:
	case len(args) == 1 && args[0] == "on":
		change = func([]string) []string { return slices.Clone(notify.Kinds) }
	case len(args) == 1 && args[0] == "off":
		change = func([]string) []string { return []string{} }
	case len(args) == 2 && slices.Contains(notify.Kinds, args[0]) && (args[1] == "on" || args[1] == "off"):
		kind, on := args[0], args[1] == "on"
		change = func(kinds []string) []string {
			kinds = slices.DeleteFunc(kinds, func(k string) bool { return k == kind })
			if on {
				kinds = append(kinds, kind)
			}
			return kinds
		}
	default:
		b.reply(ctx, r, "<b>notify:</b> kinds are "+strings.Join(notify.Kinds, ", ")+".\n"+notifyUsage)
		return
	}
	if change != nil {
		if err := b.State.Update(func(data *state.Data) { chooseNotifications(data, r.chatID, change) }); err != nil {
			b.Logger.Warn("Could not save notification choices; they last until the bot stops", "error", err)
		}
	}
	b.reply(ctx, r, b.notificationsText(r.chatID))
}

// chooseNotifications sets the kinds of notification chatID gets. The first
// chat to choose ends the default, where the chat a master used last gets
// completions and outages; that chat keeps them.
func chooseNotifications(data *state.Data, chatID int64, change func([]string) []string) {
	current := notify.KindsOf(data, chatID)
	if len(data.Notify) == 0 {
		data.Notify = make(map[int64][]string)
		if data.LastChat != 0 && data.LastChat != chatID {
			data.Notify[data.LastChat] = slices.Clone(notify.DefaultKinds)
		}
	}
	chosen := change(current)
	kinds := []string{}
	for _, kind := range notify.Kinds {
		if slices.Contains(chosen, kind) {
			kinds = append(kinds, kind)
		}
	}
	data.Notify[chatID] = kinds
}

// notificationsText shows the notifications chatID gets.
func (b *Bot) notificationsText(chatID int64) string {
	var kinds []string
	var chosen bool
	var lastChat int64
	b.State.Read(func(data *state.Data) {
		kinds = notify.KindsOf(data, chatID)
		chosen = len(data.Notify) > 0
		lastChat = data.LastChat
	})
	lines := []string{"<b>Notifications in this chat</b>"}
	for _, kind := range notify.Kinds {
		mark := "⬜"
		if slices.Contains(kinds, kind) {
			mark = "✅"
		}
		lines = append(lines, mark+" <b>"+kind+"</b> — "+esc(notify.Describe(kind, b.Config.LowDisk)))
	}
	if !chosen {
		where := "another chat"
		if lastChat == chatID {
			where = "this one"
		}
		lines = append(lines, "", "No chat has chosen yet, so completions and outages go to the chat a master used last: "+where+".")
	}
	return strings.Join(append(lines, notifyUsage), "\n")
}
