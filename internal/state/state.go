// Package state keeps what the bot remembers across restarts in a JSON
// file: where notifications go, each chat's sort order, which torrents were
// not done yet, and the command menu it registered.
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sync"
)

// version is the format of the state file. Bump it only for incompatible
// changes: a file from a newer version is refused rather than misread.
const version = 1

// Data is everything the bot remembers across restarts.
type Data struct {
	Version int `json:"version"`
	// LastChat is the chat a master used last. Notifications go there
	// until a chat chooses its own with /notify.
	LastChat int64 `json:"lastChat,omitempty"`
	// Notify holds the chats that chose notifications with /notify, with
	// the kinds each wants, possibly none. Once it has any chat, LastChat
	// no longer gets notifications by default.
	Notify map[int64][]string `json:"notify,omitempty"`
	// Incomplete holds the info-hashes of the torrents that were not done
	// at the watcher's last look, so that torrents finishing while the bot
	// is down are announced when it is back.
	Incomplete []string `json:"incomplete,omitempty"`
	// Sorts holds each chat's sort order, as /sort takes it: "size" or
	// "rev size".
	Sorts map[int64]string `json:"sorts,omitempty"`
	// Menu is the commands of the menu the bot last registered, so that it
	// can tell its own menu from one set in BotFather.
	Menu []string `json:"menu,omitempty"`
}

// Store holds Data and saves it after every update. A Store without a path
// keeps everything in memory only. It is safe for concurrent use.
type Store struct {
	path string
	mu   sync.Mutex
	data Data
}

// DefaultPath is transmission-telegram/state.json in the user's
// configuration directory.
func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "transmission-telegram", "state.json"), nil
}

// Load reads the state file at path, or starts empty when there is none
// yet. An empty path keeps the state in memory. A file that cannot be read
// is an error rather than a fresh start, so a damaged file never silently
// loses what the bot remembered.
func Load(path string) (*Store, error) {
	s := &Store{path: path, data: Data{Version: version}}
	if path == "" {
		return s, nil
	}
	content, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read the state file: %w", err)
	}
	if err := json.Unmarshal(content, &s.data); err != nil {
		return nil, fmt.Errorf("the state file %s is damaged (move it aside to start afresh): %w", path, err)
	}
	if s.data.Version > version {
		return nil, fmt.Errorf("the state file %s is from a newer transmission-telegram (format %d)", path, s.data.Version)
	}
	s.data.Version = version
	return s, nil
}

// Path is where the state is saved; empty when it is kept in memory.
func (s *Store) Path() string {
	return s.path
}

// Read calls fn with the current state, which fn must not keep.
func (s *Store) Read(fn func(*Data)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(&s.data)
}

// Update applies change and saves the result. When saving fails, the change
// is kept in memory and the error returned for the caller to report.
func (s *Store) Update(change func(*Data)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	change(&s.data)
	return s.save()
}

// save writes the state atomically: to a private temporary file in the
// same directory, which then replaces the old file.
func (s *Store) save() error {
	if s.path == "" {
		return nil
	}
	content, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return fmt.Errorf("encode the state: %w", err)
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create the state directory: %w", err)
	}
	temp, err := os.CreateTemp(dir, ".state-*.json")
	if err != nil {
		return fmt.Errorf("save the state: %w", err)
	}
	defer os.Remove(temp.Name()) // a no-op once renamed
	if _, err := temp.Write(append(content, '\n')); err != nil {
		temp.Close()
		return fmt.Errorf("save the state: %w", err)
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return fmt.Errorf("save the state: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("save the state: %w", err)
	}
	if err := os.Rename(temp.Name(), s.path); err != nil {
		return fmt.Errorf("save the state: %w", err)
	}
	syncDir(dir)
	return nil
}

// syncDir asks the system to write dir's entries to disk, so that a file
// just renamed into it is there after a power cut. It is best effort:
// Windows, and some file systems, cannot sync a directory.
func syncDir(dir string) {
	if runtime.GOOS == "windows" {
		return
	}
	if file, err := os.Open(dir); err == nil {
		file.Sync()
		file.Close()
	}
}
