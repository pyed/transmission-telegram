// Package service has the system start transmission-telegram: a systemd
// unit on Linux, a launchd job on macOS, or a Windows service, each running
// "transmission-telegram -config FILE" with the settings -install saved
// there. On Windows it also runs the bot as that service.
package service

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"math/rand/v2"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/pyed/transmission-telegram/v2/internal/config"
)

const (
	// Name names the service for systemd and Windows.
	Name = "transmission-telegram"
	// launchdLabel names it for launchd.
	launchdLabel = "com.github.pyed.transmission-telegram"
	// configName is the name of the config file -install writes.
	configName = "transmission-telegram.conf"
	// logName is the name of the log a Windows service writes next to its
	// config file.
	logName = "transmission-telegram.log"
)

// host is the machine being installed on: who installs, for whom, and how
// to reach the service manager. Tests replace its parts.
type host struct {
	goos   string
	stdout io.Writer
	// admin reports whether the installer runs as root, or elevated on
	// Windows, which installs a service for the whole system.
	admin bool
	// account is whom the service runs as.
	account account
	// uid is the installer's own user ID; -1 on Windows.
	uid        int
	executable string
	// prefix goes before the absolute system paths the installer writes.
	prefix string
	// command runs a service manager command.
	command func(name string, args ...string) error
	// check tries the settings before anything is installed.
	check func(ctx context.Context) error
	// programData is Windows' folder for programs' data, which holds the
	// service's own folder; empty elsewhere.
	programData string
	// secureDir creates the Windows service's folder, or takes over the
	// one there is, so that only administrators and the system may change
	// what it holds.
	secureDir func(dir string) error
	// On Windows, these install and remove the service: installWindows
	// copies the program at source to target, and has the service run
	// target with args.
	installWindows   func(source, target string, args []string) error
	uninstallWindows func() error
}

// account is the user a service runs as.
type account struct {
	name string
	home string
	// configDir is where the user's settings live, as os.UserConfigDir
	// reports for them.
	configDir string
	// uid and gid own the files written for the account; -1 when the
	// installer is the account, whose files are already its own.
	uid, gid int
}

func newHost(stdout io.Writer) host {
	h := host{
		goos:             runtime.GOOS,
		stdout:           stdout,
		uid:              os.Getuid(),
		command:          runCommand,
		programData:      programDataDir(),
		secureDir:        secureServiceDir,
		installWindows:   installWindowsService,
		uninstallWindows: uninstallWindowsService,
	}
	h.admin = isAdmin()
	h.account.uid, h.account.gid = -1, -1
	if current, err := user.Current(); err == nil {
		h.account.name, h.account.home = current.Username, current.HomeDir
	}
	h.account.configDir, _ = os.UserConfigDir()
	// "sudo transmission-telegram -install" installs a system service that
	// runs as the user who ran sudo, with that user's settings.
	if sudoUser := os.Getenv("SUDO_USER"); h.admin && h.goos != "windows" && sudoUser != "" && sudoUser != "root" {
		if found, err := user.Lookup(sudoUser); err == nil {
			uid, uidErr := strconv.Atoi(found.Uid)
			gid, gidErr := strconv.Atoi(found.Gid)
			if uidErr == nil && gidErr == nil {
				h.account = account{name: found.Username, home: found.HomeDir, uid: uid, gid: gid,
					configDir: userConfigDir(h.goos, found.HomeDir)}
			}
		}
	}
	if exe, err := os.Executable(); err == nil {
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			exe = resolved
		}
		h.executable = exe
	}
	return h
}

// Install saves cfg's settings to a config file, and has the system start
// "transmission-telegram -config FILE" at boot, and now. check tries the
// settings first, so that a service that cannot work is not installed.
func Install(ctx context.Context, cfg *config.Config, check func(context.Context) error, stdout io.Writer) error {
	h := newHost(stdout)
	h.check = check
	return h.install(ctx, cfg)
}

// Uninstall stops the service and removes it, keeping its settings.
func Uninstall(stdout io.Writer) error {
	return newHost(stdout).uninstall()
}

// userConfigDir is os.UserConfigDir for a user with home directory home.
func userConfigDir(goos, home string) string {
	if goos == "darwin" {
		return filepath.Join(home, "Library", "Application Support")
	}
	return filepath.Join(home, ".config")
}

