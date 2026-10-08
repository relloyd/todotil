package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/relloyd/todotil/internal/todo"
)

func TestListSearchNarrowsAsYouType(t *testing.T) {
	h := newHarness(t)
	h.add("buy milk")
	h.add("write report")
	h.add("book flights")

	h.keys("/")
	require.True(t, h.m.filters[tabNow].editing)
	h.typeText("b")
	assert.ElementsMatch(t, []string{"buy milk", "book flights"}, h.viewTitles(tabNow))
	s := h.screen()
	assert.Contains(t, s, "/ b")
	assert.Contains(t, s, "2 of 3")
	assert.NotContains(t, s, "write report")
	assert.Contains(t, s, "Now 3", "the tab count stays unfiltered")

	// Letters go to the prompt, not to list actions.
	h.typeText("ook")
	assert.Equal(t, []string{"book flights"}, h.viewTitles(tabNow))
	assert.Equal(t, "book flights", h.selectedTitle())

	// enter keeps the search and returns the keys to the list.
	h.keys("enter")
	assert.False(t, h.m.filters[tabNow].editing)
	h.m.status = "" // the "Added" notice hides the prompt line for a few seconds
	assert.Contains(t, h.screen(), "esc clear search")
	assert.Contains(t, h.screen(), "/ book")

	// esc clears it and keeps the cursor on the item.
	h.keys("esc")
	assert.False(t, h.m.filters[tabNow].active())
	assert.Len(t, h.viewTitles(tabNow), 3)
	assert.Equal(t, "book flights", h.selectedTitle())
}

func TestListSearchIsPerTab(t *testing.T) {
	h := newHarness(t)
	h.add("alpha now")
	h.keys("2")
	h.add("alpha next")
	h.add("beta next")

	h.keys("/")
	h.typeText("beta")
	h.keys("enter")
	assert.Equal(t, []string{"beta next"}, h.viewTitles(tabNext))

	h.keys("1")
	assert.Equal(t, []string{"alpha now"}, h.viewTitles(tabNow))
	assert.False(t, h.m.filters[tabNow].active())

	h.keys("2")
	h.m.status = ""
	assert.Equal(t, []string{"beta next"}, h.viewTitles(tabNext))
	assert.Contains(t, h.screen(), "/ beta")
}

func TestListSearchSwitchingTabsClosesPrompt(t *testing.T) {
	h := newHarness(t)
	h.add("alpha")
	h.keys("/")
	h.typeText("al")
	h.m.switchTab(tabNext)
	assert.False(t, h.m.filters[tabNow].editing)
	assert.Equal(t, "al", h.m.filters[tabNow].query())
}

func TestListSearchNoMatches(t *testing.T) {
	h := newHarness(t)
	h.add("alpha")
	h.keys("/")
	h.typeText("zzz")
	assert.Empty(t, h.viewTitles(tabNow))
	assert.Contains(t, h.screen(), "No matches")
	assert.Contains(t, h.screen(), "0 of 1")
	h.keys("esc")
	assert.Equal(t, []string{"alpha"}, h.viewTitles(tabNow))
}

func TestListSearchShowsMatchingChildFlatWithContext(t *testing.T) {
	h := newHarness(t)
	h.add("project\n\n- [ ] ship thing\n- [ ] other")
	h.keys("/")
	h.typeText("ship")
	require.Equal(t, []string{"ship thing"}, h.viewTitles(tabNow))
	r := h.m.rows[tabNow][0]
	assert.Equal(t, 0, r.Depth)
	assert.Equal(t, "project", r.Context)
	assert.Contains(t, h.screen(), "project › ship thing")
}

func TestListSearchActionsApplyToFilteredRows(t *testing.T) {
	h := newHarness(t)
	h.add("alpha")
	h.add("beta")
	h.keys("/")
	h.typeText("beta")
	h.keys("enter", "x")
	assert.Empty(t, h.viewTitles(tabNow), "beta is done and leaves the view")
	assert.Equal(t, 1, h.m.totals[tabNow])

	// An item added while searching but not matching is reported as hidden.
	h.add("gamma")
	assert.Contains(t, h.screen(), "hidden by the search")
	assert.Equal(t, 2, h.m.totals[tabNow])
}

