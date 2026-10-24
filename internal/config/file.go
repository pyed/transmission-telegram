package config

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"runtime"
	"strings"
)

// nonSettings are the flags that act rather than configure, which a config
// file cannot hold.
var nonSettings = map[string]bool{"config": true, "install": true, "uninstall": true, "version": true}

// secretSettings are the settings that hold a secret.
var secretSettings = map[string]bool{"token": true, "password": true}

// applyConfigFile sets the flags named in the config file at path, except
// those given on the command line. Each line is a flag name and its value,
// as "url = http://localhost:9091/transmission/rpc", or a true-or-false
// flag's name alone; blank lines and lines starting with # are ignored. It
// returns a warning when the file holds a secret that other users may read.
func applyConfigFile(fs *flag.FlagSet, path string) (warning string, err error) {
	given := make(map[string]bool)
	fs.Visit(func(f *flag.Flag) { given[f.Name] = true })
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("-config: %w", err)
	}
	defer file.Close()
	secret := false
	scanner := bufio.NewScanner(file)
	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimSpace(scanner.Text())
		if line == 1 {
			text = strings.TrimPrefix(text, "\uFEFF") // as Notepad writes
		}
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		name, value, hasValue := strings.Cut(text, "=")
		name, value = strings.TrimSpace(name), strings.TrimSpace(value)
		f := fs.Lookup(name)
		switch {
		case (f == nil || nonSettings[name]) && !hasValue:
			// Not quoted: a line without a name may be a secret.
			return "", fmt.Errorf("%s:%d: expected a setting and its value, such as url = %s", path, line, defaultRPCURL)
		case f == nil || nonSettings[name]:
			return "", fmt.Errorf("%s:%d: %q is not a setting; transmission-telegram -h lists them", path, line, name)
		case !hasValue && !isBoolFlag(f):
			return "", fmt.Errorf("%s:%d: %s needs a value, as in %s = …", path, line, name, name)
		case !hasValue:
			value = "true"
		}
		secret = secret || secretSettings[name]
		if given[name] {
			continue
		}
		if err := fs.Set(name, value); err != nil {
			return "", fmt.Errorf("%s:%d: %s: %w", path, line, name, err)
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("-config: %w", err)
	}
	if secret && runtime.GOOS != "windows" {
		if info, err := file.Stat(); err == nil && info.Mode().Perm()&0o077 != 0 {
			return "The config file " + path + " holds a secret, and other users may read it; make it private with chmod 600", nil
		}
	}
	return "", nil
}

// isBoolFlag reports whether f is a true-or-false flag, which a command
// line may give without a value.
func isBoolFlag(f *flag.Flag) bool {
	boolFlag, ok := f.Value.(interface{ IsBoolFlag() bool })
	return ok && boolFlag.IsBoolFlag()
}