func runCommand(name string, args ...string) error {
	output, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		if text := strings.TrimSpace(string(output)); text != "" {
			return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, text)
		}
		return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return nil
}

func (h host) path(name string) string {
	return filepath.Join(h.prefix, filepath.FromSlash(name))
}

func (h host) printf(format string, args ...any) {
	fmt.Fprintf(h.stdout, format+"\n", args...)
}

func (h host) install(ctx context.Context, cfg *config.Config) error {
	if h.executable == "" {
		return errors.New("-install: cannot find the transmission-telegram executable")
	}
	if strings.HasPrefix(h.executable, os.TempDir()) {
		return fmt.Errorf("-install: %s is a temporary build, as go run makes; install transmission-telegram and run -install from it", h.executable)
	}
	switch h.goos {
	case "linux":
		if _, err := os.Stat(h.path("/run/systemd/system")); err != nil {
			return errors.New("-install works with systemd, which this system does not run; have your init system run transmission-telegram -config FILE instead")
		}
	case "darwin", "windows":
	default:
		return fmt.Errorf("-install does not support %s; have your init system run transmission-telegram -config FILE instead", h.goos)
	}
	if h.account.configDir == "" {
		return errors.New("-install: cannot find the user's config directory")
	}
	if h.goos == "windows" && h.programData == "" {
		return errors.New("-install: cannot find Windows' ProgramData folder")
	}

	dir := h.settingsDir()
	configPath, err := filepath.Abs(cmp.Or(cfg.ConfigFile, filepath.Join(dir, configName)))
	if err != nil {
		return err
	}
	settings := maps.Clone(cfg.Settings)
	rewrite := cfg.RewriteConfig
	// The Windows service runs as LocalSystem, so what it runs and reads
	// is kept where only administrators may change it: in its own folder,
	// whatever -config said. A user's own files there could be changed by
	// any program of theirs, which would then run as LocalSystem.
	earlierState := filepath.Join(h.account.configDir, "transmission-telegram", "state.json")
	if h.goos == "windows" {
		if !samePath(configPath, filepath.Join(dir, configName)) {
			configPath, rewrite = filepath.Join(dir, configName), true
		}
		if samePath(settings["state"], earlierState) {
			settings["state"] = "" // where 2.2.0 kept it, which moves too
		}
	}
	if settings["state"] == "" {
		// The service may run with another home directory, or none, so it
		// is told where what the bot remembers is kept.
		settings["state"] = filepath.Join(dir, "state.json")
		rewrite = true
	}
	text, err := configText(settings)
	if err != nil {
		return err
	}
	if err := h.check(ctx); err != nil {
		return err
	}
	if h.goos == "windows" {
		if err := h.secureDir(dir); err != nil {
			return fmt.Errorf("-install: %w", err)
		}
		h.moveEarlierState(earlierState, settings["state"])
	}
	if _, statErr := os.Stat(configPath); rewrite || statErr != nil {
		if err := writeAccountFile(configPath, text, h.account); err != nil {
			return fmt.Errorf("save the settings: %w", err)
		}
		h.printf("Saved the settings to %s", configPath)
	}

	switch h.goos {
	case "linux":
		err = h.installSystemd(configPath)
	case "darwin":
		err = h.installLaunchd(configPath)
	default:
		exe := filepath.Join(dir, Name+".exe")
		if err := h.installWindows(h.executable, exe, []string{"-config", configPath}); err != nil {
			return err
		}
		h.printf("Installed and started the %s service, which starts with Windows.", Name)
		h.printf("It runs as LocalSystem, so only administrators may change its program, settings, and state, in %s.", dir)
		h.printf("It runs a copy of this program, %s: after upgrading transmission-telegram, run -install again.", exe)
		h.printf("Logs: %s", filepath.Join(dir, logName))
		h.printf("After editing %s from an elevated editor, restart it with: Restart-Service %s", configPath, Name)
		h.printf("Remove it with: transmission-telegram -uninstall")
		if cfg.ConfigFile != "" && !samePath(cfg.ConfigFile, configPath) {
			h.printf("Note: the settings were copied from %s, which the service does not read. Delete it, as it holds the bot token.", cfg.ConfigFile)
		}
		for _, name := range []string{"state", "logfile"} {
			if path := settings[name]; path != "" && !withinDir(dir, path) {
				h.printf("Note: the service writes %s (%s) as LocalSystem, outside %s, where other accounts or programs "+
					"may be able to change what it writes; keep it in %s.", path, name, dir, dir)
			}
		}
		return nil
	}
	if err == nil && h.admin && (h.account.name == "" || h.account.name == "root") {
		h.printf("Note: it runs as root. To run it as your own account, run sudo transmission-telegram -install from that account.")
	}
	return err
}

