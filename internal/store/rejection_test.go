package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/relloyd/todotil/internal/todo"
)

func TestRejectedOutcomeUsesCurrentFormatFile(t *testing.T) {
	dir := t.TempDir()
	old := []byte(`{"op":"meta","v":2}` + "\n")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "events-000001.jsonl"), old, 0o600))

	l, _, err := Open(dir)
	require.NoError(t, err)
	rejectedAt := time.Date(2026, 9, 29, 10, 30, 0, 0, time.UTC)
	it := &todo.Item{
		ID: "rejected", Title: "rejected task", State: todo.Now,
		RejectedAt: &rejectedAt, RejectedBy: "agent",
	}
	require.NoError(t, l.Append([]todo.Event{{
		Time: rejectedAt, Tx: 1, Op: todo.OpPut, Item: it,
	}}))
	assert.Equal(t, FormatVersion, l.ver)
	require.NoError(t, l.Close())

	files, err := logFiles(dir)
	require.NoError(t, err)
	assert.Equal(t, []string{"events-000001.jsonl", "events-000002.jsonl"}, files)

	l, res, err := Open(dir)
	require.NoError(t, err)
	defer l.Close()
	require.Len(t, res.Events, 1)
	assert.True(t, it.Equal(res.Events[0].Item))
}
