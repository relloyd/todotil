// Package store persists the event log as JSON Lines files and manages
// locking and backups.
package store

import (
	"bufio"
	"bytes"
	"context"
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

// FormatVersion is written in the header of every log file. Version 2 added
// claims, notes and created_by/completed_by to items.
const FormatVersion = 2

// DefaultMaxFileSize is the size after which a new log file is started.
const DefaultMaxFileSize = 10 << 20

// DefaultLockTimeout bounds how long a writer waits for another process.
const DefaultLockTimeout = 10 * time.Second

const (
	filePattern = "events-*.jsonl"
	lockName    = ".lock"
)

// Log is an append-only event log split over numbered files in one
// directory. Several processes may share it: each holds an exclusive file
// lock only while reading new events and appending its own. A Log is safe
// for concurrent use by goroutines.
type Log struct {
	dir         string
	MaxSize     int64
	LockTimeout time.Duration

	mu   sync.Mutex
	lock *flock.Flock
	seq  int   // newest file read so far (0 if none)
	off  int64 // bytes of that file consumed
	ver  int   // header version of that file
}

// LoadResult carries what was read when opening a log.
type LoadResult struct {
	Events []todo.Event
	// Skipped counts lines that could not be parsed (for example a torn
	// final write after a crash).
	Skipped int
}

// ErrLockTimeout is returned when another process holds the lock too long.
var ErrLockTimeout = errors.New("timed out waiting for the data lock")

// Open reads every log file in dir.
func Open(dir string) (*Log, LoadResult, error) {
	var res LoadResult
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, res, err
	}
	l := &Log{
		dir:         dir,
		MaxSize:     DefaultMaxFileSize,
		LockTimeout: DefaultLockTimeout,
		lock:        flock.New(filepath.Join(dir, lockName)),
	}
	err := l.locked(func() error {
		evs, skipped, err := l.readNew()
		res.Events, res.Skipped = evs, skipped
		return err
	})
	if err != nil {
		return nil, res, err
	}
	return l, res, nil
}

// Dir returns the directory holding the log files.
func (l *Log) Dir() string { return l.dir }

// Close releases resources. The log holds no open files between calls.
func (l *Log) Close() error { return nil }

// locked runs fn holding both the in-process mutex and the file lock.
func (l *Log) locked(fn func() error) (err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), l.LockTimeout)
	defer cancel()
	ok, err := l.lock.TryLockContext(ctx, 5*time.Millisecond)
	if err != nil && !errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if !ok {
		return ErrLockTimeout
	}
	defer func() {
		if uerr := l.lock.Unlock(); err == nil {
			err = uerr
		}
	}()
	return fn()
}

// Poll returns events appended by other processes since the last read. It
// avoids taking the lock when nothing has changed.
func (l *Log) Poll() ([]todo.Event, error) {
	if !l.changed() {
		return nil, nil
	}
	var evs []todo.Event
	err := l.locked(func() error {
		var err error
		evs, _, err = l.readNew()
		return err
	})
	return evs, err
}

func (l *Log) changed() bool {
	l.mu.Lock()
	seq, off := l.seq, l.off
	l.mu.Unlock()
	files, err := logFiles(l.dir)
	if err != nil || len(files) == 0 {
		return err != nil
	}
	last := files[len(files)-1]
	if fileSeq(last) != seq {
		return true
	}
	st, err := os.Stat(filepath.Join(l.dir, last))
	return err != nil || st.Size() != off
}

// Update is the read-modify-write step. Holding the lock, it reads events
// other processes have appended, passes them to fn, and appends the events
// fn returns. Nothing is written if fn returns an error.
func (l *Log) Update(fn func(foreign []todo.Event) ([]todo.Event, error)) error {
	return l.locked(func() error {
		foreign, _, err := l.readNew()
		if err != nil {
			return err
		}
		out, err := fn(foreign)
		if err != nil || len(out) == 0 {
			return err
		}
		return l.append(out)
	})
}

// Append writes events. It is Update without looking at foreign events,
// which callers replaying state must not ignore; it is kept for tests and
// tools.
func (l *Log) Append(events []todo.Event) error {
	return l.Update(func([]todo.Event) ([]todo.Event, error) { return events, nil })
}

