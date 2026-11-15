package service

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/pyed/transmission-telegram/v2/internal/config"
)

// testInstall is a host whose system lives under a temporary directory,
// with the user alice, whose commands are recorded.
type testInstall struct {
	host     host
	commands []string
	out      bytes.Buffer
	// failing makes the commands that start with one of them fail.
	failing []string
}

func newTestInstall(t *testing.T, goos string, admin bool) *testInstall {
	t.Helper()
	prefix := t.TempDir()
	home := filepath.Join(prefix, "home", "alice")
	ti := &testInstall{}
	ti.host = host{
		goos: goos, stdout: &ti.out, admin: admin, uid: 501,
		account:    account{name: "alice", home: home, configDir: userConfigDir(goos, home), uid: -1, gid: -1},
		executable: "/opt/transmission-telegram/transmission-telegram",
		prefix:     prefix,
		command: func(name string, args ...string) error {
			command := name + " " + strings.Join(args, " ")
			ti.commands = append(ti.commands, command)
			for _, failing := range ti.failing {
				if strings.HasPrefix(command, failing) {
					return errors.New("failed")
				}
			}
			return nil
		},
		check: func(context.Context) error { return nil },
	}
	if err := os.MkdirAll(filepath.Join(prefix, "run", "systemd", "system"), 0o755); err != nil {
		t.Fatal(err)
	}
	return ti
}

