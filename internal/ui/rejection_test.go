package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/relloyd/todotil/internal/todo"
)

func TestRejectShortcutConfirmsCascadeAndReopens(t *testing.T) {
	h := newHarness(t)
	h.add("parent\n\n- [ ] child")

	h.keys("alt+x")
	require.NotNil(t, h.m.confirm)
	assert.Contains(t, h.screen(), "Reject “parent” and its 1 open child?")

	h.keys("y")
	assert.Empty(t, h.viewTitles(tabNow))
	assert.Equal(t, []string{"parent", "child"}, h.viewTitles(tabHistory))

	h.keys("4", "g")
	require.Equal(t, "parent", h.selectedTitle())
	assert.Contains(t, h.screen(), "× parent")
	h.keys("x")
	assert.Equal(t, []string{"parent"}, h.viewTitles(tabNow))
	assert.True(t, h.svc.Board().Children(h.svc.Board().ViewRows(todo.Now)[0].Item.ID)[0].Rejected())
}

func TestHistoryFilterCyclesWithoutChangingHistorySort(t *testing.T) {
	h := newHarness(t)
	h.add("completed")
	h.keys("x")
	h.add("rejected")
	h.keys("alt+x")
	h.add("open")

	_, err := h.svc.Add("journal", todo.Journal, "", todo.Now)
	require.NoError(t, err)
	h.m.refresh()

	h.keys("4")
	assert.ElementsMatch(t, []string{"completed", "rejected", "open", "journal"}, h.viewTitles(tabHistory))
	assert.Contains(t, h.screen(), "filter: All")

	h.keys("f")
	assert.Equal(t, []string{"completed"}, h.viewTitles(tabHistory))
	assert.Contains(t, h.screen(), "filter: Completed")
	h.keys("f")
	assert.Equal(t, []string{"rejected"}, h.viewTitles(tabHistory))
	h.keys("f")
	assert.Equal(t, []string{"journal"}, h.viewTitles(tabHistory))
	h.keys("f")
	assert.ElementsMatch(t, []string{"completed", "rejected", "open", "journal"}, h.viewTitles(tabHistory))
	assert.Equal(t, string(todo.SortCreated), h.m.Settings.History.Sort)
}

func TestRejectShortcutStillClassifiesEditorEntries(t *testing.T) {
	h := newHarness(t)
	h.add("next item", "alt+x")

	assert.Empty(t, h.viewTitles(tabNow))
	assert.Equal(t, []string{"next item"}, h.viewTitles(tabNext))
}