func (h host) uninstall() error {
	var err error
	switch h.goos {
	case "linux":
		err = h.uninstallSystemd()
	case "darwin":
		err = h.uninstallLaunchd()
	case "windows":
		if err = h.uninstallWindows(); err == nil {
			// The copy of the program the service ran; this program, when
			// it is that copy, cannot remove itself.
			exe := filepath.Join(h.settingsDir(), Name+".exe")
			if removeErr := os.Remove(exe); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				h.printf("Note: could not remove %s: %v", exe, removeErr)
			}
		}
	default:
		err = fmt.Errorf("-uninstall does not support %s", h.goos)
	}
	if err != nil {
		return err
	}
	h.printf("Removed the %s service. The settings in %s were kept; delete them if they are no longer needed.",
		Name, h.settingsDir())
	return nil
}

// settingsDir is where -install keeps the settings, and the state unless
// told otherwise: the user's config directory, or on Windows, the service's
// own folder in ProgramData.
func (h host) settingsDir() string {
	if h.goos == "windows" {
		return filepath.Join(h.programData, "transmission-telegram")
	}
	return filepath.Join(h.account.configDir, "transmission-telegram")
}

// moveEarlierState copies the state 2.2.0's Windows service kept in the
// user's config directory, from earlier, to its new place, to, unless
// there is a state there already.
func (h host) moveEarlierState(earlier, to string) {
	if samePath(earlier, to) {
		return
	}
	if _, err := os.Stat(to); !errors.Is(err, os.ErrNotExist) {
		return
	}
	content, err := os.ReadFile(earlier)
	if err != nil {
		return // nothing to move
	}
	if err := writePrivateFile(to, string(content), -1, -1); err != nil {
		h.printf("Note: could not copy the state from %s to %s: %v", earlier, to, err)
		return
	}
	h.printf("Copied the state from %s to %s.", earlier, to)
}

