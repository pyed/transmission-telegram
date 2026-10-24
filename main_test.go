package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tgbot "github.com/go-telegram/bot"
	"github.com/pyed/transmission-telegram/v2/internal/config"
)

const testToken = "123456:TEST-SECRET"

// fakeTransmission answers session-get with version 4.0.6.
func fakeTransmission(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"result":"success","arguments":{"version":"4.0.6","rpc-version":17}}`))
	}))
	t.Cleanup(server.Close)
	return server
}

// fakeTelegram answers getMe as @MyBot, or, with refuse, as Telegram does a
// revoked token.
func fakeTelegram(t *testing.T, refuse bool) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if refuse {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"ok":false,"error_code":401,"description":"Unauthorized"}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":{"id":123456,"is_bot":true,"first_name":"Bot","username":"MyBot"}}`))
	}))
	t.Cleanup(server.Close)
	return server
}

func TestRunPrintsTheVersion(t *testing.T) {
	var out bytes.Buffer
	if err := run(context.Background(), &config.Config{ShowVersion: true}, &out); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "transmission-telegram "+config.Version+"\n" {
		t.Fatalf("output = %q", got)
	}
}

// A damaged state file stops the bot with an error, rather than letting it
// start afresh and lose what it remembered.
func TestRunStopsOnADamagedStateFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{BotToken: testToken, Masters: config.MasterSlice{"7"},
		RPCURL: fakeTransmission(t).URL + "/transmission/rpc", StateFile: path}
	err := run(context.Background(), cfg, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "is damaged") {
		t.Fatalf("run = %v", err)
	}
}

// -install checks that Telegram takes the token and Transmission answers,
// and says so without the token.
func TestCheckConnections(t *testing.T) {
	ctx := context.Background()
	transmissionURL := fakeTransmission(t).URL + "/transmission/rpc"
	cfg := &config.Config{BotToken: testToken, RPCURL: transmissionURL}
	var out bytes.Buffer
	if err := checkConnections(ctx, cfg, &out, tgbot.WithServerURL(fakeTelegram(t, false).URL)); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "Checked: Telegram bot @MyBot, Transmission 4.0.6 at "+transmissionURL+"\n" {
		t.Fatalf("output = %q", got)
	}

	err := checkConnections(ctx, cfg, &out, tgbot.WithServerURL(fakeTelegram(t, true).URL))
	if err == nil || !strings.HasPrefix(err.Error(), "telegram, with the bot token: ") || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("a refused token: %v", err)
	}

	cfg.RPCURL = "http://user:hunter2@127.0.0.1:1/transmission/rpc"
	err = checkConnections(ctx, cfg, &out, tgbot.WithServerURL(fakeTelegram(t, false).URL))
	if err == nil || !strings.Contains(err.Error(), "start it, or correct the settings") || strings.Contains(err.Error(), "hunter2") {
		t.Fatalf("Transmission down: %v", err)
	}
}