// parse reads the settings of args and of the environment env.
func parse(t *testing.T, args []string, env map[string]string) *config.Config {
	t.Helper()
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	cfg, err := config.ParseFlags(fs, args, func(name string) string { return env[name] })
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// installConfig is -install with args, and the bot token, master and URL in
// the environment.
func installConfig(t *testing.T, args ...string) *config.Config {
	t.Helper()
	return parse(t, append([]string{"-install"}, args...),
		map[string]string{"TT_BOT_TOKEN": "123:SECRET", "TT_MASTERS": "7", "TR_URL": "http://nas:9091/transmission/rpc"})
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestInstallSystemdUserService(t *testing.T) {
	ti := newTestInstall(t, "linux", false)
	if err := ti.host.install(context.Background(), installConfig(t, "-no-live", "-password=p w")); err != nil {
		t.Fatal(err)
	}
	configDir := filepath.Join(ti.host.account.home, ".config")
	configPath := filepath.Join(configDir, "transmission-telegram", "transmission-telegram.conf")
	statePath := filepath.Join(configDir, "transmission-telegram", "state.json")
	settings := readFile(t, configPath)
	for _, want := range []string{"\nmaster = 7\nno-live = true\npassword = p w\n", "\ntoken = 123:SECRET\n",
		"\nurl = http://nas:9091/transmission/rpc\n", "\nstate = " + statePath + "\n"} {
		if !strings.Contains(settings, want) {
			t.Errorf("config file lacks %q:\n%s", want, settings)
		}
	}
	if info, err := os.Stat(configPath); err != nil || (runtime.GOOS != "windows" && info.Mode().Perm() != 0o600) {
		t.Fatalf("config file mode = %v, %v", info.Mode(), err)
	}
	unit := readFile(t, filepath.Join(configDir, "systemd", "user", "transmission-telegram.service"))
	for _, want := range []string{`ExecStart="/opt/transmission-telegram/transmission-telegram" -config ` + systemdArg(configPath) + "\n",
		"Restart=on-failure\n", "WantedBy=default.target\n", "NoNewPrivileges=yes\n"} {
		if !strings.Contains(unit, want) {
			t.Errorf("unit lacks %q:\n%s", want, unit)
		}
	}
	if strings.Contains(unit, "User=") || strings.Contains(unit, "network-online") || strings.Contains(unit, "ProtectSystem") {
		t.Errorf("a user unit names a user or a system target, or sandboxes as a system one:\n%s", unit)
	}
	if got := strings.Join(ti.commands, "; "); got != "systemctl --user daemon-reload; systemctl --user enable transmission-telegram.service; "+
		"systemctl --user restart transmission-telegram.service; loginctl enable-linger alice" {
		t.Fatalf("commands = %s", got)
	}
	if out := ti.out.String(); !strings.Contains(out, "It starts at boot.") || !strings.Contains(out, "journalctl --user -u transmission-telegram -f") {
		t.Errorf("output = %s", out)
	}

	// The saved file is enough to run the bot.
	cfg := parse(t, []string{"-config", configPath}, nil)
	if cfg.BotToken != "123:SECRET" || cfg.StateFile != statePath || !cfg.NoLive || cfg.Password != "p w" || cfg.RewriteConfig {
		t.Fatalf("running from the config file: %+v", cfg)
	}

	ti.commands = nil
	if err := ti.host.uninstall(); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(ti.commands, "; "); got != "systemctl --user disable --now transmission-telegram.service; systemctl --user daemon-reload" {
		t.Fatalf("uninstall commands = %s", got)
	}
	if _, err := os.Stat(filepath.Join(configDir, "systemd", "user", "transmission-telegram.service")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the unit is still there: %v", err)
	}
	if _, err := os.Stat(configPath); err != nil {
		t.Fatalf("uninstall removed the settings: %v", err)
	}
	if err := ti.host.uninstall(); err == nil || !strings.Contains(err.Error(), "no transmission-telegram service") {
		t.Fatalf("a second uninstall = %v", err)
	}
}

func TestInstallSystemdWithoutLingerStartsAtLogin(t *testing.T) {
	ti := newTestInstall(t, "linux", false)
	ti.failing = []string{"loginctl"}
	if err := ti.host.install(context.Background(), installConfig(t)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ti.out.String(), "It starts when you log in. To start it at boot, run: sudo loginctl enable-linger alice") {
		t.Fatalf("output = %s", ti.out.String())
	}
}

func TestInstallSystemdSystemService(t *testing.T) {
	ti := newTestInstall(t, "linux", true)
	if err := ti.host.install(context.Background(), installConfig(t)); err != nil {
		t.Fatal(err)
	}
	unitPath := filepath.Join(ti.host.prefix, "etc", "systemd", "system", "transmission-telegram.service")
	unit := readFile(t, unitPath)
	for _, want := range []string{"User=alice\n", "After=network-online.target\n", "WantedBy=multi-user.target\n",
		"NoNewPrivileges=yes\n", "ProtectSystem=full\n", "RestrictSUIDSGID=yes\n"} {
		if !strings.Contains(unit, want) {
			t.Errorf("system unit lacks %q:\n%s", want, unit)
		}
	}
	if strings.Contains(ti.out.String(), "runs as root") {
		t.Errorf("warned of root for alice's service: %s", ti.out.String())
	}
	if got := strings.Join(ti.commands, "; "); got != "systemctl daemon-reload; systemctl enable transmission-telegram.service; "+
		"systemctl restart transmission-telegram.service" {
		t.Fatalf("commands = %s", got)
	}
	// Without root, uninstall points at sudo.
	user := ti.host
	user.admin = false
	if err := user.uninstall(); err == nil || !strings.Contains(err.Error(), "sudo transmission-telegram -uninstall") {
		t.Fatalf("uninstall without root = %v", err)
	}
	if err := ti.host.uninstall(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(unitPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the unit is still there: %v", err)
	}
}

// A system service installed as root, not through sudo from an account,
// runs as root, and -install says so.
func TestInstallAsRootSaysSo(t *testing.T) {
	for _, goos := range []string{"linux", "darwin"} {
		ti := newTestInstall(t, goos, true)
		ti.host.account.name = "root"
		if err := ti.host.install(context.Background(), installConfig(t)); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(ti.out.String(), "Note: it runs as root") {
			t.Errorf("%s output = %s", goos, ti.out.String())
		}
	}
	unit := systemdUnit("/opt/transmission-telegram", "/etc/transmission-telegram.conf", true, "root")
	if strings.Contains(unit, "User=") {
		t.Errorf("a unit for root names a user:\n%s", unit)
	}
}

func TestInstallChecksBeforeWritingAnything(t *testing.T) {
	ti := newTestInstall(t, "linux", false)
	ti.host.check = func(context.Context) error { return errors.New("transmission: connection refused") }
	if err := ti.host.install(context.Background(), installConfig(t)); err == nil || !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("install = %v", err)
	}
	if _, err := os.Stat(filepath.Join(ti.host.account.home, ".config")); !errors.Is(err, os.ErrNotExist) || len(ti.commands) != 0 {
		t.Fatalf("a failed check left files (%v) or ran %v", err, ti.commands)
	}

	ti = newTestInstall(t, "linux", false)
	if err := os.RemoveAll(filepath.Join(ti.host.prefix, "run", "systemd")); err != nil {
		t.Fatal(err)
	}
	if err := ti.host.install(context.Background(), installConfig(t)); err == nil || !strings.Contains(err.Error(), "systemd") {
		t.Fatalf("install without systemd = %v", err)
	}
	ti.host.goos = "plan9"
	if err := ti.host.install(context.Background(), installConfig(t)); err == nil || !strings.Contains(err.Error(), "plan9") {
		t.Fatalf("install on plan9 = %v", err)
	}
	ti.host.goos, ti.host.executable = "linux", filepath.Join(os.TempDir(), "go-build1", "exe", "transmission-telegram")
	if err := ti.host.install(context.Background(), installConfig(t)); err == nil || !strings.Contains(err.Error(), "temporary build") {
		t.Fatalf("install from go run = %v", err)
	}
}

func TestInstallKeepsAHandWrittenConfigFile(t *testing.T) {
	ti := newTestInstall(t, "linux", false)
	path := filepath.Join(t.TempDir(), "my.conf")
	text := "# mine\ntoken = 123:SECRET\nmaster = 7\nstate = /var/lib/transmission-telegram/state.json\n"
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ti.host.install(context.Background(), parse(t, []string{"-install", "-config", path}, nil)); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, path); got != text {
		t.Fatalf("install rewrote the config file:\n%s", got)
	}
	unit := readFile(t, filepath.Join(ti.host.account.configDir, "systemd", "user", "transmission-telegram.service"))
	if !strings.Contains(unit, "-config "+systemdArg(path)+"\n") {
		t.Fatalf("the unit does not use the given config file:\n%s", unit)
	}

	// A flag beside it is saved there too, with what the file held.
	if err := ti.host.install(context.Background(), parse(t, []string{"-install", "-config", path, "-no-live"}, nil)); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, path); !strings.Contains(got, "\nno-live = true\n") || !strings.Contains(got, "\ntoken = 123:SECRET\n") {
		t.Fatalf("config file:\n%s", got)
	}
	// So is a setting from the environment.
	if err := ti.host.install(context.Background(), parse(t, []string{"-install", "-config", path}, map[string]string{"TR_AUTH": "u:p"})); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, path); !strings.Contains(got, "\npassword = p\n") || !strings.Contains(got, "\nusername = u\n") {
		t.Fatalf("config file:\n%s", got)
	}
}

