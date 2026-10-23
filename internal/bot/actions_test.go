package bot

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/go-telegram/bot/models"
	"github.com/pyed/transmission"
)

// fakeTorrent is a small fake .torrent payload (not a real torrent).
var fakeTorrent = []byte("d8:announce19:http://tracker.example/4:infod6:lengthi123456ee")

// fakeTransmission is a minimal in-process Transmission RPC server.
type fakeTransmission struct {
	*httptest.Server
	mu         sync.Mutex
	addBodies  []string
	rpcMethods []string
	rpcBodies  map[string][]string
	torrents   transmission.Torrents
	// stall, when set, holds torrent-get requests until it is closed.
	stall chan struct{}
	// session is session-get's arguments, as JSON; just a version when
	// empty.
	session string
	// addAnswer is torrent-add's arguments, as JSON; a new torrent 7 when
	// empty.
	addAnswer string
}

func (f *fakeTransmission) setStall(stall chan struct{}) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stall = stall
}

// newFakeTransmission starts a fake Transmission RPC server that records
// torrent-add request bodies.
func newFakeTransmission(t *testing.T) *fakeTransmission {
	t.Helper()
	f := &fakeTransmission{rpcBodies: make(map[string][]string)}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Method    string `json:"method"`
			Arguments struct {
				// IDs is a list of torrent IDs, "recently-active", or
				// absent for every torrent.
				IDs json.RawMessage `json:"ids"`
			} `json:"arguments"`
		}
		_ = json.Unmarshal(body, &req)
		f.mu.Lock()
		f.rpcMethods = append(f.rpcMethods, req.Method)
		f.rpcBodies[req.Method] = append(f.rpcBodies[req.Method], string(body))
		f.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "session-get":
			f.mu.Lock()
			session := f.session
			f.mu.Unlock()
			if session == "" {
				session = `{"version":"test"}`
			}
			_, _ = fmt.Fprintf(w, `{"result":"success","arguments":%s}`, session)
		case "torrent-add":
			f.mu.Lock()
			f.addBodies = append(f.addBodies, string(body))
			answer := f.addAnswer
			f.mu.Unlock()
			if answer == "" {
				answer = `{"torrent-added":{"id":7,"name":"Test Torrent","hashString":"abc123"}}`
			}
			_, _ = fmt.Fprintf(w, `{"result":"success","arguments":%s}`, answer)
		case "torrent-get":
			f.mu.Lock()
			stall := f.stall
			f.mu.Unlock()
			if stall != nil {
				<-stall
			}
			f.mu.Lock()
			torrents := append(transmission.Torrents(nil), f.torrents...)
			f.mu.Unlock()
			var ids []int
			if json.Unmarshal(req.Arguments.IDs, &ids) == nil && ids != nil {
				var asked transmission.Torrents
				for _, t := range torrents {
					if slices.Contains(ids, t.ID) {
						asked = append(asked, t)
					}
				}
				torrents = asked
			}
			_ = json.NewEncoder(w).Encode(struct {
				Result    string `json:"result"`
				Arguments struct {
					Torrents transmission.Torrents `json:"torrents"`
				} `json:"arguments"`
			}{
				Result: "success",
				Arguments: struct {
					Torrents transmission.Torrents `json:"torrents"`
				}{Torrents: torrents},
			})
		default:
			_, _ = w.Write([]byte(`{"result":"success","arguments":{}}`))
		}
	}))
	t.Cleanup(f.Close)
	return f
}

// addRequests returns the raw bodies of all torrent-add requests.
func (f *fakeTransmission) addRequests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.addBodies...)
}

func (f *fakeTransmission) methodCount(method string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	var count int
	for _, got := range f.rpcMethods {
		if got == method {
			count++
		}
	}
	return count
}

func (f *fakeTransmission) requestBodies(method string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.rpcBodies[method]...)
}

// TestReceiveTorrentDoesNotLeakToken verifies that an uploaded .torrent is
// downloaded by the bot and sent to Transmission as raw metainfo, so the
// bot token embedded in the Telegram file URL never reaches Transmission.
func TestReceiveTorrentDoesNotLeakToken(t *testing.T) {
	ft := newFakeTelegram(t)
	ft.fileBytes = fakeTorrent

	fts := newFakeTransmission(t)
	client, err := transmission.New(fts.URL+"/transmission/rpc", "", "")
	if err != nil {
		t.Fatalf("failed to create transmission client: %v", err)
	}

	b := newTestBot(t, ft.newBot(t), client)

	b.receiveTorrent(context.Background(), &request{chatID: 42, message: &models.Message{
		Chat:     models.Chat{ID: 42},
		Document: &models.Document{FileID: "test-file-id", FileName: "upload.torrent"},
	}})

	// 1. Transmission must have received exactly one torrent-add.
	adds := fts.addRequests()
	if len(adds) != 1 {
		t.Fatalf("expected 1 torrent-add request, got %d", len(adds))
	}
	body := adds[0]

	// 2. The torrent bytes must be present as base64 metainfo.
	wantMeta := base64.StdEncoding.EncodeToString(fakeTorrent)
	if !strings.Contains(body, `"metainfo":"`+wantMeta+`"`) {
		t.Errorf("torrent-add body does not contain expected metainfo %q:\n%s", wantMeta, body)
	}

	// 3. The bot token and the Telegram file URL must not leak.
	if strings.Contains(body, testBotToken) {
		t.Errorf("bot token leaked to Transmission:\n%s", body)
	}
	if strings.Contains(body, ft.Server.URL) {
		t.Errorf("Telegram file URL leaked to Transmission:\n%s", body)
	}

	// 4. The user should be told the torrent was added.
	texts := ft.sentTexts()
	if len(texts) == 0 || !strings.Contains(texts[len(texts)-1], "Test Torrent") {
		t.Errorf("expected final message about the added torrent, got %v", texts)
	}
}