// readNew reads complete events past the last position. Callers hold the
// lock.
func (l *Log) readNew() ([]todo.Event, int, error) {
	files, err := logFiles(l.dir)
	if err != nil {
		return nil, 0, err
	}
	var (
		events  []todo.Event
		skipped int
	)
	for _, name := range files {
		seq := fileSeq(name)
		if seq < l.seq {
			continue
		}
		start := int64(0)
		if seq == l.seq {
			start = l.off
		}
		r, err := readFrom(filepath.Join(l.dir, name), start)
		if err != nil {
			return nil, 0, err
		}
		events = append(events, r.events...)
		skipped += r.skipped
		if start == 0 {
			l.ver = r.version
		}
		l.seq, l.off = seq, r.end
	}
	return events, skipped, nil
}

// append writes events to the newest file, starting a new file when the
// current one is full or was written by an older format. Callers hold the
// lock and have just called readNew.
func (l *Log) append(events []todo.Event) error {
	var buf bytes.Buffer
	for _, e := range events {
		b, err := json.Marshal(e)
		if err != nil {
			return err
		}
		buf.Write(b)
		buf.WriteByte('\n')
	}
	if l.seq == 0 || l.ver < FormatVersion || (l.off > 0 && l.off+int64(buf.Len()) > l.MaxSize) {
		if err := l.startFile(l.seq + 1); err != nil {
			return err
		}
	}
	f, err := os.OpenFile(l.path(l.seq), os.O_APPEND|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	// A crash mid-write can leave a partial last line, which readNew has
	// already skipped. Terminate it so this write starts on a fresh line.
	if l.off > 0 {
		last := make([]byte, 1)
		if _, err := f.ReadAt(last, l.off-1); err != nil {
			return err
		}
		if last[0] != '\n' {
			buf2 := append([]byte{'\n'}, buf.Bytes()...)
			buf.Reset()
			buf.Write(buf2)
		}
	}
	n, err := f.Write(buf.Bytes())
	l.off += int64(n)
	if err != nil {
		return err
	}
	return f.Sync()
}

func (l *Log) path(seq int) string {
	return filepath.Join(l.dir, fmt.Sprintf("events-%06d.jsonl", seq))
}

// startFile creates log file number seq with a header line.
func (l *Log) startFile(seq int) error {
	hdr, _ := json.Marshal(todo.Event{Time: time.Now(), Op: todo.OpMeta, Version: FormatVersion})
	f, err := os.OpenFile(l.path(seq), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	n, err := f.Write(append(hdr, '\n'))
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	l.seq, l.off, l.ver = seq, int64(n), FormatVersion
	return syncDir(l.dir)
}

func logFiles(dir string) ([]string, error) {
	matches, err := filepath.Glob(filepath.Join(dir, filePattern))
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(matches))
	for _, m := range matches {
		if fileSeq(filepath.Base(m)) > 0 {
			names = append(names, filepath.Base(m))
		}
	}
	slices.Sort(names)
	return names, nil
}

func fileSeq(name string) int {
	var seq int
	if _, err := fmt.Sscanf(name, "events-%06d.jsonl", &seq); err != nil {
		return 0
	}
	return seq
}

type readResult struct {
	events  []todo.Event
	skipped int
	version int
	end     int64
}

// readFrom parses lines from offset start to the end of the file. The lock
// is held, so a final line without a newline is crash debris: it is
// skipped and consumed.
func readFrom(path string, start int64) (readResult, error) {
	var res readResult
	f, err := os.Open(path)
	if err != nil {
		return res, err
	}
	defer f.Close()
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return res, err
	}
	res.end = start
	r := bufio.NewReader(f)
	for {
		line, err := r.ReadBytes('\n')
		res.end += int64(len(line))
		if len(bytes.TrimSpace(line)) > 0 {
			var e todo.Event
			if jerr := json.Unmarshal(line, &e); jerr != nil || e.Op == "" {
				res.skipped++
			} else if e.Op == todo.OpMeta {
				if e.Version > FormatVersion {
					return res, fmt.Errorf("%s was written by a newer todotil (format %d); please upgrade", filepath.Base(path), e.Version)
				}
				res.version = max(e.Version, 1)
			} else {
				res.events = append(res.events, e)
			}
		}
		if err == io.EOF {
			return res, nil
		}
		if err != nil {
			return res, err
		}
	}
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

// ErrLocked is returned when an older todotil, which held a lock for its
// whole session, is running.
var ErrLocked = errors.New("an older todotil is running; quit it before using this version")

// CheckLegacyLock fails if a pre-v2 binary holds the session lock at path.
// New binaries never hold it, so the check releases it straight away.
func CheckLegacyLock(path string) error {
	fl := flock.New(path)
	ok, err := fl.TryLock()
	if err != nil {
		return err
	}
	if !ok {
		return ErrLocked
	}
	return fl.Unlock()
}
