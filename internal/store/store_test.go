package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofrs/flock"

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

func TestLegacyLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "todotil.lock")
	require.NoError(t, CheckLegacyLock(path), "nobody holds it")
	old := flock.New(path)
	ok, err := old.TryLock()
	require.NoError(t, err)
	require.True(t, ok)
	assert.ErrorIs(t, CheckLegacyLock(path), ErrLocked)
	require.NoError(t, old.Unlock())
	assert.NoError(t, CheckLegacyLock(path))
}

func TestSharedWritersSeeEachOther(t *testing.T) {
	dir := t.TempDir()
	a, _, err := Open(dir)
	require.NoError(t, err)
	b, _, err := Open(dir)
	require.NoError(t, err)

	require.NoError(t, a.Append([]todo.Event{put("from-a")}))
	evs, err := b.Poll()
	require.NoError(t, err)
	require.Len(t, evs, 1)
	assert.Equal(t, "from-a", evs[0].Item.ID)

	evs, err = b.Poll()
	require.NoError(t, err)
	assert.Empty(t, evs, "nothing new")

	// b's update is handed a's newer write before it appends.
	require.NoError(t, a.Append([]todo.Event{put("a2")}))
	var seen []string
	require.NoError(t, b.Update(func(foreign []todo.Event) ([]todo.Event, error) {
		for _, e := range foreign {
			seen = append(seen, e.Item.ID)
		}
		return []todo.Event{put("from-b")}, nil
	}))
	assert.Equal(t, []string{"a2"}, seen)

	evs, err = a.Poll()
	require.NoError(t, err)
	require.Len(t, evs, 1)
	assert.Equal(t, "from-b", evs[0].Item.ID)

	// A failing update writes nothing.
	require.Error(t, a.Update(func([]todo.Event) ([]todo.Event, error) {
		return []todo.Event{put("never")}, errors.New("nope")
	}))
	_, res, err := Open(dir)
	require.NoError(t, err)
	assert.Len(t, res.Events, 3)
}

func TestConcurrentWritersInterleaveWholeBatches(t *testing.T) {
	dir := t.TempDir()
	const writers, batches = 4, 25
	var wg sync.WaitGroup
	for w := range writers {
		l, _, err := Open(dir)
		require.NoError(t, err)
		l.MaxSize = 2000 // force rotations under contention
		wg.Go(func() {
			for i := range batches {
				id := fmt.Sprintf("w%d-%d", w, i)
				assert.NoError(t, l.Append([]todo.Event{put(id + "a"), put(id + "b")}))
			}
		})
	}
	wg.Wait()
	_, res, err := Open(dir)
	require.NoError(t, err)
	assert.Zero(t, res.Skipped)
	require.Len(t, res.Events, writers*batches*2)
	for i := 0; i < len(res.Events); i += 2 {
		a, b := res.Events[i].Item.ID, res.Events[i+1].Item.ID
		assert.Equal(t, a[:len(a)-1], b[:len(b)-1], "batches are never split")
	}
}

func TestUpgradeStartsNewFile(t *testing.T) {
	dir := t.TempDir()
	v1 := `{"op":"meta","v":1}` + "\n" + `{"op":"put","item":{"id":"old","title":"old","state":"now"}}` + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "events-000001.jsonl"), []byte(v1), 0o600))
	l, res, err := Open(dir)
	require.NoError(t, err)
	assert.Len(t, res.Events, 1)
	require.NoError(t, l.Append([]todo.Event{put("new")}))
	files, err := logFiles(dir)
	require.NoError(t, err)
	assert.Equal(t, []string{"events-000001.jsonl", "events-000002.jsonl"}, files)
	b, err := os.ReadFile(filepath.Join(dir, files[1]))
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(string(b), `{"t":`))
	assert.Contains(t, string(b), `"v":3`)
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