// samePath reports whether a and b name the same path, ignoring case, as
// Windows does.
func samePath(a, b string) bool {
	return a != "" && b != "" && strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

// withinDir reports whether path is dir or inside it, ignoring case.
func withinDir(dir, path string) bool {
	rel, err := filepath.Rel(strings.ToLower(filepath.Clean(dir)), strings.ToLower(filepath.Clean(path)))
	return err == nil && filepath.IsLocal(rel)
}

// copyExecutable copies the program at source to target, replacing it,
// unless they are the same file.
func copyExecutable(source, target string) error {
	if sourceInfo, err := os.Stat(source); err != nil {
		return err
	} else if targetInfo, err := os.Stat(target); err == nil && os.SameFile(sourceInfo, targetInfo) {
		return nil
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	temp, err := os.CreateTemp(filepath.Dir(target), ".transmission-telegram-*.exe")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name()) // a no-op once renamed
	_, err = io.Copy(temp, in)
	if err == nil {
		err = temp.Chmod(0o755)
	}
	if err == nil {
		err = temp.Sync()
	}
	if closeErr := temp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	// A service that has just stopped may hold its program open a moment.
	for attempt := 1; ; attempt++ {
		err = os.Rename(temp.Name(), target)
		if err == nil || attempt == 20 {
			return err
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// configText renders settings as a config file.
func configText(settings map[string]string) (string, error) {
	var text strings.Builder
	text.WriteString("# transmission-telegram settings: a flag name and its value per line (transmission-telegram -h lists them).\n" +
		"# This file holds the bot token, so only its owner may read it.\n")
	for _, name := range slices.Sorted(maps.Keys(settings)) {
		value := settings[name]
		if strings.ContainsAny(value, "\r\n") || value != strings.TrimSpace(value) {
			return "", fmt.Errorf("the %s setting cannot be saved to a config file: it spans lines, or starts or ends with a space", name)
		}
		fmt.Fprintf(&text, "%s = %s\n", name, value)
	}
	return text.String(), nil
}

// writeAccountFile replaces the file at path with content readable only by
// its owner, the account's. When -install runs as root for an account, as
// through sudo, a file in the account's home directory is written through a
// handle on that directory that no symbolic link can lead out of: the
// account may change its own directory as it is written, and a link could
// otherwise have root write, or give the account, a file anywhere.
func writeAccountFile(path, content string, account account) error {
	if account.uid != -1 && account.home != "" {
		if rel, err := filepath.Rel(account.home, path); err == nil && filepath.IsLocal(rel) {
			if err := writeWithin(account.home, rel, content, account.uid, account.gid); err != nil {
				return fmt.Errorf("%w (without following links out of %s)", err, account.home)
			}
			return nil
		}
	}
	return writePrivateFile(path, content, account.uid, account.gid)
}

// writeWithin replaces the file rel, inside home, with content readable
// only by uid, creating its missing directories, for uid and gid, without
// leaving home.
func writeWithin(home, rel, content string, uid, gid int) error {
	root, err := os.OpenRoot(home)
	if err != nil {
		return err
	}
	defer root.Close()
	dir := filepath.Dir(rel)
	if dir != "." {
		made := ""
		for _, part := range strings.Split(dir, string(filepath.Separator)) {
			made = filepath.Join(made, part)
			err := root.Mkdir(made, 0o700)
			switch {
			case err == nil:
				if err := root.Lchown(made, uid, gid); err != nil {
					return err
				}
			case !errors.Is(err, fs.ErrExist):
				return err
			}
		}
	}
	temp := filepath.Join(dir, fmt.Sprintf(".transmission-telegram-%d", rand.Uint64()))
	file, err := root.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer root.Remove(temp) // a no-op once renamed
	return finishPrivateFile(file, content, uid, gid, func() error { return root.Rename(temp, rel) })
}

// writePrivateFile replaces the file at path with content readable only by
// its owner, creating its directory if needed. When uid is not -1, the file
// and the directories it creates are given to uid and gid.
func writePrivateFile(path, content string, uid, gid int) error {
	dir := filepath.Dir(path)
	if err := mkdirOwned(dir, uid, gid); err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, ".transmission-telegram-*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name()) // a no-op once renamed
	return finishPrivateFile(temp, content, uid, gid, func() error { return os.Rename(temp.Name(), path) })
}

// finishPrivateFile writes content to a new file, makes it private, and
// gives it to uid and gid unless uid is -1, all through the open file
// rather than its name, which could come to name another file meanwhile;
// then it closes the file and puts it in place with rename.
func finishPrivateFile(file *os.File, content string, uid, gid int, rename func() error) error {
	_, err := file.WriteString(content)
	if err == nil {
		err = file.Chmod(0o600)
	}
	if err == nil && uid != -1 {
		err = file.Chown(uid, gid)
	}
	if err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = rename()
	}
	return err
}

// mkdirOwned creates dir and its missing parents, giving the ones it creates
// to uid and gid unless uid is -1. A directory replaced by a link as it is
// made is not followed: the link is given instead.
func mkdirOwned(dir string, uid, gid int) error {
	if info, err := os.Stat(dir); err == nil {
		if !info.IsDir() {
			return fmt.Errorf("%s is not a directory", dir)
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if parent := filepath.Dir(dir); parent != dir {
		if err := mkdirOwned(parent, uid, gid); err != nil {
			return err
		}
	}
	if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	if uid != -1 {
		return os.Lchown(dir, uid, gid)
	}
	return nil
}

// systemdArg quotes s as one argument of a systemd command line.
func systemdArg(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "%", "%%", "$", "$$").Replace(s) + `"`
}

// systemdUnit is the unit that runs exe with the config file. A system unit
// runs as account, after the network is up; a user unit runs as its owner.
func systemdUnit(exe, configPath string, system bool, account string) string {
	var unit strings.Builder
	unit.WriteString("# Written by transmission-telegram -install; remove it with transmission-telegram -uninstall.\n" +
		"[Unit]\nDescription=transmission-telegram, a Telegram bot for Transmission\n" +
		"Documentation=https://github.com/pyed/transmission-telegram\n")
	if system {
		unit.WriteString("Wants=network-online.target\nAfter=network-online.target\n")
	}
	fmt.Fprintf(&unit, "\n[Service]\nExecStart=%s -config %s\nRestart=on-failure\nRestartSec=15\n", systemdArg(exe), systemdArg(configPath))
	if system && account != "" && account != "root" {
		fmt.Fprintf(&unit, "User=%s\n", account)
	}
	// The bot talks to Telegram and Transmission and keeps its own files,
	// and needs nothing more: it may not gain privileges, and a system
	// service may not change the system's files, kernel, or control groups.
	// A user's service manager cannot sandbox further without privileges.
	unit.WriteString("NoNewPrivileges=yes\n")
	if system {
		unit.WriteString("ProtectSystem=full\nProtectKernelTunables=yes\nProtectKernelModules=yes\nProtectControlGroups=yes\nRestrictSUIDSGID=yes\n")
	}
	target := "default.target"
	if system {
		target = "multi-user.target"
	}
	fmt.Fprintf(&unit, "\n[Install]\nWantedBy=%s\n", target)
	return unit.String()
}

// systemdUnitPath is where the unit goes: the system's units for root, or
// the user's own.
func (h host) systemdUnitPath() string {
	if h.admin {
		return h.path("/etc/systemd/system/" + Name + ".service")
	}
	return filepath.Join(h.account.configDir, "systemd", "user", Name+".service")
}

func (h host) systemctl(args ...string) error {
	if !h.admin {
		args = append([]string{"--user"}, args...)
	}
	return h.command("systemctl", args...)
}

func (h host) installSystemd(configPath string) error {
	unitPath := h.systemdUnitPath()
	if err := os.MkdirAll(filepath.Dir(unitPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(unitPath, []byte(systemdUnit(h.executable, configPath, h.admin, h.account.name)), 0o644); err != nil {
		return fmt.Errorf("write the systemd unit: %w", err)
	}
	for _, args := range [][]string{{"daemon-reload"}, {"enable", Name + ".service"}, {"restart", Name + ".service"}} {
		if err := h.systemctl(args...); err != nil {
			if !h.admin {
				return fmt.Errorf("%w\nIf systemctl --user cannot reach your user manager, log in as this user directly rather than with su, "+
					"or run sudo transmission-telegram -install to install a system service", err)
			}
			return err
		}
	}
	scope, sudo := "--user ", ""
	if h.admin {
		scope, sudo = "", "sudo "
	}
	h.printf("Installed and started the %s service (%s).", Name, unitPath)
	if !h.admin {
		// A user's services start at boot only when the user lingers.
		if err := h.command("loginctl", "enable-linger", h.account.name); err != nil {
			h.printf("It starts when you log in. To start it at boot, run: sudo loginctl enable-linger %s", h.account.name)
		} else {
			h.printf("It starts at boot.")
		}
	} else {
		h.printf("It starts at boot, as %s.", cmp.Or(h.account.name, "root"))
	}
	h.printf("Logs: %sjournalctl %s-u %s -f", sudo, scope, Name)
	h.printf("After editing %s, restart it with: %ssystemctl %srestart %s", configPath, sudo, scope, Name)
	h.printf("Remove it with: %stransmission-telegram -uninstall", sudo)
	return nil
}

func (h host) uninstallSystemd() error {
	unitPath := h.systemdUnitPath()
	if _, err := os.Stat(unitPath); err != nil {
		if !h.admin {
			if _, err := os.Stat(h.path("/etc/systemd/system/" + Name + ".service")); err == nil {
				return errors.New("transmission-telegram is installed as a system service; remove it with sudo transmission-telegram -uninstall")
			}
		}
		return fmt.Errorf("no transmission-telegram service is installed (%s does not exist)", unitPath)
	}
	disableErr := h.systemctl("disable", "--now", Name+".service")
	if err := os.Remove(unitPath); err != nil {
		return err
	}
	if err := h.systemctl("daemon-reload"); err != nil {
		return err
	}
	if disableErr != nil {
		h.printf("Note: %s", disableErr)
	}
	return nil
}

// xmlEscape escapes s for an XML text node.
func xmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

// launchdPlist is the job that runs exe with the config file. A daemon runs
// as account; an agent runs as its owner.
func launchdPlist(exe, configPath, logPath string, daemon bool, account string) string {
	var plist strings.Builder
	plist.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<!-- Written by transmission-telegram -install; remove it with transmission-telegram -uninstall. -->
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>` + launchdLabel + `</string>
	<key>ProgramArguments</key>
	<array>
		<string>` + xmlEscape(exe) + `</string>
		<string>-config</string>
		<string>` + xmlEscape(configPath) + `</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<dict>
		<key>SuccessfulExit</key>
		<false/>
	</dict>
	<key>ThrottleInterval</key>
	<integer>15</integer>
	<key>StandardOutPath</key>
	<string>` + xmlEscape(logPath) + `</string>
	<key>StandardErrorPath</key>
	<string>` + xmlEscape(logPath) + `</string>
`)
	if daemon && account != "" && account != "root" {
		plist.WriteString("\t<key>UserName</key>\n\t<string>" + xmlEscape(account) + "</string>\n")
	}
	plist.WriteString("</dict>\n</plist>\n")
	return plist.String()
}

// launchdJob is where the job goes, its domain, and its log: a daemon for
// root, or an agent of the user's.
func (h host) launchdJob() (plistPath, domain, logPath string) {
	if h.admin {
		return h.path("/Library/LaunchDaemons/" + launchdLabel + ".plist"), "system", h.path("/Library/Logs/" + logName)
	}
	return filepath.Join(h.account.home, "Library", "LaunchAgents", launchdLabel+".plist"),
		"gui/" + strconv.Itoa(h.uid), filepath.Join(h.account.home, "Library", "Logs", logName)
}

func (h host) installLaunchd(configPath string) error {
	plistPath, domain, logPath := h.launchdJob()
	if err := os.MkdirAll(filepath.Dir(plistPath), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(plistPath, []byte(launchdPlist(h.executable, configPath, logPath, h.admin, h.account.name)), 0o644); err != nil {
		return fmt.Errorf("write the launchd job: %w", err)
	}
	// Reinstalling replaces a loaded job, so it is unloaded first.
	h.command("launchctl", "bootout", domain+"/"+launchdLabel)
	if err := h.command("launchctl", "bootstrap", domain, plistPath); err != nil {
		if h.admin {
			return err
		}
		// Without a desktop session, as over SSH, agents load into the
		// user's domain.
		domain = "user/" + strconv.Itoa(h.uid)
		h.command("launchctl", "bootout", domain+"/"+launchdLabel)
		if retryErr := h.command("launchctl", "bootstrap", domain, plistPath); retryErr != nil {
			return fmt.Errorf("%w; %w", err, retryErr)
		}
	}
	sudo := ""
	if h.admin {
		sudo = "sudo "
		h.printf("Installed and started %s (%s). It starts at boot, as %s.", launchdLabel, plistPath, cmp.Or(h.account.name, "root"))
	} else {
		h.printf("Installed and started %s (%s). It starts when you log in.", launchdLabel, plistPath)
	}
	h.printf("Logs: %s", logPath)
	h.printf("After editing %s, restart it with: %slaunchctl kickstart -k %s/%s", configPath, sudo, domain, launchdLabel)
	h.printf("Remove it with: %stransmission-telegram -uninstall", sudo)
	return nil
}

func (h host) uninstallLaunchd() error {
	plistPath, domain, _ := h.launchdJob()
	if _, err := os.Stat(plistPath); err != nil {
		if !h.admin {
			if _, err := os.Stat(h.path("/Library/LaunchDaemons/" + launchdLabel + ".plist")); err == nil {
				return errors.New("transmission-telegram is installed for the whole system; remove it with sudo transmission-telegram -uninstall")
			}
		}
		return fmt.Errorf("no transmission-telegram service is installed (%s does not exist)", plistPath)
	}
	h.command("launchctl", "bootout", domain+"/"+launchdLabel)
	if !h.admin {
		h.command("launchctl", "bootout", "user/"+strconv.Itoa(h.uid)+"/"+launchdLabel)
	}
	return os.Remove(plistPath)
}
