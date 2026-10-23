package bot

import (
	"regexp"
	"strings"
	"testing"
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