// -install -config FILE writes FILE when there is none yet.
func TestInstallWritesTheConfigFileGiven(t *testing.T) {
	ti := newTestInstall(t, "linux", false)
	path := filepath.Join(t.TempDir(), "conf", "bot.conf")
	if err := ti.host.install(context.Background(), installConfig(t, "-config", path)); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, path); !strings.Contains(got, "\ntoken = 123:SECRET\n") {
		t.Fatalf("config file:\n%s", got)
	}
}

// A setting the config file cannot hold as it is stops -install.
func TestInstallRefusesSettingsAConfigFileCannotHold(t *testing.T) {
	ti := newTestInstall(t, "linux", false)
	for _, password := range []string{" p", "p ", "a\nb"} {
		err := ti.host.install(context.Background(), installConfig(t, "-password="+password))
		if err == nil || !strings.Contains(err.Error(), "the password setting cannot be saved") {
			t.Errorf("password %q: %v", password, err)
		}
	}
	if _, err := os.Stat(filepath.Join(ti.host.account.home, ".config")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a refused install wrote files: %v", err)
	}
}

func TestSystemdArgEscapesSpecifiersAndQuotes(t *testing.T) {
	if got := systemdArg(`/opt/my "bots"/50%$HOME\x`); got != `"/opt/my \"bots\"/50%%$$HOME\\x"` {
		t.Fatalf("systemdArg = %s", got)
	}
}

