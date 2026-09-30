package store

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"time"
)

const backupLayout = "2006-01-02T150405"

// Backup copies the log files into a new timestamped directory under
// backupDir. The data lock is held while copying so the copy is consistent.
func (l *Log) Backup(backupDir string, now time.Time) (string, error) {
	var path string
	err := l.locked(func() error {
		var err error
		path, err = l.backup(backupDir, now)
		return err
	})
	return path, err
}

func (l *Log) backup(backupDir string, now time.Time) (string, error) {
	if err := os.MkdirAll(backupDir, 0o700); err != nil {
		return "", err
	}
	name := now.Format(backupLayout)
	final := filepath.Join(backupDir, name)
	for i := 1; ; i++ {
		if _, err := os.Stat(final); os.IsNotExist(err) {
			break
		}
		final = filepath.Join(backupDir, fmt.Sprintf("%s-%d", name, i))
	}
	tmp, err := os.MkdirTemp(backupDir, ".tmp-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	files, err := logFiles(l.dir)
	if err != nil {
		return "", err
	}
	for _, f := range files {
		if err := copyFile(filepath.Join(l.dir, f), filepath.Join(tmp, f)); err != nil {
			return "", err
		}
	}
	if err := os.Rename(tmp, final); err != nil {
		return "", err
	}
	return final, syncDir(backupDir)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

type backupEntry struct {
	name string
	time time.Time
}

// backups lists backup directories, oldest first.
func backups(backupDir string) ([]backupEntry, error) {
	entries, err := os.ReadDir(backupDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []backupEntry
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || len(name) < len(backupLayout) {
			continue
		}
		t, err := time.ParseInLocation(backupLayout, name[:len(backupLayout)], time.Local)
		if err != nil {
			continue
		}
		out = append(out, backupEntry{name, t})
	}
	slices.SortFunc(out, func(a, b backupEntry) int { return a.time.Compare(b.time) })
	return out, nil
}

// LatestBackup returns the time of the newest backup.
func LatestBackup(backupDir string) (time.Time, bool, error) {
	bs, err := backups(backupDir)
	if err != nil || len(bs) == 0 {
		return time.Time{}, false, err
	}
	return bs[len(bs)-1].time, true, nil
}

// NeedsBackup reports whether no backup has been made within maxAge.
func NeedsBackup(backupDir string, now time.Time, maxAge time.Duration) (bool, error) {
	last, ok, err := LatestBackup(backupDir)
	if err != nil {
		return false, err
	}
	return !ok || now.Sub(last) >= maxAge, nil
}

// Prune removes backups older than keepDays days. Only backup directories
// are ever removed.
func Prune(backupDir string, now time.Time, keepDays int) ([]string, error) {
	bs, err := backups(backupDir)
	if err != nil {
		return nil, err
	}
	cutoff := now.AddDate(0, 0, -keepDays)
	var removed []string
	for _, b := range bs {
		if !b.time.Before(cutoff) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(backupDir, b.name)); err != nil {
			return removed, err
		}
		removed = append(removed, b.name)
	}
	return removed, nil
}

// NextBackupTime returns the next occurrence of hour:00 local time after now.
func NextBackupTime(now time.Time, hour int) time.Time {
	next := time.Date(now.Year(), now.Month(), now.Day(), hour, 0, 0, 0, now.Location())
	if !next.After(now) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}
