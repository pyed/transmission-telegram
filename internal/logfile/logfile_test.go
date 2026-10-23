package logfile

import (
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// A log that reaches its limit moves aside to FILE.1, replacing the one
// before, and goes on in a new file; one that starts over its limit, from an
// earlier run, moves aside at the first line.
func TestLogMovesAsideAtItsLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transmission-telegram.log")
	if err := os.WriteFile(path, []byte(strings.Repeat("old\n", 30)), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := Open(path, 100)
	if err != nil {
		t.Fatal(err)
	}
	logger := log.New(file, "", 0)
	read := func(path string) string {
		t.Helper()
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(content)
	}
	logger.Print("first")
	if got, aside := read(path), read(path+".1"); got != "first\n" || aside != strings.Repeat("old\n", 30) {
		t.Fatalf("after the first line: %q, aside %q", got, aside)
	}
	for i := range 20 {
		logger.Printf("line %02d", i)
	}
	if got, aside := read(path), read(path+".1"); len(got) > 100 || len(aside) > 100 || !strings.HasSuffix(got, "line 19\n") ||
		strings.Contains(aside, "old") {
		t.Fatalf("after twenty lines: %q, aside %q", got, aside)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

// The log holds chat IDs and command names, so only its owner may read it,
// including a log an earlier version created readable by everyone.
func TestLogIsPrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no Unix permission bits")
	}
	dir := t.TempDir()
	fresh := filepath.Join(dir, "fresh.log")
	old := filepath.Join(dir, "old.log")
	if err := os.WriteFile(old, []byte("old\n"), 0o666); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(old, 0o666); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{fresh, old} {
		file, err := Open(path, MaxSize)
		if err != nil {
			t.Fatal(err)
		}
		file.Close()
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if mode := info.Mode().Perm(); mode != 0o600 {
			t.Errorf("%s has mode %o, want 600", filepath.Base(path), mode)
		}
	}
}
