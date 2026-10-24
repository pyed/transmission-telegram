package bot

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// updateREADME rewrites the README's list of commands from the command
// table: go test ./internal/bot -run TestREADMEListsEveryCommand -update
var updateREADME = flag.Bool("update", false, "rewrite README.md's list of commands from the command table")

// The README's list of commands lies between these lines.
const (
	readmeBegin = "<!-- BEGIN COMMANDS: made from internal/bot/commands.go; go test ./internal/bot -run TestREADMEListsEveryCommand -update rewrites it -->\n"
	readmeEnd   = "<!-- END COMMANDS -->\n"
)

// readmeCommands is the README's list of commands: a table for each section
// of the command table, and the command menu as BotFather takes it.
func readmeCommands() string {
	var text strings.Builder
	for _, section := range commandTable() {
		fmt.Fprintf(&text, "\n**%s**\n\n| Command | Aliases | What it does |\n|---|---|---|\n", section.title)
		for _, c := range section.commands {
			usage := "/" + c.name
			if c.args != "" {
				usage += " " + c.args
			}
			aliases := "—"
			if len(c.aliases) > 0 {
				aliases = "`/" + strings.Join(c.aliases, "`, `/") + "`"
			}
			// A table cell takes a | escaped, even in code.
			fmt.Fprintf(&text, "| `%s` | %s | %s |\n", strings.ReplaceAll(usage, "|", `\|`), aliases, c.help)
		}
	}
	text.WriteString("\n<details>\n<summary>The command menu, for BotFather</summary>\n\n" +
		"The bot sets Telegram's command menu itself, and keeps one set in BotFather. " +
		"To set it yourself, send `/setcommands` to [@BotFather](https://t.me/BotFather), choose the bot, and paste:\n\n```text\n")
	for _, c := range menuCommands() {
		fmt.Fprintf(&text, "%s - %s\n", c.Command, c.Description)
	}
	text.WriteString("```\n\n</details>\n\n")
	return text.String()
}

// The README lists every command as the command table has it.
func TestREADMEListsEveryCommand(t *testing.T) {
	path := filepath.Join("..", "..", "README.md")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	readme := strings.ReplaceAll(string(content), "\r\n", "\n")
	begin, end := strings.Index(readme, readmeBegin), strings.Index(readme, readmeEnd)
	if begin < 0 || end < begin {
		t.Fatalf("README.md lacks the lines\n%s\nand\n%s", readmeBegin, readmeEnd)
	}
	begin += len(readmeBegin)
	want := readmeCommands()
	if readme[begin:end] == want {
		return
	}
	if !*updateREADME {
		t.Fatal("README.md's list of commands is not the command table's; rewrite it with\n" +
			"go test ./internal/bot -run TestREADMEListsEveryCommand -update")
	}
	updated := readme[:begin] + want + readme[end:]
	if strings.Contains(string(content), "\r\n") {
		updated = strings.ReplaceAll(updated, "\n", "\r\n")
	}
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		t.Fatal(err)
	}
}
