package cli

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/relloyd/todotil/internal/todo"
)

func TestRejectedItemIsVisibleInAgentJSON(t *testing.T) {
	rejectedAt := time.Date(2026, 9, 29, 10, 30, 0, 0, time.Local)
	it := &todo.Item{
		ID:         "rejected-id",
		Title:      "rejected task",
		State:      todo.Now,
		RejectedAt: &rejectedAt,
		RejectedBy: "agent",
	}

	got := item(todo.NewBoard(), it, false)
	assert.Equal(t, false, got.Done)
	assert.Equal(t, true, got.Rejected)
	assert.Equal(t, &rejectedAt, got.RejectedAt)
	assert.Equal(t, "agent", got.RejectedBy)

	ref := ref(it)
	assert.Equal(t, false, ref.Done)
	assert.Equal(t, true, ref.Rejected)
}