func TestListSearchHistoryKeepsDayHeadersOfMatches(t *testing.T) {
	h := newHarness(t)
	h.add("apple")
	h.add("banana")
	for _, it := range h.svc.Board().All() {
		_, err := h.svc.Complete(it.ID, false)
		require.NoError(t, err)
	}
	h.m.refresh()
	h.keys("4")
	require.Len(t, h.viewTitles(tabHistory), 2)

	h.keys("/")
	h.typeText("ban")
	assert.Equal(t, []string{"banana"}, h.viewTitles(tabHistory))
	days := 0
	for _, r := range h.m.rows[tabHistory] {
		if r.Kind == todo.RowDay {
			days++
		}
	}
	assert.Equal(t, 1, days)
	h.keys("ctrl+u")
	h.typeText("nomatch")
	assert.Empty(t, h.m.rows[tabHistory], "a header with no items is dropped")
}

func TestJumpClearsSearchThatHidesTarget(t *testing.T) {
	h := newHarness(t)
	h.add("parent\n\n- [ ] kid")
	h.keys("/")
	h.typeText("kid")
	h.keys("enter")
	require.Equal(t, "kid", h.selectedTitle())
	h.keys("p")
	assert.Equal(t, "parent", h.selectedTitle())
	assert.False(t, h.m.filters[tabNow].active())
	assert.Contains(t, h.screen(), "Search cleared to show “parent”")
}

func TestSearchDoesNotHideConfirmOrMovePrompts(t *testing.T) {
	h := newHarness(t)
	h.add("alpha")
	h.keys("/")
	h.typeText("alp")
	h.keys("enter")
	h.m.status = ""

	h.keys("m")
	assert.Contains(t, h.screen(), "Move to:")
	h.keys("esc")
	h.m.status = ""

	h.keys("D")
	require.NotNil(t, h.m.confirm)
	assert.Contains(t, h.screen(), "Delete")
	h.keys("n")
	h.m.status = ""
	assert.Contains(t, h.screen(), "/ alp", "the applied search returns once the prompt is answered")
}

func TestSearchRefusesReorderAndIndent(t *testing.T) {
	h := newHarness(t)
	h.add("alpha")
	h.add("beta")
	h.keys("/")
	h.typeText("beta")
	h.keys("enter")
	before := h.svc.Board().ViewRows(todo.Now)
	for _, k := range []string{"alt+k", "alt+j", ">", "<"} {
		h.keys(k)
		assert.Contains(t, h.screen(), "Clear the search", k)
		h.m.status = ""
	}
	after := h.svc.Board().ViewRows(todo.Now)
	require.Len(t, after, len(before))
	for i := range before {
		assert.Same(t, before[i].Item, after[i].Item, "nothing changed")
	}

	// With the search cleared they work again.
	h.keys("esc", "alt+k")
	assert.NotContains(t, h.screen(), "Clear the search")
}

func TestSearchHiddenAddKeepsCursorAndEditNotes(t *testing.T) {
	h := newHarness(t)
	h.add("report one")
	h.add("report two")
	h.add("report three")
	h.keys("/")
	h.typeText("report")
	h.keys("enter", "down", "down")
	want := h.selectedTitle()

	h.add("buy milk")
	assert.Contains(t, h.screen(), "hidden by the search")
	assert.Equal(t, want, h.selectedTitle(), "the cursor stays on the same item")

	// Editing an item so it stops matching says so as well.
	h.keys("e")
	require.NotNil(t, h.m.editor)
	h.keys("ctrl+a", "ctrl+k")
	h.typeText("something else")
	h.keys("enter")
	assert.Contains(t, h.screen(), "Saved")
	assert.Contains(t, h.screen(), "hidden by the search")
}
