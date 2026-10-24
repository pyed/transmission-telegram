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
	"maps"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"

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
	// On Windows, these install and remove the service.
	installWindows   func(exe string, args []string) error
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

	dir := filepath.Join(h.account.configDir, "transmission-telegram")
	configPath, err := filepath.Abs(cmp.Or(cfg.ConfigFile, filepath.Join(dir, configName)))
	if err != nil {
		return err
	}
	settings := maps.Clone(cfg.Settings)
	rewrite := cfg.RewriteConfig
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
	if _, statErr := os.Stat(configPath); rewrite || statErr != nil {
		if err := writePrivateFile(configPath, text, h.account.uid, h.account.gid); err != nil {
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
		if err := h.installWindows(h.executable, []string{"-config", configPath}); err != nil {
			return err
		}
		h.printf("Installed and started the %s service, which starts with Windows.", Name)
		h.printf("Logs: %s", filepath.Join(filepath.Dir(configPath), logName))
		h.printf("After editing %s, restart it with: Restart-Service %s", configPath, Name)
		h.printf("Remove it with: transmission-telegram -uninstall")
		h.printf("Note: it runs as LocalSystem. To run it as your own account, set it under Log On in the " +
			"service's properties (services.msc), and restart it.")
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
		err = h.uninstallWindows()
	default:
		err = fmt.Errorf("-uninstall does not support %s", h.goos)
	}
	if err != nil {
		return err
	}
	h.printf("Removed the %s service. The settings in %s were kept; delete them if they are no longer needed.",
		Name, filepath.Join(h.account.configDir, "transmission-telegram"))
	return nil
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
	_, err = temp.WriteString(content)
	if err == nil {
		err = temp.Sync()
	}
	if closeErr := temp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Chmod(temp.Name(), 0o600)
	}
	if err == nil && uid != -1 {
		err = os.Chown(temp.Name(), uid, gid)
	}
	if err == nil {
		err = os.Rename(temp.Name(), path)
	}
	return err
}

// mkdirOwned creates dir and its missing parents, giving the ones it creates
// to uid and gid unless uid is -1.
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
		return os.Chown(dir, uid, gid)
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
