package config

import (
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

// writeConfig writes a config file holding lines, readable by its owner
// only.
func writeConfig(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "transmission-telegram.conf")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// Settings come from flags, then the config file, then the environment.
func TestParseFlags_ConfigFile(t *testing.T) {
	path := writeConfig(t,
		string(rune(0xFEFF))+"# Written by hand",
		"",
		"token = file-token",
		"master = 123, @Alice",
		"master=456",
		"  url = http://nas:9091/transmission/rpc  ",
		"no-live",
		"watch-interval = 30s",
	)
	env := map[string]string{"TT_BOT_TOKEN": "env-token", "TT_MASTERS": "999", "TR_AUTH": "envuser:envpass", "TT_STATE": "/env/state.json"}
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	cfg, err := ParseFlags(fs, []string{"-config=" + path, "-url=http://flag:9091/transmission/rpc"}, func(key string) string { return env[key] })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BotToken != "file-token" || !reflect.DeepEqual(cfg.Masters, MasterSlice{"123", "alice", "456"}) {
		t.Errorf("the environment won over the file: %q, %v", cfg.BotToken, cfg.Masters)
	}
	if cfg.RPCURL != "http://flag:9091/transmission/rpc" {
		t.Errorf("the file won over the flag: %q", cfg.RPCURL)
	}
	if !cfg.NoLive || cfg.WatchInterval != 30*time.Second {
		t.Errorf("no-live = %v, watch-interval = %v", cfg.NoLive, cfg.WatchInterval)
	}
	if cfg.Username != "envuser" || cfg.Password != "envpass" || cfg.StateFile != "/env/state.json" {
		t.Errorf("settings the file lacks did not come from the environment: %q %q %q", cfg.Username, cfg.Password, cfg.StateFile)
	}
	if cfg.ConfigFile != path || len(cfg.Warnings) != 0 {
		t.Errorf("ConfigFile = %q, warnings %q", cfg.ConfigFile, cfg.Warnings)
	}
}

// A mistake in the config file stops the bot and says where it is, without
// quoting a line that may be a secret.
func TestParseFlags_ConfigFileMistakes(t *testing.T) {
	for _, test := range []struct {
		line, want string
	}{
		{"colour = blue", `:2: "colour" is not a setting`},
		{"123456:SECRET-TOKEN", ":2: expected a setting and its value"},
		{"config = other.conf", `:2: "config" is not a setting`},
		{"version", ":2: expected a setting and its value"},
		{"url", ":2: url needs a value"},
		{"watch-interval = soon", ":2: watch-interval: parse error"},
		{"master = ,", ":2: master: master cannot be empty"},
	} {
		path := writeConfig(t, "token = t", test.line, "master = 1")
		fs := flag.NewFlagSet("test", flag.ContinueOnError)
		_, err := ParseFlags(fs, []string{"-config", path}, func(string) string { return "" })
		if err == nil || !strings.Contains(err.Error(), path+test.want) || strings.Contains(err.Error(), "SECRET") {
			t.Errorf("%q: error %v, want %q", test.line, err, test.want)
		}
	}
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	_, err := ParseFlags(fs, []string{"-config", filepath.Join(t.TempDir(), "missing.conf")}, func(string) string { return "" })
	if err == nil || !strings.HasPrefix(err.Error(), "-config: open ") {
		t.Errorf("a missing config file: %v", err)
	}
}

// A config file with a secret that other users can read is reported.
func TestParseFlags_ConfigFileOthersCanRead(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows files have no Unix permissions")
	}
	for _, test := range []struct {
		lines []string
		mode  os.FileMode
		warn  bool
	}{
		{[]string{"token = t", "master = 1"}, 0o644, true},
		{[]string{"master = 1", "password = p"}, 0o640, true},
		{[]string{"token = t", "master = 1"}, 0o600, false},
		{[]string{"master = 1"}, 0o644, false},
	} {
		path := writeConfig(t, test.lines...)
		if err := os.Chmod(path, test.mode); err != nil {
			t.Fatal(err)
		}
		fs := flag.NewFlagSet("test", flag.ContinueOnError)
		cfg, err := ParseFlags(fs, []string{"-config=" + path}, func(key string) string {
			if key == "TT_BOT_TOKEN" {
				return "t"
			}
			return ""
		})
		if err != nil {
			t.Fatal(err)
		}
		warned := len(cfg.Warnings) == 1 && strings.Contains(cfg.Warnings[0], path+" holds a secret")
		if warned != test.warn {
			t.Errorf("%q at %o: warnings %q", test.lines, test.mode, cfg.Warnings)
		}
	}
}

// -master takes several masters separated by commas, as TT_MASTERS does.
func TestParseFlags_MastersSeparatedByCommas(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	cfg, err := ParseFlags(fs, []string{"-token=t", "-master=123,456", "-master=789"}, nil)
	if err != nil || !reflect.DeepEqual(cfg.Masters, MasterSlice{"123", "456", "789"}) {
		t.Fatalf("masters = %v, %v", cfg.Masters, err)
	}
}
