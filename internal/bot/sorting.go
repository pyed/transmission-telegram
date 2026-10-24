package bot

import (
	"cmp"
	"context"
	"slices"
	"strings"

	"github.com/pyed/transmission"
	"github.com/pyed/transmission-telegram/v2/internal/state"
)

// sortKeys compare torrents by each method /sort takes.
var sortKeys = map[string]func(a, b *transmission.Torrent) int{
	"id": func(a, b *transmission.Torrent) int { return cmp.Compare(a.ID, b.ID) },
	"name": func(a, b *transmission.Torrent) int {
		return cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	},
	"age":       func(a, b *transmission.Torrent) int { return cmp.Compare(a.AddedDate, b.AddedDate) },
	"size":      func(a, b *transmission.Torrent) int { return cmp.Compare(a.SizeWhenDone, b.SizeWhenDone) },
	"progress":  func(a, b *transmission.Torrent) int { return cmp.Compare(a.PercentDone, b.PercentDone) },
	"downspeed": func(a, b *transmission.Torrent) int { return cmp.Compare(a.RateDownload, b.RateDownload) },
	"upspeed":   func(a, b *transmission.Torrent) int { return cmp.Compare(a.RateUpload, b.RateUpload) },
	"download":  func(a, b *transmission.Torrent) int { return cmp.Compare(a.DownloadedEver, b.DownloadedEver) },
	"upload":    func(a, b *transmission.Torrent) int { return cmp.Compare(a.UploadedEver, b.UploadedEver) },
	"ratio":     func(a, b *transmission.Torrent) int { return cmp.Compare(a.UploadRatio, b.UploadRatio) },
}

// sortTorrents puts torrents in order, given as /sort takes it: "size", or
// "rev size" for the reverse. Ties go by ID, so a live list keeps still.
func sortTorrents(torrents transmission.Torrents, order string) {
	method, reversed := strings.CutPrefix(order, "rev ")
	key, ok := sortKeys[method]
	if !ok {
		key, reversed = sortKeys["id"], false
	}
	slices.SortStableFunc(torrents, func(a, b *transmission.Torrent) int {
		c := key(a, b)
		if reversed {
			c = -c
		}
		if c == 0 {
			c = cmp.Compare(a.ID, b.ID)
		}
		return c
	})
}

// torrents lists every torrent in the order chatID chose with /sort.
func (b *Bot) torrents(ctx context.Context, chatID int64) (transmission.Torrents, error) {
	torrents, err := b.Client.GetTorrentsByIDs(ctx, nil)
	if err != nil {
		return nil, err
	}
	var order string
	b.State.Read(func(data *state.Data) { order = data.Sorts[chatID] })
	sortTorrents(torrents, order)
	return torrents, nil
}
