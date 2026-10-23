package bot

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/go-telegram/bot/models"
	"github.com/pyed/transmission"
	"github.com/pyed/transmission-telegram/v2/internal/redact"
)

// fileFields are the torrent fields the files screen shows.
var fileFields = []string{"id", "hashString", "name", "files", "fileStats"}

// File states, as buttons name them.
const (
	fileSkip   = "skip"
	fileLow    = "low"
	fileNormal = "normal"
	fileHigh   = "high"
)

// fileState is whether a file is downloaded, and how soon.
func fileState(stat transmission.FileStat) string {
	switch {
	case !stat.Wanted:
		return fileSkip
	case stat.Priority == transmission.PriorityHigh:
		return fileHigh
	case stat.Priority == transmission.PriorityLow:
		return fileLow
	}
	return fileNormal
}

var fileIcons = map[string]string{fileSkip: "⬜", fileLow: "🔻", fileNormal: "✅", fileHigh: "⭐"}

// files lists a torrent's files, with buttons to skip or prioritize them.
func (b *Bot) files(ctx context.Context, r *request) {
	if len(r.args) != 1 {
		b.reply(ctx, r, "<b>files:</b> needs one torrent ID")
		return
	}
	id, err := strconv.Atoi(r.args[0])
	if err != nil {
		b.reply(ctx, r, "<b>files:</b> "+esc(r.args[0])+" is not a number")
		return
	}
	t, err := b.filesTorrent(ctx, id)
	switch {
	case errors.Is(err, errNoTorrent):
		b.reply(ctx, r, fmt.Sprintf("<b>files:</b> can't find a torrent with an ID of %d", id))
		return
	case err != nil:
		b.replyError(ctx, r, "files", err)
		return
	}
	text, keyboard := filesPage(t, 0)
	b.send(ctx, r.chatID, text, keyboard)
}

func (b *Bot) filesTorrent(ctx context.Context, id int) (*transmission.Torrent, error) {
	torrents, err := b.Client.GetTorrentsByIDs(ctx, []int{id}, fileFields...)
	if err != nil {
		return nil, err
	}
	if len(torrents) == 0 {
		return nil, errNoTorrent
	}
	return torrents[0], nil
}

// filesPage shows a page of a torrent's files, a line each, with a numbered
// button that opens a file, buttons for every file at once, and one back to
// the torrent's card.
func filesPage(t *transmission.Torrent, page int) (string, *models.InlineKeyboardMarkup) {
	ref := fmt.Sprintf("%d:%s", t.ID, torrentFingerprint(t.HashString))
	card := []models.InlineKeyboardButton{{Text: "⬅ Card", CallbackData: "c:" + ref}}
	heading := fmt.Sprintf("📂 <code>%d</code> <b>%s</b>", t.ID, esc(t.Name))
	if len(t.Files) == 0 || len(t.FileStats) != len(t.Files) {
		return heading + "\nNo files yet: Transmission is still fetching the torrent's metadata.",
			&models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{card}}
	}

	pages := (len(t.Files) + pageSize - 1) / pageSize
	page = max(0, min(page, pages-1))
	first, last := page*pageSize, min((page+1)*pageSize, len(t.Files))
	lines := []string{heading}
	var rows [][]models.InlineKeyboardButton
	for i := first; i < last; i++ {
		file := t.Files[i]
		lines = append(lines, fmt.Sprintf("<b>%d</b> %s %s · %s (%s)", i+1, fileIcons[fileState(t.FileStats[i])],
			esc(file.Name), size(uint64(max(file.Length, 0))), filePercent(file)))
		if (i-first)%buttonsPerRow == 0 {
			rows = append(rows, nil)
		}
		rows[len(rows)-1] = append(rows[len(rows)-1], models.InlineKeyboardButton{
			Text: strconv.Itoa(i + 1), CallbackData: fmt.Sprintf("fo:%s:%d", ref, i),
		})
	}
	if pages > 1 {
		lines = append(lines, "", fmt.Sprintf("<i>Page %d of %d · %d files</i>", page+1, pages, len(t.Files)))
		var nav []models.InlineKeyboardButton
		if page > 0 {
			nav = append(nav, models.InlineKeyboardButton{Text: "◀", CallbackData: fmt.Sprintf("fl:%s:%d", ref, page-1)})
		}
		if page < pages-1 {
			nav = append(nav, models.InlineKeyboardButton{Text: "▶", CallbackData: fmt.Sprintf("fl:%s:%d", ref, page+1)})
		}
		rows = append(rows, nav)
	}
	rows = append(rows, []models.InlineKeyboardButton{
		{Text: "⬜ Skip all", CallbackData: fmt.Sprintf("fa:%s:%s", ref, fileSkip)},
		{Text: "✅ Download all", CallbackData: fmt.Sprintf("fa:%s:%s", ref, fileNormal)},
	}, card)
	lines = append(lines, "", "⬜ skipped · 🔻 low · ✅ normal · ⭐ high priority. Tap a number to change a file.")
	return strings.Join(lines, "\n"), &models.InlineKeyboardMarkup{InlineKeyboard: rows}
}

func filePercent(file transmission.File) string {
	if file.Length <= 0 {
		return percent(1)
	}
	return percent(float64(file.BytesCompleted) / float64(file.Length))
}