func TestInstallLaunchd(t *testing.T) {
	ti := newTestInstall(t, "darwin", false)
	// Over SSH there is no desktop session to load into.
	ti.failing = []string{"launchctl bootstrap gui/"}
	if err := ti.host.install(context.Background(), installConfig(t)); err != nil {
		t.Fatal(err)
	}
	home := ti.host.account.home
	configPath := filepath.Join(home, "Library", "Application Support", "transmission-telegram", "transmission-telegram.conf")
	plistPath := filepath.Join(home, "Library", "LaunchAgents", "com.github.pyed.transmission-telegram.plist")
	plist := readFile(t, plistPath)
	for _, want := range []string{"<string>/opt/transmission-telegram/transmission-telegram</string>", "<string>" + xmlEscape(configPath) + "</string>",
		"<key>RunAtLoad</key>", filepath.Join(home, "Library", "Logs", "transmission-telegram.log")} {
		if !strings.Contains(plist, want) {
			t.Errorf("plist lacks %q:\n%s", want, plist)
		}
	}
	if strings.Contains(plist, "UserName") {
		t.Errorf("an agent names a user:\n%s", plist)
	}
	label := "com.github.pyed.transmission-telegram"
	if got := strings.Join(ti.commands, "; "); got != "launchctl bootout gui/501/"+label+"; launchctl bootstrap gui/501 "+plistPath+
		"; launchctl bootout user/501/"+label+"; launchctl bootstrap user/501 "+plistPath {
		t.Fatalf("commands = %s", got)
	}
	if !strings.Contains(ti.out.String(), "launchctl kickstart -k user/501/"+label) {
		t.Errorf("output = %s", ti.out.String())
	}
	if err := ti.host.uninstall(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(plistPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the plist is still there: %v", err)
	}

	daemon := newTestInstall(t, "darwin", true)
	if err := daemon.host.install(context.Background(), installConfig(t)); err != nil {
		t.Fatal(err)
	}
	plist = readFile(t, filepath.Join(daemon.host.prefix, "Library", "LaunchDaemons", label+".plist"))
	if !strings.Contains(plist, "<key>UserName</key>\n\t<string>alice</string>") {
		t.Fatalf("daemon plist:\n%s", plist)
	}
	if got := daemon.commands[len(daemon.commands)-1]; !strings.HasPrefix(got, "launchctl bootstrap system ") {
		t.Fatalf("the daemon was loaded with %s", got)
	}
}

// The Windows service runs as LocalSystem, so it runs a copy of the program
// in a folder of ProgramData only administrators may change, with its
// settings, state, and logs, whatever -config said; the state 2.2.0 kept in
// the user's config directory moves there.
func TestInstallWindowsService(t *testing.T) {
	ti := newTestInstall(t, "windows", true)
	ti.host.programData = filepath.Join(ti.host.prefix, "ProgramData")
	dir := filepath.Join(ti.host.programData, "transmission-telegram")
	var secured []string
	ti.host.secureDir = func(dir string) error {
		secured = append(secured, dir)
		return os.MkdirAll(dir, 0o700)
	}
	var installed []string
	ti.host.installWindows = func(source, target string, args []string) error {
		installed = append([]string{source, target}, args...)
		return nil
	}

	// 2.2.0 installed with its settings and state in the user's folder.
	earlier := filepath.Join(ti.host.account.configDir, "transmission-telegram")
	earlierConfig := filepath.Join(earlier, "transmission-telegram.conf")
	if err := os.MkdirAll(earlier, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(earlierConfig, []byte("token = 123:SECRET\nmaster = 7\nstate = "+filepath.Join(earlier, "state.json")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(earlier, "state.json"), []byte(`{"version":1,"lastChat":42}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ti.host.install(context.Background(), parse(t, []string{"-install", "-config", earlierConfig, "-logfile", `C:\Users\alice\bot.log`}, nil)); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "transmission-telegram.conf")
	if got, want := strings.Join(installed, " "), "/opt/transmission-telegram/transmission-telegram "+filepath.Join(dir, "transmission-telegram.exe")+
		" -config "+configPath; got != want {
		t.Fatalf("installed %s\nwant %s", got, want)
	}
	if len(secured) != 1 || secured[0] != dir {
		t.Fatalf("secured %q", secured)
	}
	if settings := readFile(t, configPath); !strings.Contains(settings, "\ntoken = 123:SECRET\n") ||
		!strings.Contains(settings, "\nstate = "+filepath.Join(dir, "state.json")+"\n") {
		t.Fatalf("settings:\n%s", settings)
	}
	if state := readFile(t, filepath.Join(dir, "state.json")); state != `{"version":1,"lastChat":42}` {
		t.Fatalf("the earlier state was not moved: %q", state)
	}
	out := ti.out.String()
	for _, want := range []string{"Logs: " + filepath.Join(dir, "transmission-telegram.log"), "run -install again",
		"the settings were copied from " + earlierConfig, `writes C:\Users\alice\bot.log (logfile) as LocalSystem, outside`} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}

	// Removing it removes the copy of the program, and keeps the settings.
	if err := os.WriteFile(filepath.Join(dir, "transmission-telegram.exe"), []byte("program"), 0o700); err != nil {
		t.Fatal(err)
	}
	ti.host.uninstallWindows = func() error { return nil }
	if err := ti.host.uninstall(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "transmission-telegram.exe")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the copy of the program is still there: %v", err)
	}
	if !strings.Contains(ti.out.String(), "The settings in "+dir+" were kept") {
		t.Errorf("output = %s", ti.out.String())
	}
	ti.host.uninstallWindows = func() error { return errors.New("no transmission-telegram service is installed") }
	if err := ti.host.uninstall(); err == nil {
		t.Fatal("a failed removal was reported as done")
	}

	// A folder that cannot be made private stops -install before anything
	// is installed.
	ti.host.secureDir = func(string) error { return errors.New("it belongs to EVIL\\mallory") }
	installed = nil
	if err := ti.host.install(context.Background(), installConfig(t)); err == nil || !strings.Contains(err.Error(), "mallory") || installed != nil {
		t.Fatalf("install into a folder of another user's = %v, installed %q", err, installed)
	}
}

// The program is copied to the service's folder, over an older copy, but
// not onto itself, when -install is run from the copy.
func TestCopyExecutable(t *testing.T) {
	dir := t.TempDir()
	source, target := filepath.Join(dir, "new.exe"), filepath.Join(dir, "service", "transmission-telegram.exe")
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, file := range []struct{ path, content string }{{source, "new program"}, {target, "old program"}} {
		if err := os.WriteFile(file.path, []byte(file.content), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := copyExecutable(source, target); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, target); got != "new program" {
		t.Fatalf("target = %q", got)
	}
	if err := copyExecutable(target, target); err != nil || readFile(t, target) != "new program" {
		t.Fatalf("copying the program onto itself: %v", err)
	}
	if entries, _ := os.ReadDir(filepath.Dir(target)); len(entries) != 1 {
		t.Fatalf("temporary files left behind: %v", entries)
	}
}

// As root for an account, as through sudo, -install writes the account's
// files in its home without following links out of it, which the account
// could plant as they are written to have root write anywhere.
func TestAccountFilesStayInTheAccountsHome(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no sudo, and its files are not given to another account")
	}
	home, outside := t.TempDir(), t.TempDir()
	alice := account{name: "alice", home: home, uid: os.Getuid(), gid: os.Getgid()}
	path := filepath.Join(home, ".config", "transmission-telegram", "transmission-telegram.conf")
	if err := writeAccountFile(path, "token = 123:SECRET\n", alice); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 || readFile(t, path) != "token = 123:SECRET\n" {
		t.Fatalf("the file: %v, %v", info, err)
	}
	if info, err := os.Stat(filepath.Dir(path)); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("its folder: %v, %v", info, err)
	}

	// A link out of the home directory is not followed.
	linked := t.TempDir()
	alice.home = linked
	if err := os.Symlink(outside, filepath.Join(linked, ".config")); err != nil {
		t.Fatal(err)
	}
	err := writeAccountFile(filepath.Join(linked, ".config", "transmission-telegram", "transmission-telegram.conf"), "token = x\n", alice)
	if err == nil {
		t.Fatal("followed a link out of the home directory")
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatalf("wrote outside the home directory: %v", entries)
	}

	// A file the administrator puts elsewhere is written there.
	elsewhere := filepath.Join(outside, "etc", "transmission-telegram.conf")
	if err := writeAccountFile(elsewhere, "token = y\n", alice); err != nil || readFile(t, elsewhere) != "token = y\n" {
		t.Fatalf("writing outside the home directory, as asked: %v", err)
	}
}

// -uninstall needs no settings, and cannot go with -install.
func TestUninstallNeedsNoSettings(t *testing.T) {
	if cfg := parse(t, []string{"-uninstall"}, nil); !cfg.Uninstall {
		t.Fatalf("-uninstall = %+v", cfg)
	}
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	if _, err := config.ParseFlags(fs, []string{"-install", "-uninstall"}, func(string) string { return "" }); err == nil {
		t.Fatal("-install with -uninstall was accepted")
	}
}
