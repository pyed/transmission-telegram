// Package state keeps what the bot remembers across restarts in a JSON
// file: where notifications go, each chat's sort order, the status
// messages and digests of chats and topics, which torrents were not done
// yet, and the command menu it registered.
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
	"syscall"
)

// version is the newest format of the state file, which this version
// reads. Bump it only for incompatible changes: a file from a newer version
// is refused rather than misread.
//
// Format 2 names places in topics ("-100123:45") as format 1 cannot. A
// state with no such place is saved as format 1, which is the same but for
// that, so that an older version can still read it; that version ignores
// lastTopic, and sends to the chat itself instead.
const version = 2

// Data is everything the bot remembers across restarts.
type Data struct {
	Version int `json:"version"`
	// LastChat is the chat a master used last, and LastTopic its topic, if
	// any (Last). Notifications go there until a chat or topic chooses its
	// own with /notify.
	LastChat  int64 `json:"lastChat,omitempty"`
	LastTopic int   `json:"lastTopic,omitempty"`
	// Notify holds the chats and topics that chose notifications with
	// /notify, with the kinds each wants, possibly none. Once it has any,
	// the last chat no longer gets notifications by default.
	Notify map[Place][]string `json:"notify,omitempty"`
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
	// Status holds the message ID of each chat's or topic's status message
	// (/status).
	Status map[Place]int `json:"status,omitempty"`
	// Digest holds each chat's or topic's digest (/digest).
	Digest map[Place]Digest `json:"digest,omitempty"`
}

// Last is the chat, and topic, a master used last.
func (d *Data) Last() Place {
	return Place{Chat: d.LastChat, Topic: d.LastTopic}
}

// SetLast records the chat, and topic, a master used last.
func (d *Data) SetLast(p Place) {
	d.LastChat, d.LastTopic = p.Chat, p.Topic
}

// format is the oldest format that holds d: 2 when a topic has
// notifications, a status message, or a digest.
func (d *Data) format() int {
	inTopic := func(p Place) bool { return p.Topic != 0 }
	if anyKey(d.Notify, inTopic) || anyKey(d.Status, inTopic) || anyKey(d.Digest, inTopic) {
		return 2
	}
	return 1
}

func anyKey[V any](m map[Place]V, match func(Place) bool) bool {
	for key := range m {
		if match(key) {
			return true
		}
	}
	return false
}

// Digest is a chat's summary every day, week, or month.
type Digest struct {
	// Time is when it comes, as HH:MM in the bot's time zone.
	Time string `json:"time"`
	// Every is "weekly" or "monthly"; empty is daily.
	Every string `json:"every,omitempty"`
	// LastDue is the date the last digest was due, as 2006-01-02. It was
	// sent then, or as soon as the bot could, unless Telegram refused it.
	LastDue string `json:"lastDue"`
	// Since is when the next digest's period began, in Unix seconds: when
	// the last one was sent, or the digest was set.
	Since int64 `json:"since"`
	// Uploaded and Downloaded are Transmission's cumulative totals at
	// Since, which the next digest counts from.
	Uploaded   uint64 `json:"uploaded"`
	Downloaded uint64 `json:"downloaded"`
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
	s := &Store{path: path, data: Data{Version: 1}}
	if path == "" {
		return s, nil
	}
	content, err := os.ReadFile(path)
	// A path inside a file, which Unix reports as ENOTDIR rather than
	// absent, holds no state either; saving there fails, and says so.
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
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
	s.data.Version = s.data.format()
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

// MoveChat moves what is remembered of chat from to chat to, as when a
// group becomes a supergroup, which is a new chat: where notifications go,
// the sort order, and the digests, of the chat and its topics. What chat to
// already has is kept. The status messages end, since their IDs mean
// nothing in the new chat. It reports whether anything changed.
func (d *Data) MoveChat(from, to int64) bool {
	if from == to {
		return false
	}
	changed := false
	if d.LastChat == from {
		d.LastChat, changed = to, true
	}
	changed = moveKeys(d.Notify, from, to) || changed
	if order, ok := d.Sorts[from]; ok {
		if _, has := d.Sorts[to]; !has {
			d.Sorts[to] = order
		}
		delete(d.Sorts, from)
		changed = true
	}
	changed = moveKeys(d.Digest, from, to) || changed
	for place := range d.Status {
		if place.Chat == from {
			delete(d.Status, place)
			changed = true
		}
	}
	return changed
}

// moveKeys moves the entries of chat from's places to the same places in
// chat to, unless chat to has them already, and reports whether it moved
// any.
func moveKeys[V any](m map[Place]V, from, to int64) bool {
	moved := false
	for place, value := range m {
		if place.Chat != from {
			continue
		}
		delete(m, place)
		if _, has := m[Place{Chat: to, Topic: place.Topic}]; !has {
			m[Place{Chat: to, Topic: place.Topic}] = value
		}
		moved = true
	}
	return moved
}

// ForgetTopic forgets what was set up in a topic that is gone: it gets no
// notifications, which go nowhere else instead, and its digest and status
// message end. It reports whether anything changed.
func (d *Data) ForgetTopic(p Place) bool {
	if p.Topic == 0 {
		return false
	}
	changed := false
	if _, ok := d.Notify[p]; ok {
		delete(d.Notify, p)
		if len(d.Notify) == 0 {
			// Notifications must not go back to the chat a master used
			// last, as they do until any place chooses: the chat itself is
			// kept as having chosen none, as every place that has not
			// chosen gets.
			d.Notify[Place{Chat: p.Chat}] = []string{}
		}
		changed = true
	}
	if _, ok := d.Digest[p]; ok {
		delete(d.Digest, p)
		changed = true
	}
	if _, ok := d.Status[p]; ok {
		delete(d.Status, p)
		changed = true
	}
	if d.Last() == p {
		d.SetLast(Place{})
		changed = true
	}
	return changed
}
