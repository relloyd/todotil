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

	h.keys("ctrl+x")
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
	h.keys("ctrl+x")
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

func TestRejectShortcutIgnoredInEditor(t *testing.T) {
	h := newHarness(t)
	h.add("next item", "ctrl+x", "alt+x")

	assert.Empty(t, h.viewTitles(tabNow))
	assert.Equal(t, []string{"next item"}, h.viewTitles(tabNext))
	assert.True(t, h.svc.Board().ViewRows(todo.Next)[0].Item.Open())
}

func TestRejectExplainsWhyItCannot(t *testing.T) {
	tests := []struct {
		name  string
		setup func(h *harness)
		want  string
	}{
		{"done", func(h *harness) { h.add("item"); h.keys("x", "4") }, "A completed item can't be rejected"},
		{"journal", func(h *harness) { h.add("item", "alt+j"); h.keys("4") }, "Journal items can't be rejected"},
		{"already rejected", func(h *harness) { h.add("item"); h.keys("ctrl+x", "4") }, "“item” is already rejected; x reopens it"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			tt.setup(h)
			require.Equal(t, "item", h.selectedTitle())
			assert.NotContains(t, h.screen(), "ctrl+x reject", "hint hidden for items that can't be rejected")

			h.keys("ctrl+x")
			assert.Contains(t, h.screen(), tt.want)

			// The detail view explains too.
			h.m.status = ""
			h.keys("enter", "ctrl+x")
			assert.Contains(t, h.screen(), tt.want)
		})
	}
}

func TestRejectHintShownForOpenItems(t *testing.T) {
	h := newHarness(t)
	h.add("item")
	assert.Contains(t, h.screen(), "ctrl+x reject")
	h.keys("enter")
	assert.Contains(t, h.screen(), "ctrl+x reject")
}

func TestJumpClearsHistoryFilterThatHidesTarget(t *testing.T) {
	h := newHarness(t)
	h.add("parent\n\n- [ ] kid\n- [ ] other")
	// Complete the parent's own row only, leaving kid open in Now.
	_, err := h.svc.Complete(h.svc.Board().ViewRows(todo.Now)[0].Item.ID, true)
	require.NoError(t, err)
	_, err = h.svc.Add("rejected", todo.Now, "", todo.Now)
	require.NoError(t, err)
	_, err = h.svc.Reject(h.svc.Board().ViewRows(todo.Now)[0].Item.ID, false)
	require.NoError(t, err)
	h.m.refresh()

	h.keys("4", "f", "f") // History filter: Rejected
	require.Equal(t, todo.HistoryRejected, h.m.historyFilter)
	require.NotContains(t, h.viewTitles(tabHistory), "parent")

	// Reopen kid so it sits in Now, then jump to its completed parent.
	for _, c := range h.svc.Board().All() {
		if c.Title == "kid" {
			_, err = h.svc.Reopen(c.ID)
			require.NoError(t, err)
		}
	}
	h.m.refresh()
	h.keys("1", "g")
	require.Equal(t, "kid", h.selectedTitle())
	h.keys("p")
	assert.Equal(t, tabHistory, h.m.tab)
	assert.Equal(t, "parent", h.selectedTitle())
	assert.Equal(t, todo.HistoryAll, h.m.historyFilter)
	assert.Contains(t, h.screen(), "History filter cleared to show “parent”")
}

func TestCopyMarksRejectedChildren(t *testing.T) {
	h := newHarness(t)
	h.add("note\n\n- [ ] boxed\n- [ ] kept")
	h.keys("j") // boxed
	require.Equal(t, "boxed", h.selectedTitle())
	h.keys("ctrl+x")
	// A child added directly, not from a checkbox line.
	h.keys("g", "A")
	h.typeText("extra")
	h.keys("enter")
	extra := h.m.selected()
	require.Equal(t, "extra", extra.Title)
	h.keys("ctrl+x")

	h.keys("g", "y")
	assert.Equal(t, "note\n\n- [-] boxed\n- [ ] kept\n\n- [-] extra", h.copied)
}
