package bot

import (
	"context"
	"fmt"
	"slices"
	"strings"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/pyed/transmission-telegram/v2/internal/state"
)

// command is one of the bot's commands.
type command struct {
	name    string
	aliases []string
	args    string // how its arguments are written in /help, if it takes any
	help    string // what it does, for /help and Telegram's command menu
	run     func(*Bot, context.Context, *request)
}

// commandSection is a heading of /help and the commands under it.
type commandSection struct {
	title    string
	commands []command
}

// commandTable is every command, in the order /help and the command menu
// show them. It is the one list: dispatch, /help, and the menu come from it.
// It is a function, not a variable, because /help reads it.
func commandTable() []commandSection {
	return []commandSection{
		{"Torrent lists", []command{
			{"list", []string{"li", "ls"}, "[tracker]", "List torrents, or only those of a tracker", (*Bot).list},
			{"head", []string{"he"}, "[n]", "Show the first n torrents (5), updating live", (*Bot).head},
			{"tail", []string{"ta"}, "[n]", "Show the last n torrents (5), updating live", (*Bot).tail},
			{"active", []string{"ac"}, "", "Show the torrents transferring now, updating live", (*Bot).active},
			{"downs", []string{"dg"}, "", "List downloading and queued torrents", (*Bot).downs},
			{"seeding", []string{"sd"}, "", "List seeding and queued torrents", (*Bot).seeding},
			{"paused", []string{"pa"}, "", "List stopped torrents", (*Bot).paused},
			{"checking", []string{"ch"}, "", "List torrents being verified", (*Bot).checking},
			{"errors", []string{"er"}, "", "List torrents with errors, and why", (*Bot).errors},
			{"latest", []string{"la"}, "[n]", "List the n newest torrents (5)", (*Bot).latest},
			{"search", []string{"se"}, "<words>", "Find torrents by name", (*Bot).search},
			{"info", []string{"in"}, "<id…>", "Show torrents' details, with buttons", (*Bot).info},
			{"files", []string{"fi"}, "<id>", "List a torrent's files, to skip them or change their priority", (*Bot).files},
		}},
		{"Torrent control", []command{
			{"add", []string{"ad"}, "<link…>", "Add torrents from links or magnets; or send a .torrent file", (*Bot).add},
			{"stop", []string{"sp"}, "<id…|all>", "Pause torrents", (*Bot).stop},
			{"start", []string{"st"}, "<id…|all>", "Resume torrents", (*Bot).start},
			{"check", []string{"ck"}, "<id…|all>", "Verify torrents' data", (*Bot).check},
			{"reannounce", []string{"ra"}, "<id…|all>", "Ask trackers for peers now", (*Bot).reannounce},
			{"del", []string{"rm"}, "<id…>", "Remove torrents and keep their data", (*Bot).del},
			{"deldata", nil, "<id…> [confirm]", "Remove torrents and delete their data, after asking", (*Bot).deldata},
		}},
		{"Session and settings", []command{
			{"speed", []string{"ss"}, "", "Show the current speeds, updating live", (*Bot).speed},
			{"stats", []string{"sa"}, "", "Show session and total statistics", (*Bot).stats},
			{"count", []string{"co"}, "", "Count torrents in each state", (*Bot).count},
			{"trackers", []string{"tr"}, "", "Count torrents per tracker", (*Bot).trackers},
			{"sort", []string{"so"}, "[rev] <method>", "Choose the order of lists", (*Bot).sort},
			{"turtle", []string{"alt", "tu"}, "[on|off]", "Switch the alternative speed limits", (*Bot).turtle},
			{"free", []string{"space", "disk"}, "[path]", "Show free disk space", (*Bot).free},
			{"downlimit", []string{"dl"}, "[KB/s|off]", "Show or set the global download limit; 0 or off removes it", (*Bot).downlimit},
			{"uplimit", []string{"ul"}, "[KB/s|off]", "Show or set the global upload limit; 0 or off removes it", (*Bot).uplimit},
			{"downloaddir", []string{"dd"}, "<path>", "Set the default download folder", (*Bot).downloaddir},
			{"notify", nil, "[on|off|<kind> on|off]", "Choose the notifications this chat gets", (*Bot).notify},
			{"whoami", nil, "", "Show your Telegram user ID and this chat's ID", (*Bot).whoami},
			{"version", []string{"ver"}, "", "Show Transmission's and the bot's versions", (*Bot).version},
			{"help", nil, "", "List the commands", (*Bot).help},
		}},
	}
}

