package bot

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/go-telegram/bot/models"
	"github.com/pyed/transmission"
)

// Every command name and alias of earlier versions still works.
func TestCommandsKeepTheirNamesAndAliases(t *testing.T) {
	b := &Bot{}
	b.registerCommands()
	for _, name := range []string{
		"list", "li", "ls", "head", "he", "tail", "ta", "downs", "dg", "seeding", "sd",
		"paused", "pa", "checking", "ch", "active", "ac", "errors", "er", "sort", "so",
		"trackers", "tr", "downloaddir", "dd", "add", "ad", "search", "se", "latest", "la",
		"info", "in", "stop", "sp", "start", "st", "check", "ck", "stats", "sa",
		"downlimit", "dl", "uplimit", "ul", "speed", "ss", "count", "co", "del", "rm",
		"deldata", "turtle", "alt", "tu", "free", "space", "disk", "reannounce", "ra",
		"help", "version", "ver",
	} {
		if _, ok := b.commands[name]; !ok {
			t.Errorf("command %q is no longer handled", name)
		}
	}
}

// No name or alias belongs to two commands.
func TestCommandNamesAreUnique(t *testing.T) {
	seen := make(map[string]string)
	for _, section := range commandTable() {
		for _, c := range section.commands {
			for _, name := range append([]string{c.name}, c.aliases...) {
				if other, taken := seen[name]; taken {
					t.Errorf("%q names both /%s and /%s", name, other, c.name)
				}
				seen[name] = c.name
			}
		}
	}
}

// Telegram takes menu commands of 1–32 lowercase letters, digits and
// underscores, with descriptions of 1–256 characters, at most 100 of them.
func TestMenuCommandsAreValid(t *testing.T) {
	valid := regexp.MustCompile(`^[a-z0-9_]{1,32}$`)
	menu := menuCommands()
	if len(menu) == 0 || len(menu) > 100 {
		t.Fatalf("menu has %d commands", len(menu))
	}
	for _, c := range menu {
		if !valid.MatchString(c.Command) {
			t.Errorf("menu command %q is not one Telegram accepts", c.Command)
		}
		if n := telegramLength(c.Description); n < 1 || n > 256 {
			t.Errorf("/%s has a %d-character description", c.Command, n)
		}
	}
}

// The bot registers its menu when it has none, or one this bot registered,
// from any version, and keeps a menu its owner set in BotFather.
func TestRegisterMenuKeepsTheOwnersMenu(t *testing.T) {
	answer := func(menu []models.BotCommand) string {
		body, err := json.Marshal(map[string]any{"ok": true, "result": menu})
		if err != nil {
			t.Fatal(err)
		}
		return string(body)
	}
	named := func(names ...string) []models.BotCommand {
		menu := make([]models.BotCommand, len(names))
		for i, name := range names {
			menu[i] = models.BotCommand{Command: name, Description: "something else"}
		}
		return menu
	}
	for _, tt := range []struct {
		name    string
		current []models.BotCommand
		wantSet bool
	}{
		{"no menu", nil, true},
		{"v2.0's", named(earlierMenus[0]...), true},
		{"v2.1's", named(earlierMenus[1]...), true},
		{"this version's, worded differently", named(commandNames(menuCommands())...), true},
		{"this version's", menuCommands(), false},
		{"the owner's", named("list", "speed"), false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ft := newFakeTelegram(t)
			b := newTestBot(t, ft.newBot(t), nil)
			ft.script("getMyCommands", answer(tt.current))
			b.registerMenu(context.Background())
			if set := ft.callCount("setMyCommands") == 1; set != tt.wantSet {
				t.Fatalf("menu set = %v, want %v", set, tt.wantSet)
			}
		})
	}
}

// Telegram sends a bare /start when someone opens a chat with the bot: it
// gets the commands, not "needs an argument", and starts nothing.
func TestBareStartListsTheCommands(t *testing.T) {
	ft := newFakeTelegram(t)
	fts := newFakeTransmission(t)
	client, err := transmission.New(fts.URL+"/transmission/rpc", "", "")
	if err != nil {
		t.Fatalf("failed to create transmission client: %v", err)
	}
	b := newTestBot(t, ft.newBot(t), client)

	b.start(context.Background(), req())
	texts := ft.sentTexts()
	if len(texts) != 1 || !strings.Contains(texts[0], "Torrent lists") || renderedLength(texts[0]) > maxMessageLength {
		t.Fatalf("expected the commands in one message, got %q", texts)
	}
	if got := fts.methodCount("torrent-start"); got != 0 {
		t.Fatalf("a bare /start started torrents (%d calls)", got)
	}
}

// /help names every command and alias, is valid HTML, and fits one message.
func TestHelpCoversEveryCommand(t *testing.T) {
	help := helpText()
	checkTelegramHTML(t, help)
	if n := renderedLength(help); n > maxMessageLength {
		t.Fatalf("help is %d characters; Telegram permits %d", n, maxMessageLength)
	}
	shown := plainText(help)
	for _, section := range commandTable() {
		if !strings.Contains(shown, section.title) {
			t.Errorf("help has no %q heading", section.title)
		}
		for _, c := range section.commands {
			if !strings.Contains(shown, "/"+c.name+" ") {
				t.Errorf("help does not mention /%s", c.name)
			}
			for _, alias := range c.aliases {
				if !strings.Contains(shown, "/"+alias) {
					t.Errorf("help does not mention /%s's alias /%s", c.name, alias)
				}
			}
		}
	}
}
