package bot

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	tgbot "github.com/go-telegram/bot"
	"github.com/pyed/transmission"
)

// add adds torrents by URL or magnet link.
func (b *Bot) add(ctx context.Context, r *request) {
	if len(r.args) == 0 {
		b.reply(ctx, r, "<b>add:</b> needs at least one URL")
		return
	}

	for _, link := range r.args {
		torrent, duplicate, err := b.addTorrent(ctx, transmission.AddTorrentOptions{Filename: &link})
		if err != nil {
			b.replyError(ctx, r, "add", err)
			continue
		}
		if duplicate {
			b.reply(ctx, r, alreadyAdded(torrent))
			continue
		}

		added := fmt.Sprintf("<b>Added:</b> <code>%d</code> %s", torrent.ID, esc(torrent.Name))
		if info, err := b.Client.GetTorrent(torrent.ID, ctx); err == nil && info.SizeWhenDone > 0 {
			added += " (" + size(info.SizeWhenDone) + ")"
		}
		b.reply(ctx, r, added)
	}
}

// addResult is torrent-add's answer: the torrent Transmission added, or
// the one it already had.
type addResult struct {
	Added     *transmission.TorrentAdded `json:"torrent-added"`
	Duplicate *transmission.TorrentAdded `json:"torrent-duplicate"`
}

// addTorrent adds a torrent, and reports whether Transmission already had
// it; pyed/transmission's AddTorrent answers both the same way.
func (b *Bot) addTorrent(ctx context.Context, options transmission.AddTorrentOptions) (*transmission.TorrentAdded, bool, error) {
	var result addResult
	if err := b.Client.Execute(ctx, "torrent-add", options, &result); err != nil {
		return nil, false, err
	}
	switch {
	case result.Added != nil:
		return result.Added, false, nil
	case result.Duplicate != nil:
		return result.Duplicate, true, nil
	}
	return nil, false, errors.New("transmission added nothing")
}

func alreadyAdded(torrent *transmission.TorrentAdded) string {
	return fmt.Sprintf("<b>Already added:</b> <code>%d</code> %s", torrent.ID, esc(torrent.Name))
}

// receiveTorrent adds a .torrent file sent as a document.
func (b *Bot) receiveTorrent(ctx context.Context, r *request) {
	if r.message == nil || r.message.Document == nil {
		return // has no document
	}

	file, err := b.API.GetFile(ctx, &tgbot.GetFileParams{
		FileID: r.message.Document.FileID,
	})
	if err != nil {
		b.replyError(ctx, r, "receiver", err)
		return
	}

	// Download the file in this process and add it by raw metainfo. The
	// Telegram file URL contains the bot token, so it must never be handed
	// to Transmission; downloading in-process keeps that credential from
	// being disclosed to Transmission.
	data, err := downloadTelegramFile(ctx, b.API.FileDownloadLink(file))
	if err != nil {
		b.Logger.Warn("Telegram file download failed", "error", err)
		b.replyError(ctx, r, "receiver", err)
		return
	}

	metainfo := base64.StdEncoding.EncodeToString(data)
	torrent, duplicate, err := b.addTorrent(ctx, transmission.AddTorrentOptions{MetaInfo: &metainfo})
	if err != nil {
		b.replyError(ctx, r, "receiver", err)
		return
	}
	if duplicate {
		b.reply(ctx, r, alreadyAdded(torrent))
		return
	}

	b.reply(ctx, r, fmt.Sprintf("<b>Added:</b> <code>%d</code> %s", torrent.ID, esc(torrent.Name)))
}

// maxTorrentDownloadSize is the largest .torrent file we will download from
// Telegram; real .torrent files are small, so anything bigger is rejected.
const maxTorrentDownloadSize = 10 << 20 // 10 MiB

// torrentDownloadClient has an explicit timeout for Telegram file downloads;
// the passed context can still cancel the download earlier.
var torrentDownloadClient = &http.Client{Timeout: 60 * time.Second}

// downloadTelegramFile downloads a Telegram file by URL. The URL contains the
// bot token, so it must only ever be requested from this process.
func downloadTelegramFile(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, privateDownloadError{cause: err}
	}
	resp, err := torrentDownloadClient.Do(req)
	if err != nil {
		return nil, privateDownloadError{cause: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %s", resp.Status)
	}
	// Read at most maxTorrentDownloadSize+1 bytes so an oversize response is
	// detected without buffering it unboundedly.
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxTorrentDownloadSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxTorrentDownloadSize {
		return nil, fmt.Errorf("file exceeds maximum size of %d bytes", maxTorrentDownloadSize)
	}
	return data, nil
}

// privateDownloadError retains the underlying error for errors.Is/As while
// ensuring its token-bearing Telegram URL is never rendered in a log or chat.
type privateDownloadError struct {
	cause error
}

func (e privateDownloadError) Error() string {
	return "could not download file from Telegram"
}

func (e privateDownloadError) Unwrap() error {
	return e.cause
}

// del removes torrents, keeping their data.
func (b *Bot) del(ctx context.Context, r *request) {
	b.remove(ctx, r, "del", false)
}

// deldata removes torrents and deletes their data.
func (b *Bot) deldata(ctx context.Context, r *request) {
	b.remove(ctx, r, "deldata", true)
}