// fileCard shows one file, with a button for each state and one back to
// its page.
func fileCard(t *transmission.Torrent, index int) (string, *models.InlineKeyboardMarkup) {
	ref := fmt.Sprintf("%d:%s", t.ID, torrentFingerprint(t.HashString))
	file, state := t.Files[index], fileState(t.FileStats[index])
	text := fmt.Sprintf("📂 <code>%d</code> <b>%s</b>\nFile %d of %d: <code>%s</code>\n%s · %s done · %s %s",
		t.ID, esc(t.Name), index+1, len(t.Files), esc(file.Name), size(uint64(max(file.Length, 0))), filePercent(file),
		fileIcons[state], state)
	set := func(label, to string) models.InlineKeyboardButton {
		return models.InlineKeyboardButton{Text: label, CallbackData: fmt.Sprintf("fs:%s:%d:%s", ref, index, to)}
	}
	return text, &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{
		{set("⬜ Skip", fileSkip), set("🔻 Low", fileLow), set("✅ Normal", fileNormal), set("⭐ High", fileHigh)},
		{{Text: "⬅ Files", CallbackData: fmt.Sprintf("fl:%s:%d", ref, index/pageSize)}},
	}}
}

// filesPress handles the files screen's buttons, each naming the torrent
// by ID and fingerprint: fl:<page> shows a page, fo:<file> opens a file,
// fs:<file>:<state> sets one file, fa:<state> sets every file, and c goes
// back to the card.
func (b *Bot) filesPress(ctx context.Context, p *press) {
	if len(p.args) < 2 {
		b.answerCallback(ctx, p.query.ID, "This button no longer works; send /files again", true)
		return
	}
	idArg, fingerprint, rest := p.args[0], p.args[1], p.args[2:]
	id, err := strconv.Atoi(idArg)
	if err != nil {
		b.answerCallback(ctx, p.query.ID, "Invalid torrent ID", false)
		return
	}
	if p.verb == "c" {
		t, ok := b.pressedTorrent(ctx, p, idArg, fingerprint)
		if ok {
			b.answerCallback(ctx, p.query.ID, "", false)
			b.editPressed(ctx, p, torrentCard(t, false), cardKeyboard(t, b.hasScreen(p)))
		}
		return
	}
	t, err := b.filesTorrent(ctx, id)
	switch {
	case errors.Is(err, errNoTorrent):
		b.answerCallback(ctx, p.query.ID, "Torrent no longer exists", true)
		return
	case err != nil:
		b.answerCallback(ctx, p.query.ID, "Transmission failed: "+redact.Error(err), true)
		return
	case torrentFingerprint(t.HashString) != fingerprint:
		b.answerCallback(ctx, p.query.ID, "Torrent changed; send /files again", true)
		return
	}
	number := func(arg string) (int, bool) {
		index, err := strconv.Atoi(arg)
		return index, err == nil && index >= 0 && index < len(t.Files) && len(t.FileStats) == len(t.Files)
	}

	switch {
	case p.verb == "fl" && len(rest) == 1:
		page, _ := strconv.Atoi(rest[0])
		b.answerCallback(ctx, p.query.ID, "", false)
		text, keyboard := filesPage(t, page)
		b.editPressed(ctx, p, text, keyboard)
	case p.verb == "fo" && len(rest) == 1:
		index, ok := number(rest[0])
		if !ok {
			b.answerCallback(ctx, p.query.ID, "That file is gone; send /files again", true)
			return
		}
		b.answerCallback(ctx, p.query.ID, "", false)
		text, keyboard := fileCard(t, index)
		b.editPressed(ctx, p, text, keyboard)
	case p.verb == "fs" && len(rest) == 2:
		index, ok := number(rest[0])
		if !ok || fileIcons[rest[1]] == "" {
			b.answerCallback(ctx, p.query.ID, "That file is gone; send /files again", true)
			return
		}
		if !b.setFiles(ctx, p, t.ID, []int{index}, rest[1]) {
			return
		}
		if t, err = b.filesTorrent(ctx, id); err == nil {
			text, keyboard := fileCard(t, index)
			b.editPressed(ctx, p, text, keyboard)
		}
	case p.verb == "fa" && len(rest) == 1 && (rest[0] == fileSkip || rest[0] == fileNormal):
		every := make([]int, len(t.Files))
		for i := range every {
			every[i] = i
		}
		if !b.setFiles(ctx, p, t.ID, every, rest[0]) {
			return
		}
		if t, err = b.filesTorrent(ctx, id); err == nil {
			text, keyboard := filesPage(t, 0)
			b.editPressed(ctx, p, text, keyboard)
		}
	default:
		b.answerCallback(ctx, p.query.ID, "This button no longer works; send /files again", true)
	}
}

// setFiles skips files, or downloads them at a priority, and answers the
// press; it returns false when Transmission failed.
func (b *Bot) setFiles(ctx context.Context, p *press, id int, files []int, state string) bool {
	var options transmission.TorrentSetOptions
	switch state {
	case fileSkip:
		options.FilesUnwanted = files
	case fileLow:
		options.FilesWanted, options.PriorityLow = files, files
	case fileHigh:
		options.FilesWanted, options.PriorityHigh = files, files
	default:
		options.FilesWanted, options.PriorityNormal = files, files
	}
	if err := b.Client.SetTorrent(ctx, []int{id}, options); err != nil {
		b.answerCallback(ctx, p.query.ID, "Transmission failed: "+redact.Error(err), true)
		return false
	}
	answer := "Downloading at " + state + " priority"
	if state == fileSkip {
		answer = "Skipped"
	}
	b.answerCallback(ctx, p.query.ID, answer, false)
	return true
}
