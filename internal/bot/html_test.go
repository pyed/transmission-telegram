package bot

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/pyed/transmission"
	"github.com/pyed/transmission-telegram/v2/internal/config"
)

// trickyName holds every character Telegram's HTML or the old Markdown
// replies treated specially.
const trickyName = "<Show> & \"Friends\" S01_E02 *1080p* [x264] `tick` 🚀"

// telegramTag matches the tags Telegram's HTML parse mode takes.
var telegramTag = regexp.MustCompile(`<(/?)(b|strong|i|em|u|ins|s|strike|del|code|pre|a|tg-spoiler|blockquote)(\s[^>]*)?>`)

// entity matches the entities Telegram's HTML parse mode takes.
var entity = regexp.MustCompile(`^&(lt|gt|amp|quot|#\d+|#x[0-9a-fA-F]+);`)

// checkTelegramHTML fails when text is not HTML that Telegram parses: an
// unknown or unbalanced tag, or a bare <, > or &.
func checkTelegramHTML(t *testing.T, text string) {
	t.Helper()
	var open []string
	for i := 0; i < len(text); {
		switch text[i] {
		case '<':
			tag := telegramTag.FindStringSubmatch(text[i:])
			if tag == nil || !strings.HasPrefix(text[i:], tag[0]) {
				t.Errorf("bare or unknown tag at %d in %q", i, text)
				return
			}
			if tag[1] == "/" {
				if len(open) == 0 || open[len(open)-1] != tag[2] {
					t.Errorf("unbalanced </%s> in %q", tag[2], text)
					return
				}
				open = open[:len(open)-1]
			} else {
				open = append(open, tag[2])
			}
			i += len(tag[0])
		case '>':
			t.Errorf("bare > at %d in %q", i, text)
			return
		case '&':
			match := entity.FindString(text[i:])
			if match == "" {
				t.Errorf("bare & at %d in %q", i, text)
				return
			}
			i += len(match)
		default:
			i++
		}
	}
	if len(open) != 0 {
		t.Errorf("unclosed tags %v in %q", open, text)
	}
}

// Every command's reply is HTML Telegram accepts, and shows torrent names
// exactly as they are, whatever characters they hold.
func TestRepliesAreValidHTMLAndShowNamesExactly(t *testing.T) {
	ft := newFakeTelegram(t)
	fts := newFakeTransmission(t)
	fts.torrents = transmission.Torrents{
		{ID: 1, Name: trickyName, HashString: "0123456789abcdef0123456789abcdef01234567", Status: transmission.StatusDownloading,
			PercentDone: 0.5, RateDownload: 1000, RateUpload: 10, Eta: 90, AddedDate: 1700000000,
			Trackers: []transmission.Tracker{{Announce: "https://tracker.example/announce?passkey=x&y=<z>"}}},
		{ID: 2, Name: trickyName + " 2", Status: transmission.StatusStopped, PercentDone: 1,
			Error: transmission.ErrorTrackerError, ErrorString: "Tracker <error> & \"refusal\""},
	}
	client, err := transmission.New(fts.URL+"/transmission/rpc", "", "")
	if err != nil {
		t.Fatalf("failed to create transmission client: %v", err)
	}
	b := newTestBot(t, ft.newBot(t), client)
	b.Config.Masters = config.MasterSlice{"12345"}

	commands := [][]string{
		{"list"}, {"list", "tracker.example"}, {"list", "<nothing>"}, {"head"}, {"tail", "1"}, {"latest"},
		{"search", "show>"}, {"search", "<none & nothing>"}, {"downs"}, {"seeding"}, {"paused"}, {"checking"},
		{"errors"}, {"active"}, {"info", "1"}, {"info", "<x>"}, {"files", "1"}, {"files", "<x>"},
		{"stats"}, {"speed"}, {"count"}, {"trackers"},
		{"version"}, {"help"}, {"sort"}, {"sort", "rev", "size"}, {"sort", "<bad>"}, {"downloaddir", "/media/<a & b>"},
		{"downlimit", "100"}, {"uplimit", "x<"}, {"limit"}, {"limit", "down", "5"}, {"limit", "<x>"}, {"whoami"}, {"turtle", "on"}, {"turtle", "schedule"}, {"turtle", "schedule", "<x>"}, {"free", "/media/<a & b>"}, {"reannounce", "all"},
		{"add", "magnet:?xt=urn:btih:<x>&dn=a"}, {"add", "magnet:?xt=urn:btih:x", "d=/<a&b>", "l=<x>", "paused"}, {"add", "<x>=1"}, {"stop", "1", "<x>"}, {"start", "all"}, {"check", "1"},
		{"del", "1"}, {"move", "1", "/<a & b>"}, {"deldata", "<x>"}, {"notify"}, {"notify", "errors", "on"}, {"notify", "<x>"}, {"nosuchcommand"},
	}
	for _, command := range commands {
		before := len(ft.sentTexts())
		b.handleUpdate(context.Background(), nil, message("/"+strings.Join(command, " ")))
		texts := ft.sentTexts()[before:]
		if len(texts) == 0 {
			t.Errorf("/%s sent nothing", strings.Join(command, " "))
		}
		for _, text := range texts {
			checkTelegramHTML(t, text)
		}
	}

	for _, command := range []string{"list", "head", "info 1", "errors"} {
		before := len(ft.sentTexts())
		b.handleUpdate(context.Background(), nil, message("/"+command))
		shown := plainText(strings.Join(ft.sentTexts()[before:], "\n"))
		if !strings.Contains(shown, trickyName) {
			t.Errorf("/%s does not show the name exactly:\n%s", command, shown)
		}
	}
}