func TestAddHonorsCanceledContext(t *testing.T) {
	ft := newFakeTelegram(t)
	fts := newFakeTransmission(t)
	client, err := transmission.New(fts.URL+"/transmission/rpc", "", "")
	if err != nil {
		t.Fatalf("failed to create transmission client: %v", err)
	}
	b := newTestBot(t, ft.newBot(t), client)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	b.add(ctx, req("https://example.com/test.torrent"))

	if got := fts.methodCount("torrent-add"); got != 0 {
		t.Fatalf("canceled command issued %d torrent-add RPC calls", got)
	}
}

// Several IDs make one request and one reply, which names the torrents and
// the arguments that name none; "all" works in any case.
func TestActionsOnSeveralTorrents(t *testing.T) {
	ft := newFakeTelegram(t)
	fts := newFakeTransmission(t)
	fts.torrents = transmission.Torrents{{ID: 1, Name: "One <1>"}, {ID: 2, Name: "Two"}}
	client, err := transmission.New(fts.URL+"/transmission/rpc", "", "")
	if err != nil {
		t.Fatalf("failed to create transmission client: %v", err)
	}
	b := newTestBot(t, ft.newBot(t), client)
	ctx := context.Background()

	b.stop(ctx, req("1", "x", "2", "9", "1"))
	b.start(ctx, req("ALL"))
	b.del(ctx, req("2"))
	b.check(ctx, req("9"))

	for method, want := range map[string][]string{
		"torrent-stop":   {`{"method":"torrent-stop","arguments":{"ids":[1,2]}}`},
		"torrent-start":  {`{"method":"torrent-start"}`},
		"torrent-remove": {`{"method":"torrent-remove","arguments":{"ids":[2],"delete-local-data":false}}`},
		"torrent-verify": nil,
	} {
		if got := fts.requestBodies(method); !slices.Equal(got, want) {
			t.Errorf("%s requests = %q, want %q", method, got, want)
		}
	}
	want := []string{
		"<b>Stopped:</b>\n<code>1</code> One &lt;1&gt;\n<code>2</code> Two\n⚠️ x is not a torrent ID\n⚠️ No torrent has the ID 9",
		"<b>Started:</b> all torrents",
		"<b>Deleted:</b>\n<code>2</code> Two",
		"⚠️ No torrent has the ID 9",
	}
	if texts := ft.sentTexts(); !slices.Equal(texts, want) {
		t.Fatalf("replies = %q\nwant %q", texts, want)
	}
}

// Transmission answers a torrent it already has with torrent-duplicate;
// that is no new torrent, and the reply says so.
func TestAddSaysWhenATorrentWasAlreadyThere(t *testing.T) {
	ft := newFakeTelegram(t)
	ft.fileBytes = fakeTorrent
	fts := newFakeTransmission(t)
	fts.addAnswer = `{"torrent-duplicate":{"id":3,"name":"Old <Torrent>","hashString":"abc123"}}`
	client, err := transmission.New(fts.URL+"/transmission/rpc", "", "")
	if err != nil {
		t.Fatalf("failed to create transmission client: %v", err)
	}
	b := newTestBot(t, ft.newBot(t), client)

	b.add(context.Background(), req("magnet:?xt=urn:btih:abc123"))
	b.receiveTorrent(context.Background(), &request{chatID: 42, message: &models.Message{
		Document: &models.Document{FileID: "test-file-id", FileName: "upload.torrent"},
	}})
	want := "<b>Already added:</b> <code>3</code> Old &lt;Torrent&gt;"
	if texts := ft.sentTexts(); len(texts) != 2 || texts[0] != want || texts[1] != want {
		t.Fatalf("replies = %q, want %q twice", texts, want)
	}
}

// TestDownloadTelegramFile verifies the helper used by receiveTorrent.
func TestDownloadTelegramFile(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, "hello torrent")
		}))
		t.Cleanup(srv.Close)

		data, err := downloadTelegramFile(context.Background(), srv.URL)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if string(data) != "hello torrent" {
			t.Errorf("unexpected data %q", data)
		}
	})

	t.Run("non-200", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "nope", http.StatusNotFound)
		}))
		t.Cleanup(srv.Close)

		if _, err := downloadTelegramFile(context.Background(), srv.URL); err == nil {
			t.Error("expected an error for a non-200 response")
		}
	})

	t.Run("oversize", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, strings.Repeat("a", maxTorrentDownloadSize+1))
		}))
		t.Cleanup(srv.Close)

		data, err := downloadTelegramFile(context.Background(), srv.URL)
		if err == nil {
			t.Fatalf("expected an error for an oversize response, got %d bytes", len(data))
		}
		if !strings.Contains(err.Error(), "exceeds maximum size") {
			t.Errorf("unexpected error %q", err)
		}
	})

	t.Run("network error redacts token URL", func(t *testing.T) {
		const secret = "123456:SUPER-SECRET-TOKEN"
		_, err := downloadTelegramFile(context.Background(), "://api.telegram.org/file/bot"+secret+"/upload.torrent")
		if err == nil {
			t.Fatal("expected malformed download URL to fail")
		}
		if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "api.telegram.org") {
			t.Fatalf("download error exposed the private URL: %q", err)
		}
	})
}
