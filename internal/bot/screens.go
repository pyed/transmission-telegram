package bot

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/go-telegram/bot/models"
	"github.com/pyed/transmission"
)

const (
	// pageSize is how many torrents a page of a list shows.
	pageSize = 10
	// maxScreens is how many list messages the bot remembers, so that their
	// page and Back buttons work; older ones answer that they expired.
	maxScreens = 500
	// buttonsPerRow is how many torrent buttons a row of a list holds.
	buttonsPerRow = 5
)

// screen is a list message the bot remembers: the command that made it,
// which it runs again for fresh pages, and the page it shows.
type screen struct {
	command string
	args    []string
	page    int
}

type screenKey struct {
	chatID    int64
	messageID int
}

// screenStore remembers the last maxScreens list messages.
type screenStore struct {
	screens map[screenKey]*screen
	order   []screenKey
}

func (s *screenStore) put(key screenKey, sc *screen) {
	if s.screens == nil {
		s.screens = make(map[screenKey]*screen)
	}
	if _, known := s.screens[key]; !known {
		s.order = append(s.order, key)
		if len(s.order) > maxScreens {
			delete(s.screens, s.order[0])
			s.order = s.order[1:]
		}
	}
	s.screens[key] = sc
}

func (s *screenStore) get(key screenKey) (screen, bool) {
	sc, ok := s.screens[key]
	if !ok {
		return screen{}, false
	}
	return *sc, true
}

// listing is what a list command shows: its torrents, how each shows, and
// the reply when there are none.
type listing struct {
	torrents transmission.Torrents
	line     func(*transmission.Torrent) string
	none     string
}

// replyList replies with the first page of a list command's torrents, with
// buttons, and remembers the message for its page and Back buttons.
func (b *Bot) replyList(ctx context.Context, r *request, command string) {
	l, err := b.listing(ctx, r.chatID, command, r.args)
	if err != nil {
		b.replyError(ctx, r, command, err)
		return
	}
	if len(l.torrents) == 0 {
		b.reply(ctx, r, l.none)
		return
	}
	text, keyboard := listPage(l, 0)
	messageID, _ := b.send(ctx, r.chatID, text, keyboard)
	if messageID != 0 {
		b.rememberScreen(screenKey{r.chatID, messageID}, &screen{command: command, args: r.args})
	}
}

func (b *Bot) rememberScreen(key screenKey, sc *screen) {
	b.screensMu.Lock()
	defer b.screensMu.Unlock()
	b.screens.put(key, sc)
}

func (b *Bot) screenOf(p *press) (screen, bool) {
	b.screensMu.Lock()
	defer b.screensMu.Unlock()
	return b.screens.get(screenKey{p.chatID, p.messageID})
}

// hasScreen reports whether the pressed message is a list the bot
// remembers, which its cards can go back to.
func (b *Bot) hasScreen(p *press) bool {
	_, ok := b.screenOf(p)
	return ok
}

// listPage shows one page of a listing: its torrents a line each, and a
// button for each, with buttons to move between pages when there are more.
func listPage(l listing, page int) (string, *models.InlineKeyboardMarkup) {
	pages := (len(l.torrents) + pageSize - 1) / pageSize
	page = max(0, min(page, pages-1))
	shown := l.torrents[page*pageSize : min((page+1)*pageSize, len(l.torrents))]

	line := l.line
	if line == nil {
		line = torrentLine
	}
	lines := make([]string, 0, len(shown)+2)
	var buttons [][]models.InlineKeyboardButton
	for i, t := range shown {
		lines = append(lines, line(t))
		if i%buttonsPerRow == 0 {
			buttons = append(buttons, nil)
		}
		buttons[len(buttons)-1] = append(buttons[len(buttons)-1], models.InlineKeyboardButton{
			Text:         strconv.Itoa(t.ID),
			CallbackData: fmt.Sprintf("t:%d:%s", t.ID, torrentFingerprint(t.HashString)),
		})
	}
	if pages > 1 {
		lines = append(lines, "", fmt.Sprintf("<i>Page %d of %d · %d torrents</i>", page+1, pages, len(l.torrents)))
		var nav []models.InlineKeyboardButton
		if page > 0 {
			nav = append(nav, models.InlineKeyboardButton{Text: "◀", CallbackData: fmt.Sprintf("pg:%d", page-1)})
		}
		if page < pages-1 {
			nav = append(nav, models.InlineKeyboardButton{Text: "▶", CallbackData: fmt.Sprintf("pg:%d", page+1)})
		}
		buttons = append(buttons, nav)
	}
	return strings.Join(lines, "\n"), &models.InlineKeyboardMarkup{InlineKeyboard: buttons}
}

// turnPage shows another page of a remembered list: pg:<page>.
func (b *Bot) turnPage(ctx context.Context, p *press) {
	if len(p.args) != 1 {
		b.answerCallback(ctx, p.query.ID, "This button no longer works", true)
		return
	}
	page, err := strconv.Atoi(p.args[0])
	if err != nil {
		b.answerCallback(ctx, p.query.ID, "This button no longer works", true)
		return
	}
	b.showScreen(ctx, p, page)
}

// goBack shows the list a card was opened from, at the page it was on.
func (b *Bot) goBack(ctx context.Context, p *press) {
	sc, ok := b.screenOf(p)
	if !ok {
		b.answerCallback(ctx, p.query.ID, "This list is too old; send the command again", true)
		return
	}
	b.showScreen(ctx, p, sc.page)
}

// showScreen shows a page of the pressed list, made afresh.
func (b *Bot) showScreen(ctx context.Context, p *press, page int) {
	sc, ok := b.screenOf(p)
	if !ok {
		b.answerCallback(ctx, p.query.ID, "This list is too old; send the command again", true)
		return
	}
	l, err := b.listing(ctx, p.chatID, sc.command, sc.args)
	if err != nil {
		b.answerCallback(ctx, p.query.ID, "Transmission failed; try again", true)
		return
	}
	b.answerCallback(ctx, p.query.ID, "", false)
	if len(l.torrents) == 0 {
		b.editPressed(ctx, p, l.none, nil)
		return
	}
	pages := (len(l.torrents) + pageSize - 1) / pageSize
	sc.page = max(0, min(page, pages-1))
	b.rememberScreen(screenKey{p.chatID, p.messageID}, &sc)
	text, keyboard := listPage(l, sc.page)
	b.editPressed(ctx, p, text, keyboard)
}