func TestTorrentLineShowsStateAndProgress(t *testing.T) {
	downloading := &transmission.Torrent{ID: 7, Name: "a_b", Status: transmission.StatusDownloading, PercentDone: 0.456}
	if got, want := torrentLine(downloading), "<code>7</code> ⬇️ 45.6% a_b"; got != want {
		t.Errorf("torrentLine = %q, want %q", got, want)
	}
	seeding := &transmission.Torrent{ID: 8, Name: "done", Status: transmission.StatusSeeding, PercentDone: 1}
	if got, want := torrentLine(seeding), "<code>8</code> ⬆️ done"; got != want {
		t.Errorf("torrentLine = %q, want %q", got, want)
	}
	failing := &transmission.Torrent{ID: 9, Name: "x", Status: transmission.StatusSeeding, PercentDone: 1, Error: transmission.ErrorLocalError}
	if got := torrentLine(failing); !strings.Contains(got, "⚠️") {
		t.Errorf("torrentLine of a failing torrent = %q, want the warning sign", got)
	}
}

func TestRatioAndETAFormats(t *testing.T) {
	for value, want := range map[float64]string{-1: "–", -2: "∞", 0: "0.00", 1.5: "1.50"} {
		if got := ratio(value); got != want {
			t.Errorf("ratio(%v) = %q, want %q", value, got, want)
		}
	}
	for seconds, want := range map[int]string{-1: "–", -2: "∞", 90: "1m30s"} {
		// The library holds Transmission's seconds as a bare time.Duration.
		if got := eta(time.Duration(seconds)); got != want {
			t.Errorf("eta(%d) = %q, want %q", seconds, got, want)
		}
	}
	if got := date(0); got != "–" {
		t.Errorf("date(0) = %q", got)
	}
	if got := date(1700000000); !strings.HasPrefix(got, "2023-11-1") {
		t.Errorf("date shows no year: %q", got)
	}
}

func Example_torrentBlock() {
	t := &transmission.Torrent{ID: 3, Name: "Ubuntu <24.04>", Status: transmission.StatusDownloading, PercentDone: 0.5,
		HaveValid: 500000000, SizeWhenDone: 1000000000, RateDownload: 2000000, RateUpload: 3000, UploadRatio: 0.12}
	fmt.Println(torrentBlock(t, true))
	fmt.Println(torrentBlock(t, false))
	// Output:
	// <code>3</code> ⬇️ <b>Ubuntu &lt;24.04&gt;</b>
	// [█████░░░░░] 50.0% · Downloading · 500 MB of 1.0 GB
	// ↓ 2.0 MB/s · ↑ 3.0 kB/s · ratio 0.12
	// <code>3</code> ⬇️ <b>Ubuntu &lt;24.04&gt;</b>
	// [█████░░░░░] 50.0% · Downloading · 500 MB of 1.0 GB
	// ↓ – · ↑ – · ratio 0.12
}