// registerCommands builds the dispatch map from the command table, by name
// and by alias.
func (b *Bot) registerCommands() {
	b.commands = make(map[string]commandFunc)
	for _, section := range commandTable() {
		for _, c := range section.commands {
			run := c.run
			handler := func(ctx context.Context, r *request) { run(b, ctx, r) }
			for _, name := range append([]string{c.name}, c.aliases...) {
				b.commands[name] = handler
			}
		}
	}
}

// helpText is the reply to /help.
func helpText() string {
	var text strings.Builder
	for i, section := range commandTable() {
		if i > 0 {
			text.WriteString("\n")
		}
		fmt.Fprintf(&text, "<b>%s</b>\n", esc(section.title))
		for _, c := range section.commands {
			text.WriteString("/" + c.name)
			if c.args != "" {
				text.WriteString(" " + esc(c.args))
			}
			text.WriteString(" — " + esc(c.help))
			if len(c.aliases) > 0 {
				text.WriteString(" (/" + strings.Join(c.aliases, ", /") + ")")
			}
			text.WriteString("\n")
		}
	}
	text.WriteString("\nOnly authorized users are accepted. In groups, commands may carry the bot's name, as in /list@BotName.\n" +
		"Be careful with /deldata: it deletes downloaded files.\n" +
		"Report issues: https://github.com/pyed/transmission-telegram")
	return text.String()
}

func (b *Bot) help(ctx context.Context, r *request) {
	b.reply(ctx, r, helpText())
}

// menuCommands is the command menu Telegram shows when "/" is typed.
func menuCommands() []models.BotCommand {
	var menu []models.BotCommand
	for _, section := range commandTable() {
		for _, c := range section.commands {
			menu = append(menu, models.BotCommand{Command: c.name, Description: c.help})
		}
	}
	return menu
}

// earlierMenus are the command names of the menus earlier versions
// registered: v2.0 and v2.1.
var earlierMenus = [][]string{
	{"list", "head", "tail", "active", "downs", "seeding", "paused", "checking", "errors", "speed", "stats",
		"free", "turtle", "count", "help", "version"},
	{"list", "head", "tail", "active", "downs", "seeding", "paused", "checking", "errors", "latest", "search",
		"info", "add", "stop", "start", "check", "del", "deldata", "speed", "stats", "free", "turtle",
		"reannounce", "downlimit", "uplimit", "downloaddir", "sort", "trackers", "count", "help", "version"},
}

// registerMenu gives the bot its command menu, so that typing / in a chat
// lists the commands: when it has none, or has one this bot registered,
// from this version or an earlier one. A menu with other commands was set
// in BotFather by the owner, and is kept.
func (b *Bot) registerMenu(ctx context.Context) {
	current, err := b.API.GetMyCommands(ctx, &tgbot.GetMyCommandsParams{})
	if err != nil {
		b.Logger.Warn("Could not read the command menu", "error", err)
		return
	}
	menu := menuCommands()
	if slices.Equal(current, menu) {
		b.rememberMenu(menu)
		return
	}
	var registered []string
	b.State.Read(func(data *state.Data) { registered = data.Menu })
	names := commandNames(current)
	ours := len(current) == 0 || slices.Equal(names, commandNames(menu)) || slices.Equal(names, registered) ||
		slices.ContainsFunc(earlierMenus, func(earlier []string) bool { return slices.Equal(names, earlier) })
	if !ours {
		b.Logger.Info("Keeping the command menu set in BotFather")
		return
	}
	if _, err := b.API.SetMyCommands(ctx, &tgbot.SetMyCommandsParams{Commands: menu}); err != nil {
		b.Logger.Warn("Could not set the command menu", "error", err)
		return
	}
	b.rememberMenu(menu)
	b.Logger.Info("Registered the command menu with Telegram")
}

// rememberMenu saves the commands of the menu the bot registered, so that a
// later version, whose commands differ, can tell it from one set in
// BotFather.
func (b *Bot) rememberMenu(menu []models.BotCommand) {
	names := commandNames(menu)
	var known bool
	b.State.Read(func(data *state.Data) { known = slices.Equal(data.Menu, names) })
	if known {
		return
	}
	if err := b.State.Update(func(data *state.Data) { data.Menu = names }); err != nil {
		b.Logger.Warn("Could not save the command menu", "error", err)
	}
}

func commandNames(menu []models.BotCommand) []string {
	names := make([]string, len(menu))
	for i, command := range menu {
		names[i] = command.Command
	}
	return names
}
