package bot

import (
	"context"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pyed/transmission"
	"github.com/pyed/transmission-telegram/v2/internal/config"
	"github.com/pyed/transmission-telegram/v2/internal/state"
)

// integrationHash is the info-hash of the torrent the integration test
// adds: a magnet link nobody serves, added paused.
const integrationHash = "0123456789abcdef0123456789abcdeffedcba98"

// TestAgainstTransmission drives commands against the real Transmission
// that TT_TEST_TRANSMISSION_URL names, with TT_TEST_TRANSMISSION_AUTH as
// user:password when it asks for them, and checks what Transmission did. CI's
// "Against Transmission" job runs it. It changes the daemon's settings and
// torrents, so never point it at one in use.
func TestAgainstTransmission(t *testing.T) {
	rpcURL := os.Getenv("TT_TEST_TRANSMISSION_URL")
	if rpcURL == "" {
		t.Skip("set TT_TEST_TRANSMISSION_URL to run against a real Transmission")
	}
	username, password, _ := strings.Cut(os.Getenv("TT_TEST_TRANSMISSION_AUTH"), ":")
	client := waitForTransmission(t, rpcURL, username, password)
	ctx := context.Background()

	ft := newFakeTelegram(t)
	b := newTestBot(t, ft.newBot(t), client)
	b.Config.Masters = config.MasterSlice{"12345"}
	b.Config.NoLive = true
	say := func(command string) string {
		t.Helper()
		before := len(ft.sentTexts())
		b.handleUpdate(ctx, nil, message(command))
		reply := strings.Join(ft.sentTexts()[before:], "\n")
		name := strings.Fields(strings.TrimPrefix(command, "/"))[0]
		if reply == "" || strings.HasPrefix(reply, "<b>"+name+":</b>") {
			t.Fatalf("%s: %q", command, reply)
		}
		return reply
	}
	torrent := func() *transmission.Torrent {
		t.Helper()
		torrents, err := client.GetTorrents(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, torrent := range torrents {
			if torrent.HashString == integrationHash {
				return torrent
			}
		}
		return nil
	}
	// eventually waits a little for Transmission to do what it was asked.
	eventually := func(what string, done func(*transmission.Torrent) bool) {
		t.Helper()
		for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(200 * time.Millisecond) {
			current := torrent()
			if current != nil && done(current) {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: the torrent is %+v", what, current)
			}
		}
	}
	session := func() *transmission.Session {
		t.Helper()
		session, err := client.GetSession(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return session
	}
	t.Cleanup(func() {
		if torrent := torrent(); torrent != nil {
			_ = client.RemoveTorrents(ctx, true, torrent.ID)
		}
	})

	if reply := say("/version"); !strings.Contains(reply, "Transmission <b>") {
		t.Fatalf("/version: %q", reply)
	}
	magnet := "magnet:?xt=urn:btih:" + integrationHash + "&dn=tt-integration"
	if reply := say("/add " + magnet + " d=/downloads/tt-integration l=testing paused"); !strings.Contains(reply, "<b>Added:</b>") {
		t.Fatalf("/add: %q", reply)
	}
	added := torrent()
	if added == nil || added.Status != transmission.StatusStopped || added.DownloadDir != "/downloads/tt-integration" ||
		!slices.Equal(added.Labels, []string{"testing"}) {
		t.Fatalf("added %+v", added)
	}
	id := strconv.Itoa(added.ID)
	if reply := say("/add " + magnet); !strings.Contains(reply, "Already added") {
		t.Fatalf("/add again: %q", reply)
	}
	if reply := say("/info " + id); !strings.Contains(reply, "tt-integration") {
		t.Fatalf("/info: %q", reply)
	}
	for _, command := range []string{"/list", "/paused", "/latest", "/search integration", "/files " + id, "/stats", "/count", "/trackers", "/free", "/queue"} {
		say(command)
	}

	say("/setlabel " + id + " movies hd")
	if labels := torrent().Labels; !slices.Equal(slices.Sorted(slices.Values(labels)), []string{"hd", "movies"}) {
		t.Fatalf("labels = %q", labels)
	}
	if reply := say("/labels"); !strings.Contains(reply, "movies") {
		t.Fatalf("/labels: %q", reply)
	}
	say("/move " + id + " /downloads/tt-moved")
	eventually("/move", func(tr *transmission.Torrent) bool { return tr.DownloadDir == "/downloads/tt-moved" })
	say("/start " + id)
	eventually("/start", func(tr *transmission.Torrent) bool { return tr.Status != transmission.StatusStopped })
	say("/stop " + id)
	eventually("/stop", func(tr *transmission.Torrent) bool { return tr.Status == transmission.StatusStopped })

	say("/limit down 123 up 45")
	if s := session(); !s.SpeedLimitDownEnabled || s.SpeedLimitDown != 123 || !s.SpeedLimitUpEnabled || s.SpeedLimitUp != 45 {
		t.Fatalf("limits %d %v / %d %v", s.SpeedLimitDown, s.SpeedLimitDownEnabled, s.SpeedLimitUp, s.SpeedLimitUpEnabled)
	}
	say("/limit off")
	if s := session(); s.SpeedLimitDownEnabled || s.SpeedLimitUpEnabled || s.SpeedLimitDown != 123 {
		t.Fatalf("limits after off: %d %v / %v", s.SpeedLimitDown, s.SpeedLimitDownEnabled, s.SpeedLimitUpEnabled)
	}
	say("/turtle limits 10 20")
	say("/turtle schedule 01:00-02:30 weekdays")
	if s := session(); !s.AltSpeedTimeEnabled || s.AltSpeedTimeBegin != 60 || s.AltSpeedTimeEnd != 150 || s.AltSpeedTimeDay != weekdays ||
		s.AltSpeedDown != 10 || s.AltSpeedUp != 20 {
		t.Fatalf("turtle %+v", s)
	}
	say("/turtle schedule off")
	say("/turtle on")
	if s := session(); s.AltSpeedTimeEnabled || !s.AltSpeedEnabled {
		t.Fatalf("turtle on: scheduled %v, on %v", s.AltSpeedTimeEnabled, s.AltSpeedEnabled)
	}
	say("/turtle off")

	if text := b.renderStatus(ctx); !strings.Contains(text, "Torrents: 1 ·") || strings.Contains(text, "not answering") {
		t.Fatalf("status = %q", text)
	}
	if text, _, err := b.buildDigest(ctx, time.Now(), state.Digest{}); err != nil || !strings.Contains(text, "Added: <b>1</b> (hd 1, movies 1)") {
		t.Fatalf("digest = %q, %v", text, err)
	}

	say("/del " + id)
	for deadline := time.Now().Add(10 * time.Second); torrent() != nil; time.Sleep(200 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("/del left the torrent")
		}
	}
}

// waitForTransmission waits up to two minutes for Transmission to answer,
// as a container that has just started takes a while to.
func waitForTransmission(t *testing.T, rpcURL, username, password string) *transmission.Client {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for {
		client, err := transmission.New(rpcURL, username, password)
		if err == nil {
			return client
		}
		if time.Now().After(deadline) {
			t.Fatalf("Transmission at %s does not answer: %v", rpcURL, err)
		}
		time.Sleep(2 * time.Second)
	}
}
