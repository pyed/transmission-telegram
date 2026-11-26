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
		if err := b.State.Update(func(data *state.Data) { chooseNotifications(data, r.place(), change) }); err != nil {
			b.Logger.Warn("Could not save notification choices; they last until the bot stops", "error", err)
		}
	}
	b.reply(ctx, r, b.notificationsText(r.place()))
}

// chooseNotifications sets the kinds of notification a chat, or topic,
// gets. The first to choose ends the default, where the chat and topic a
// master used last gets completions and outages; that one keeps them.
func chooseNotifications(data *state.Data, place state.Place, change func([]string) []string) {
	current := notify.KindsOf(data, place)
	if len(data.Notify) == 0 {
		data.Notify = make(map[state.Place][]string)
		if last := data.Last(); data.LastChat != 0 && last != place {
			data.Notify[last] = slices.Clone(notify.DefaultKinds)
		}
	}
	chosen := change(current)
	kinds := []string{}
	for _, kind := range notify.Kinds {
		if slices.Contains(chosen, kind) {
			kinds = append(kinds, kind)
		}
	}
	data.Notify[place] = kinds
}

// notificationsText shows the notifications a chat, or topic, gets.
func (b *Bot) notificationsText(place state.Place) string {
	var kinds []string
	var chosen bool
	var last state.Place
	b.State.Read(func(data *state.Data) {
		kinds = notify.KindsOf(data, place)
		chosen = len(data.Notify) > 0
		last = data.Last()
	})
	lines := []string{"<b>Notifications in this " + placeWord(place) + "</b>"}
	for _, kind := range notify.Kinds {
		mark := "⬜"
		if slices.Contains(kinds, kind) {
			mark = "✅"
		}
		lines = append(lines, mark+" <b>"+kind+"</b> — "+esc(notify.Describe(kind, b.Config.LowDisk)))
	}
	if !chosen {
		where := "another chat"
		switch {
		case last == place:
			where = "this one"
		case last.Chat == place.Chat:
			where = "another topic"
		}
		chat := "chat"
		if place.Topic != 0 || last.Topic != 0 {
			chat = "chat, or topic,"
		}
		lines = append(lines, "", "No chat has chosen yet, so completions and outages go to the "+chat+" a master used last: "+where+".")
	}
	return strings.Join(append(lines, notifyUsage), "\n")
}
