package config

import (
	"errors"
	"flag"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestMasterSlice(t *testing.T) {
	var masters MasterSlice

	if err := masters.Set("Sheriff"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := masters.Set("@Pyed"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := masters.Set("987654321"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Test Contains (case-insensitivity and @ handling)
	if !masters.Contains("sheriff", 0) {
		t.Errorf("expected Contains('sheriff', 0) to be true")
	}
	if !masters.Contains("SHERIFF", 0) {
		t.Errorf("expected Contains('SHERIFF', 0) to be true")
	}
	if !masters.Contains("@sheriff", 0) {
		t.Errorf("expected Contains('@sheriff', 0) to be true")
	}
	if !masters.Contains("pyed", 0) {
		t.Errorf("expected Contains('pyed', 0) to be true")
	}
	if !masters.Contains("@Pyed", 0) {
		t.Errorf("expected Contains('@Pyed', 0) to be true")
	}
	// Test numeric User ID matching
	if !masters.Contains("", 987654321) {
		t.Errorf("expected Contains('', 987654321) to be true")
	}
	if !masters.Contains("any_username", 987654321) {
		t.Errorf("expected Contains('any_username', 987654321) to be true")
	}
	// Non-matching
	if masters.Contains("other_user", 111) {
		t.Errorf("expected Contains('other_user', 111) to be false")
	}

	// Test String
	str := masters.String()
	if !strings.Contains(str, "sheriff") || !strings.Contains(str, "@pyed") || !strings.Contains(str, "987654321") {
		t.Errorf("unexpected String output: %s", str)
	}
}

func TestMasterSliceUsernames(t *testing.T) {
	masters := MasterSlice{"alice", "12345", "bob_99", "987654321"}
	if got := masters.Usernames(); !reflect.DeepEqual(got, []string{"alice", "bob_99"}) {
		t.Fatalf("Usernames() = %v, want [alice bob_99]", got)
	}
	if got := (MasterSlice{"12345"}).Usernames(); got != nil {
		t.Fatalf("numeric IDs reported as usernames: %v", got)
	}
}

func TestParseFlags_Success(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	args := []string{
		"-token=test-token",
		"-master=@User1",
		"-master=user2",
		"-url=http://127.0.0.1:9091/transmission/rpc",
		"-username=admin",
		"-password=secret",
		"-no-live",
	}

	cfg, err := ParseFlags(fs, args, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.BotToken != "test-token" {
		t.Errorf("expected BotToken='test-token', got %q", cfg.BotToken)
	}
	if len(cfg.Masters) != 2 || cfg.Masters[0] != "user1" || cfg.Masters[1] != "user2" {
		t.Errorf("unexpected Masters: %v", cfg.Masters)
	}
	if cfg.RPCURL != "http://127.0.0.1:9091/transmission/rpc" {
		t.Errorf("unexpected RPCURL: %s", cfg.RPCURL)
	}
	if cfg.Username != "admin" || cfg.Password != "secret" {
		t.Errorf("unexpected credentials: %s / %s", cfg.Username, cfg.Password)
	}
	if !cfg.NoLive {
		t.Errorf("expected NoLive=true")
	}
	if cfg.Interval != 5*time.Second {
		t.Errorf("expected Interval=5s, got %v", cfg.Interval)
	}
	if cfg.Duration != 10 {
		t.Errorf("expected Duration=10, got %d", cfg.Duration)
	}
}

func TestParseFlags_EnvVars(t *testing.T) {
	env := map[string]string{
		"TT_BOTT": "env-token-123",
		"TR_AUTH": "user:pass:with:colons",
		"TR_URL":  "http://transmission:9091/transmission/rpc",
	}
	getenv := func(key string) string {
		return env[key]
	}

	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	args := []string{"-master=owner"}

	cfg, err := ParseFlags(fs, args, getenv)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.BotToken != "env-token-123" {
		t.Errorf("expected BotToken from env, got %q", cfg.BotToken)
	}
	if cfg.Username != "user" || cfg.Password != "pass:with:colons" {
		t.Errorf("expected password with colons preserved, got %q / %q", cfg.Username, cfg.Password)
	}
	if cfg.RPCURL != "http://transmission:9091/transmission/rpc" {
		t.Errorf("expected RPCURL from TR_URL, got %q", cfg.RPCURL)
	}
}

func TestParseFlags_FlagsOverrideEnvironment(t *testing.T) {
	env := map[string]string{
		"TT_BOT_TOKEN": "environment-token",
		"TT_BOTT":      "legacy-token",
		"TR_URL":       "http://environment/transmission/rpc",
		"TR_AUTH":      "environment-user:environment-password",
	}
	getenv := func(key string) string { return env[key] }
	fs := flag.NewFlagSet("test", flag.ContinueOnError)

	cfg, err := ParseFlags(fs, []string{
		"-token=flag-token",
		"-master=12345",
		"-url=http://flag/transmission/rpc",
		"-username=flag-user",
		"-password=flag-password",
	}, getenv)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.BotToken != "flag-token" || cfg.RPCURL != "http://flag/transmission/rpc" ||
		cfg.Username != "flag-user" || cfg.Password != "flag-password" {
		t.Fatalf("flags did not override environment: token=%q url=%q username=%q password=%q",
			cfg.BotToken, cfg.RPCURL, cfg.Username, cfg.Password)
	}
}

func TestParseFlags_CredentialFlagsOverrideIndependently(t *testing.T) {
	tests := []struct {
		name         string
		args         []string
		wantUsername string
		wantPassword string
	}{
		{
			name:         "username flag keeps environment password",
			args:         []string{"-username=flag-user"},
			wantUsername: "flag-user",
			wantPassword: "environment-password",
		},
		{
			name:         "password flag keeps environment username",
			args:         []string{"-password=flag-password"},
			wantUsername: "environment-user",
			wantPassword: "flag-password",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := flag.NewFlagSet("test", flag.ContinueOnError)
			args := append([]string{"-token=test-token", "-master=12345"}, tt.args...)
			cfg, err := ParseFlags(fs, args, func(key string) string {
				if key == "TR_AUTH" {
					return "environment-user:environment-password"
				}
				return ""
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cfg.Username != tt.wantUsername || cfg.Password != tt.wantPassword {
				t.Fatalf("credentials = %q / %q, want %q / %q",
					cfg.Username, cfg.Password, tt.wantUsername, tt.wantPassword)
			}
		})
	}
}

// The usage text, which -h and every usage error print, must not show values
// taken from the environment: they are secrets, and the text ends up in logs.
func TestUsageDoesNotShowValuesFromEnvironment(t *testing.T) {
	env := map[string]string{
		"TT_BOT_TOKEN": "123456:ENV-TOKEN",
		"TR_AUTH":      "env-user:env-password",
		"TR_URL":       "http://url-user:url-password@transmission:9091/transmission/rpc",
	}
	var usage strings.Builder
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(&usage)
	if _, err := ParseFlags(fs, []string{"-h"}, func(key string) string { return env[key] }); !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("-h returned %v, want flag.ErrHelp", err)
	}
	for _, value := range []string{"ENV-TOKEN", "env-user", "env-password", "url-user", "url-password"} {
		if strings.Contains(usage.String(), value) {
			t.Errorf("usage shows %q from the environment:\n%s", value, usage.String())
		}
	}
	if !strings.Contains(usage.String(), "TT_BOT_TOKEN") {
		t.Errorf("usage no longer names TT_BOT_TOKEN:\n%s", usage.String())
	}
}

func TestParseFlags_PrefersStandardTokenEnvironment(t *testing.T) {
	env := map[string]string{
		"TT_BOT_TOKEN": "standard-token",
		"TT_BOTT":      "legacy-token",
	}
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	cfg, err := ParseFlags(fs, []string{"-master=12345"}, func(key string) string { return env[key] })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.BotToken != "standard-token" {
		t.Fatalf("expected standard token environment variable, got %q", cfg.BotToken)
	}
}

// A URL Transmission cannot serve is refused at startup, without repeating
// the URL, which may hold credentials.
func TestParseFlags_RejectsURLsThatAreNotHTTP(t *testing.T) {
	for _, rpcURL := range []string{
		"localhost:9091/transmission/rpc",
		"ftp://transmission/rpc",
		"http://",
		"http//user:secret@transmission:9091/transmission/rpc",
	} {
		fs := flag.NewFlagSet("test", flag.ContinueOnError)
		_, err := ParseFlags(fs, []string{"-token=t", "-master=12345", "-url=" + rpcURL}, func(string) string { return "" })
		if err == nil {
			t.Errorf("%q accepted", rpcURL)
			continue
		}
		if strings.Contains(err.Error(), "secret") {
			t.Errorf("error repeats the URL's password: %v", err)
		}
	}
	for _, rpcURL := range []string{"https://user:secret@seedbox.example/transmission/rpc", "http://192.168.1.2:9091/transmission/rpc"} {
		fs := flag.NewFlagSet("test", flag.ContinueOnError)
		if _, err := ParseFlags(fs, []string{"-token=t", "-master=12345", "-url=" + rpcURL}, func(string) string { return "" }); err != nil {
			t.Errorf("%q refused: %v", rpcURL, err)
		}
	}
}

// The watcher's interval and disk threshold have defaults, and take sizes
// as people write them.
func TestParseFlags_WatchSettings(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	cfg, err := ParseFlags(fs, []string{"-token=t", "-master=12345"}, func(string) string { return "" })
	if err != nil || cfg.WatchInterval != 10*time.Second || cfg.LowDisk != 5_000_000_000 {
		t.Fatalf("defaults = %v, %d, %v", cfg.WatchInterval, cfg.LowDisk, err)
	}
	fs = flag.NewFlagSet("test", flag.ContinueOnError)
	cfg, err = ParseFlags(fs, []string{"-token=t", "-master=12345", "-watch-interval=1m", "-low-disk=500 MiB"}, func(string) string { return "" })
	if err != nil || cfg.WatchInterval != time.Minute || cfg.LowDisk != 500<<20 {
		t.Fatalf("given = %v, %d, %v", cfg.WatchInterval, cfg.LowDisk, err)
	}
	for _, args := range [][]string{{"-watch-interval=100ms"}, {"-low-disk=lots"}} {
		fs = flag.NewFlagSet("test", flag.ContinueOnError)
		if _, err := ParseFlags(fs, append([]string{"-token=t", "-master=12345"}, args...), func(string) string { return "" }); err == nil {
			t.Errorf("%v accepted", args)
		}
	}
	fs = flag.NewFlagSet("test", flag.ContinueOnError)
	if cfg, err = ParseFlags(fs, []string{"-token=t", "-master=12345", "-low-disk=0"}, func(string) string { return "" }); err != nil || cfg.LowDisk != 0 {
		t.Fatalf("-low-disk=0 = %d, %v", cfg.LowDisk, err)
	}
}

// Masters come from -master, or else TT_MASTERS, separated by commas or
// spaces; the placeholder 1 is the setup mode.
func TestParseFlags_MastersFromEnvironment(t *testing.T) {
	env := func(key string) string {
		if key == "TT_MASTERS" {
			return "123, @Alice 456"
		}
		return ""
	}
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	cfg, err := ParseFlags(fs, []string{"-token=t"}, env)
	if err != nil || !reflect.DeepEqual(cfg.Masters, MasterSlice{"123", "alice", "456"}) {
		t.Fatalf("masters = %v, %v", cfg.Masters, err)
	}
	fs = flag.NewFlagSet("test", flag.ContinueOnError)
	cfg, err = ParseFlags(fs, []string{"-token=t", "-master=789"}, env)
	if err != nil || !reflect.DeepEqual(cfg.Masters, MasterSlice{"789"}) {
		t.Fatalf("-master did not replace TT_MASTERS: %v, %v", cfg.Masters, err)
	}
	if cfg.Masters.Setup() || !(MasterSlice{SetupMaster}).Setup() || (MasterSlice{SetupMaster, "2"}).Setup() {
		t.Fatal("Setup misreads the masters")
	}
}

// The state file comes from -state, or else TT_STATE.
func TestParseFlags_StateFile(t *testing.T) {
	env := func(key string) string {
		if key == "TT_STATE" {
			return "/config/state.json"
		}
		return ""
	}
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	cfg, err := ParseFlags(fs, []string{"-token=t", "-master=12345"}, env)
	if err != nil || cfg.StateFile != "/config/state.json" {
		t.Fatalf("StateFile = %q, %v; want TT_STATE's", cfg.StateFile, err)
	}
	fs = flag.NewFlagSet("test", flag.ContinueOnError)
	cfg, err = ParseFlags(fs, []string{"-token=t", "-master=12345", "-state=/data/state.json"}, env)
	if err != nil || cfg.StateFile != "/data/state.json" {
		t.Fatalf("StateFile = %q, %v; want the flag's", cfg.StateFile, err)
	}
}

// -transmission-logfile no longer does anything, but existing service
// definitions pass it, so it must still parse.
func TestParseFlags_AcceptsDeprecatedTransmissionLogfile(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	cfg, err := ParseFlags(fs, []string{"-token=t", "-master=12345", "-transmission-logfile=/var/log/transmission.log"}, nil)
	if err != nil {
		t.Fatalf("deprecated flag refused: %v", err)
	}
	if cfg.TransLogFile != "/var/log/transmission.log" {
		t.Fatalf("TransLogFile = %q; main needs it to warn", cfg.TransLogFile)
	}
}

// -version must work before the bot is configured at all.
func TestParseFlags_VersionNeedsNoOtherSettings(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	cfg, err := ParseFlags(fs, []string{"-version"}, func(string) string { return "" })
	if err != nil {
		t.Fatalf("-version alone failed: %v", err)
	}
	if !cfg.ShowVersion {
		t.Fatal("-version did not ask for the version")
	}
}

func TestParseFlags_RejectsEmptyMaster(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	_, err := ParseFlags(fs, []string{"-token=test-token", "-master=@"}, nil)
	if err == nil || !strings.Contains(err.Error(), "master cannot be empty") {
		t.Fatalf("expected empty master error, got %v", err)
	}
}

func TestParseFlags_MissingMandatory(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	_, err := ParseFlags(fs, []string{}, nil)
	if err == nil {
		t.Fatalf("expected error when mandatory flags are missing")
	}

	fs2 := flag.NewFlagSet("test", flag.ContinueOnError)
	_, err = ParseFlags(fs2, []string{"-token=tok"}, nil)
	if err == nil {
		t.Fatalf("expected error when master is missing")
	}

	fs3 := flag.NewFlagSet("test", flag.ContinueOnError)
	_, err = ParseFlags(fs3, []string{"-master=user"}, nil)
	if err == nil {
		t.Fatalf("expected error when token is missing")
	}
}
