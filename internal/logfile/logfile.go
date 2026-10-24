// Package logfile writes the bot's log to a private file that moves itself
// aside once it reaches a size limit.
package logfile

import (
	"errors"
	"os"
	"sync"
)

// MaxSize is how large a log file grows before it is moved aside to FILE.1,
// which replaces the one before, so logs take at most twice this.
const MaxSize = 10 << 20

// File is a log file that moves itself aside to path.1 once it reaches its
// size limit. It is safe for concurrent use.
type File struct {
	mu   sync.Mutex
	path string
	max  int64
	file *os.File
	size int64
}

// Open opens or creates the log at path, readable only by its owner, and
// appends to it.
func Open(path string, max int64) (*File, error) {
	l := &File{path: path, max: max}
	if err := l.open(); err != nil {
		return nil, err
	}
	return l, nil
}

func (l *File) open() error {
	file, err := os.OpenFile(l.path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	// Earlier versions created the log readable by everyone.
	_ = file.Chmod(0o600)
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return err
	}
	l.file, l.size = file, info.Size()
	return nil
}

// Write appends p, first moving the file aside if p would take it past the
// limit.
func (l *File) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file != nil && l.size > 0 && l.size+int64(len(p)) > l.max {
		// Windows renames no open file, so it is closed first. If the
		// rename fails, the log goes on in the same file.
		l.file.Close()
		os.Rename(l.path, l.path+".1")
		l.file = nil
	}
	if l.file == nil {
		if err := l.open(); err != nil {
			return 0, err
		}
	}
	n, err := l.file.Write(p)
	l.size += int64(n)
	return n, err
}

// Close closes the file.
func (l *File) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return errors.New("log file is not open")
	}
	err := l.file.Close()
	l.file = nil
	return err
}
