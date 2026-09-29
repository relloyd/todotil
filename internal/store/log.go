// Package store persists the event log as JSON Lines files and manages the
// instance lock and backups.
package store

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/gofrs/flock"

	"github.com/relloyd/todotil/internal/todo"
)

// FormatVersion is written in the header of every log file.
const FormatVersion = 1

// DefaultMaxFileSize is the size after which a new log file is started.
const DefaultMaxFileSize = 10 << 20

const filePattern = "events-*.jsonl"

// Log is an append-only event log split over numbered files in one
// directory. It is safe for concurrent use.
type Log struct {
	dir     string
	MaxSize int64

	mu   sync.Mutex
	f    *os.File
	size int64
	seq  int
}

// LoadResult carries what was read when opening a log.
type LoadResult struct {
	Events []todo.Event
	// Skipped counts lines that could not be parsed (for example a torn
	// final write after a crash).
	Skipped int
}

// Open reads every log file in dir and opens the newest for appending.
func Open(dir string) (*Log, LoadResult, error) {
	var res LoadResult
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, res, err
	}
	files, err := logFiles(dir)
	if err != nil {
		return nil, res, err
	}
	for _, name := range files {
		evs, skipped, err := readFile(filepath.Join(dir, name))
		if err != nil {
			return nil, res, err
		}
		res.Events = append(res.Events, evs...)
		res.Skipped += skipped
	}
	l := &Log{dir: dir, MaxSize: DefaultMaxFileSize}
	if len(files) == 0 {
		if err := l.startFile(1); err != nil {
			return nil, res, err
		}
		return l, res, nil
	}
	last := files[len(files)-1]
	if _, err := fmt.Sscanf(last, "events-%06d.jsonl", &l.seq); err != nil {
		return nil, res, fmt.Errorf("unexpected log file name %q", last)
	}
	if err := l.openAppend(filepath.Join(dir, last)); err != nil {
		return nil, res, err
	}
	return l, res, nil
}

// Dir returns the directory holding the log files.
func (l *Log) Dir() string { return l.dir }

// Close closes the current file.
func (l *Log) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return nil
	}
	err := l.f.Close()
	l.f = nil
	return err
}

// Append writes events as one write and syncs the file to disk.
func (l *Log) Append(events []todo.Event) error {
	var buf bytes.Buffer
	for _, e := range events {
		b, err := json.Marshal(e)
		if err != nil {
			return err
		}
		buf.Write(b)
		buf.WriteByte('\n')
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return errors.New("log is closed")
	}
	if l.size > 0 && l.size+int64(buf.Len()) > l.MaxSize {
		if err := l.f.Close(); err != nil {
			return err
		}
		l.f = nil
		if err := l.startFile(l.seq + 1); err != nil {
			return err
		}
	}
	n, err := l.f.Write(buf.Bytes())
	l.size += int64(n)
	if err != nil {
		return err
	}
	return l.f.Sync()
}

// startFile creates log file number seq with a header line.
func (l *Log) startFile(seq int) error {
	path := filepath.Join(l.dir, fmt.Sprintf("events-%06d.jsonl", seq))
	if err := l.openAppend(path); err != nil {
		return err
	}
	l.seq = seq
	if l.size > 0 {
		return nil
	}
	hdr, _ := json.Marshal(todo.Event{Time: time.Now(), Op: todo.OpMeta, Version: FormatVersion})
	n, err := l.f.Write(append(hdr, '\n'))
	l.size += int64(n)
	if err != nil {
		return err
	}
	if err := l.f.Sync(); err != nil {
		return err
	}
	return syncDir(l.dir)
}

func (l *Log) openAppend(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	l.f, l.size = f, st.Size()
	// A crash mid-write can leave a partial last line. Terminate it so the
	// next append starts on a fresh line; the fragment is skipped on load.
	if l.size > 0 {
		last := make([]byte, 1)
		if _, err := f.ReadAt(last, l.size-1); err != nil {
			return err
		}
		if last[0] != '\n' {
			n, err := f.Write([]byte{'\n'})
			l.size += int64(n)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func logFiles(dir string) ([]string, error) {
	matches, err := filepath.Glob(filepath.Join(dir, filePattern))
	if err != nil {
		return nil, err
	}
	names := make([]string, len(matches))
	for i, m := range matches {
		names[i] = filepath.Base(m)
	}
	slices.Sort(names)
	return names, nil
}

func readFile(path string) ([]todo.Event, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	var (
		events  []todo.Event
		skipped int
	)
	r := bufio.NewReader(f)
	for {
		line, err := r.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			var e todo.Event
			if jerr := json.Unmarshal(line, &e); jerr != nil || e.Op == "" {
				skipped++
			} else if e.Op == todo.OpMeta {
				if e.Version > FormatVersion {
					return nil, 0, fmt.Errorf("%s was written by a newer todotil (format %d)", filepath.Base(path), e.Version)
				}
			} else {
				events = append(events, e)
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, 0, err
		}
	}
	return events, skipped, nil
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	// Some platforms don't support syncing directories; that's not fatal.
	_ = d.Sync()
	return nil
}

// Lock is an exclusive lock preventing two instances from sharing data.
type Lock struct{ fl *flock.Flock }

// ErrLocked is returned when another instance holds the lock.
var ErrLocked = errors.New("another todotil instance is running")

// AcquireLock takes the lock file at path without blocking.
func AcquireLock(path string) (*Lock, error) {
	fl := flock.New(path)
	ok, err := fl.TryLock()
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrLocked
	}
	return &Lock{fl: fl}, nil
}

// Release unlocks.
func (l *Lock) Release() error { return l.fl.Unlock() }