// remove removes the torrents named by ID, with their data or not, and
// replies once.
func (b *Bot) remove(ctx context.Context, r *request, name string, deleteData bool) {
	if len(r.args) == 0 {
		b.reply(ctx, r, "<b>"+name+":</b> needs an ID")
		return
	}
	torrents, problems, ok := b.torrentsNamed(ctx, r, name)
	if !ok {
		return
	}
	if len(torrents) > 0 {
		if err := b.Client.RemoveTorrents(ctx, deleteData, torrents.GetIDs()...); err != nil {
			b.replyError(ctx, r, name, err)
			return
		}
	}
	done := "Deleted"
	if deleteData {
		done = "Deleted with data"
	}
	b.reply(ctx, r, summary(done, torrents, problems))
}

// stop stops torrents, or all of them.
func (b *Bot) stop(ctx context.Context, r *request) {
	b.act(ctx, r, "stop", b.Client.StopAll, b.Client.StopTorrents, "Stopped")
}

// start starts torrents, or all of them. Telegram sends a bare /start when
// someone opens a chat with the bot, which gets the list of commands.
func (b *Bot) start(ctx context.Context, r *request) {
	if len(r.args) == 0 {
		b.reply(ctx, r, "This bot controls Transmission. Send it a magnet link or a .torrent file to add a torrent, "+
			"or use a command:\n\n"+helpText())
		return
	}
	b.act(ctx, r, "start", b.Client.StartAll, b.Client.StartTorrents, "Started")
}

// check verifies torrents, or all of them.
func (b *Bot) check(ctx context.Context, r *request) {
	b.act(ctx, r, "check", b.Client.VerifyAll, b.Client.VerifyTorrents, "Verifying")
}

// act runs one of stop, start and check on the torrents named by ID, in one
// request, or on every torrent for "all", in any case, and replies once.
func (b *Bot) act(ctx context.Context, r *request, name string,
	all func(...context.Context) error, some func(context.Context, ...int) error, done string) {
	if len(r.args) == 0 {
		b.reply(ctx, r, "<b>"+name+":</b> needs an argument")
		return
	}

	if strings.EqualFold(r.args[0], "all") {
		if err := all(ctx); err != nil {
			b.replyError(ctx, r, name, err)
			return
		}
		b.reply(ctx, r, "<b>"+done+":</b> all torrents")
		return
	}

	torrents, problems, ok := b.torrentsNamed(ctx, r, name)
	if !ok {
		return
	}
	if len(torrents) > 0 {
		if err := some(ctx, torrents.GetIDs()...); err != nil {
			b.replyError(ctx, r, name, err)
			return
		}
	}
	b.reply(ctx, r, summary(done, torrents, problems))
}

// torrentsNamed finds, in one request, the torrents whose IDs the command
// was given, in the order given, and describes the arguments that name
// none. It replies itself, and returns false, when Transmission fails.
func (b *Bot) torrentsNamed(ctx context.Context, r *request, name string) (transmission.Torrents, []string, bool) {
	var ids []int
	var problems []string
	for _, arg := range r.args {
		id, err := strconv.Atoi(arg)
		switch {
		case err != nil || id <= 0:
			problems = append(problems, esc(arg)+" is not a torrent ID")
		case !slices.Contains(ids, id):
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil, problems, true
	}
	found, err := b.Client.GetTorrentsByIDs(ctx, ids, "id", "name")
	if err != nil {
		b.replyError(ctx, r, name, err)
		return nil, nil, false
	}
	var torrents transmission.Torrents
	for _, id := range ids {
		index := slices.IndexFunc(found, func(t *transmission.Torrent) bool { return t.ID == id })
		if index < 0 {
			problems = append(problems, fmt.Sprintf("No torrent has the ID %d", id))
			continue
		}
		torrents = append(torrents, found[index])
	}
	return torrents, problems, true
}

// summary replies to an action on torrents: which it was done to, and the
// arguments that named none.
func summary(done string, torrents transmission.Torrents, problems []string) string {
	var lines []string
	if len(torrents) > 0 {
		lines = append(lines, "<b>"+done+":</b>")
		for _, t := range torrents {
			lines = append(lines, fmt.Sprintf("<code>%d</code> %s", t.ID, esc(t.Name)))
		}
	}
	for _, problem := range problems {
		lines = append(lines, "⚠️ "+problem)
	}
	return strings.Join(lines, "\n")
}

// reannounce asks trackers for peers now, for torrents or all of them.
func (b *Bot) reannounce(ctx context.Context, r *request) {
	if len(r.args) == 0 {
		b.reply(ctx, r, "<b>reannounce:</b> please provide torrent IDs or <i>all</i>")
		return
	}

	if strings.ToLower(r.args[0]) == "all" {
		torrents, err := b.Client.GetTorrents(ctx)
		if err != nil {
			b.replyError(ctx, r, "reannounce", err)
			return
		}
		if err := b.Client.ReannounceTorrents(ctx, torrents.GetIDs()...); err != nil {
			b.replyError(ctx, r, "reannounce", err)
			return
		}
		b.reply(ctx, r, "📡 Re-announced all torrents to trackers")
		return
	}

	var ids []int
	for _, idStr := range r.args {
		id, err := strconv.Atoi(idStr)
		if err != nil {
			continue
		}
		ids = append(ids, id)
	}

	if len(ids) == 0 {
		b.reply(ctx, r, "<b>reannounce:</b> no valid IDs provided")
		return
	}

	if err := b.Client.ReannounceTorrents(ctx, ids...); err != nil {
		b.replyError(ctx, r, "reannounce", err)
		return
	}

	b.reply(ctx, r, fmt.Sprintf("📡 Re-announced %d torrent(s) to trackers", len(ids)))
}
