package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/relloyd/todotil/internal/todo"
)

func put(title string) todo.Event {
	return todo.Event{Time: time.Now(), Tx: 1, Op: todo.OpPut, Item: &todo.Item{ID: title, Title: title, State: todo.Now}}
}

func TestLogRoundTrip(t *testing.T) {
	dir := t.TempDir()
	l, res, err := Open(dir)
	require.NoError(t, err)
	assert.Empty(t, res.Events)
	require.NoError(t, l.Append([]todo.Event{put("a"), put("b")}))
	require.NoError(t, l.Append([]todo.Event{{Op: todo.OpDel, ID: "a"}}))
	require.NoError(t, l.Close())

	l, res, err = Open(dir)
	require.NoError(t, err)
	defer l.Close()
	require.Len(t, res.Events, 3)
	assert.Equal(t, "b", res.Events[1].Item.Title)
	assert.Equal(t, todo.OpDel, res.Events[2].Op)
	assert.Zero(t, res.Skipped)
}

func TestLogSplitsFiles(t *testing.T) {
	dir := t.TempDir()
	l, _, err := Open(dir)
	require.NoError(t, err)
	l.MaxSize = 400
	for range 10 {
		require.NoError(t, l.Append([]todo.Event{put(strings.Repeat("x", 50))}))
	}
	require.NoError(t, l.Close())

	files, err := logFiles(dir)
	require.NoError(t, err)
	assert.Greater(t, len(files), 2)
	assert.Equal(t, "events-000001.jsonl", files[0])
	for _, f := range files {
		st, err := os.Stat(filepath.Join(dir, f))
		require.NoError(t, err)
		assert.LessOrEqual(t, st.Size(), int64(400)+200, f)
	}

	l, res, err := Open(dir)
	require.NoError(t, err)
	defer l.Close()
	assert.Len(t, res.Events, 10)
	assert.Equal(t, len(files), l.seq)
}

func TestLogTolerateTornWrite(t *testing.T) {
	dir := t.TempDir()
	l, _, err := Open(dir)
	require.NoError(t, err)
	require.NoError(t, l.Append([]todo.Event{put("a")}))
	require.NoError(t, l.Close())

	path := filepath.Join(dir, "events-000001.jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	require.NoError(t, err)
	_, err = f.WriteString(`{"op":"put","item":{"id":"tor`)
	require.NoError(t, err)
	require.NoError(t, f.Close())

	l, res, err := Open(dir)
	require.NoError(t, err)
	assert.Equal(t, 1, res.Skipped)
	require.NoError(t, l.Append([]todo.Event{put("b")}))
	require.NoError(t, l.Close())

	_, res, err = Open(dir)
	require.NoError(t, err)
	assert.Equal(t, 1, res.Skipped)
	require.Len(t, res.Events, 2)
	assert.Equal(t, "b", res.Events[1].Item.ID)
}

func TestLogRejectsNewerFormat(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "events-000001.jsonl"), []byte(`{"op":"meta","v":99}`+"\n"), 0o600))
	_, _, err := Open(dir)
	assert.ErrorContains(t, err, "newer")
}

func TestLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "todotil.lock")
	l1, err := AcquireLock(path)
	require.NoError(t, err)
	_, err = AcquireLock(path)
	assert.ErrorIs(t, err, ErrLocked)
	require.NoError(t, l1.Release())
	l2, err := AcquireLock(path)
	require.NoError(t, err)
	require.NoError(t, l2.Release())
}

func TestBackupAndPrune(t *testing.T) {
	home := t.TempDir()
	data, bdir := filepath.Join(home, "data"), filepath.Join(home, "backups")
	l, _, err := Open(data)
	require.NoError(t, err)
	defer l.Close()
	require.NoError(t, l.Append([]todo.Event{put("a")}))

	now := time.Date(2026, 9, 29, 11, 0, 0, 0, time.Local)
	need, err := NeedsBackup(bdir, now, 24*time.Hour)
	require.NoError(t, err)
	assert.True(t, need)

	for d := 12; d >= 0; d-- {
		_, err := l.Backup(bdir, now.AddDate(0, 0, -d))
		require.NoError(t, err)
	}
	// Same timestamp twice gets a distinct directory.
	p, err := l.Backup(bdir, now)
	require.NoError(t, err)
	assert.True(t, strings.HasSuffix(p, "-1"))
	b, err := os.ReadFile(filepath.Join(p, "events-000001.jsonl"))
	require.NoError(t, err)
	assert.Contains(t, string(b), `"title":"a"`)

	need, err = NeedsBackup(bdir, now.Add(time.Hour), 24*time.Hour)
	require.NoError(t, err)
	assert.False(t, need)

	require.NoError(t, os.WriteFile(filepath.Join(bdir, "notes.txt"), nil, 0o600))
	removed, err := Prune(bdir, now, 10)
	require.NoError(t, err)
	assert.Len(t, removed, 2)
	bs, err := backups(bdir)
	require.NoError(t, err)
	assert.Len(t, bs, 12)
	assert.Equal(t, now.AddDate(0, 0, -10), bs[0].time)
	assert.FileExists(t, filepath.Join(bdir, "notes.txt"))
	assert.DirExists(t, data)
}

func TestNextBackupTime(t *testing.T) {
	day := func(d, h, m int) time.Time { return time.Date(2026, 9, d, h, m, 0, 0, time.Local) }
	tests := []struct {
		now, want time.Time
	}{
		{day(29, 9, 30), day(29, 11, 0)},
		{day(29, 11, 0), day(30, 11, 0)},
		{day(29, 23, 59), day(30, 11, 0)},
		{day(30, 10, 59), day(30, 11, 0)},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, NextBackupTime(tt.now, 11), tt.now.String())
	}
}
