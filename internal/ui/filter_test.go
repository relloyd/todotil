package ui

import (
	"slices"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/relloyd/todotil/internal/config"
)

// shownLabels lists the settings rows the filter leaves, headers included.
func (h *harness) shownLabels() []string {
	var out []string
	for _, r := range h.m.settings.shown {
		it := h.m.settings.items[r.item]
		out = append(out, it.header+it.label)
	}
	return out
}

func TestSettingsFilter(t *testing.T) {
	h := newHarness(t)
	h.keys(",", "/")
	require.True(t, h.m.settings.filter.editing)
	h.typeText("undo dep")
	assert.Equal(t, []string{"General", "Undo depth"}, h.shownLabels())
	s := h.screen()
	assert.Contains(t, s, "/ undo dep")
	assert.Regexp(t, `1 of \d+`, s)
	assert.NotContains(t, s, "Link shortening")
	assert.Contains(t, s, "enter keep filter")

	// Letters go to the prompt, not the pane's keys.
	h.keys("down", "q")
	assert.Equal(t, modeSettings, h.m.mode)
	assert.Equal(t, "undo depq", h.m.settings.filter.query())
	h.keys("backspace")

	// enter keeps the filter and hands keys back to the pane.
	h.keys("enter")
	assert.False(t, h.m.settings.filter.editing)
	assert.Equal(t, "undo dep", h.m.settings.filter.query())
	assert.Contains(t, h.screen(), "/ undo dep", "the applied filter stays on the status line")
	assert.Contains(t, h.screen(), "esc clear filter")
	h.keys("right")
	assert.Equal(t, 11, h.svc.UndoDepth)

	// The first esc clears the filter and keeps the cursor on the setting.
	h.keys("esc")
	assert.Equal(t, modeSettings, h.m.mode)
	assert.Empty(t, h.m.settings.filter.query())
	assert.Equal(t, "Undo depth", h.m.settings.current().label)
	assert.Contains(t, h.screen(), "Link shortening")
	assert.Contains(t, h.screen(), "esc close")

	// The second closes, and reopening starts unfiltered.
	h.keys("esc")
	assert.Equal(t, modeList, h.m.mode)
	h.keys(",")
	assert.False(t, h.m.settings.filter.active())
	assert.Equal(t, "Link shortening", h.m.settings.current().label)
}

func TestSettingsFilterKeepsHeadersOfMatchingSections(t *testing.T) {
	h := newHarness(t)
	h.keys(",", "/")
	h.typeText("classify")
	assert.Equal(t, []string{"Keys · Add/edit dialog", "Classify as Now", "Classify as Next",
		"Classify as Later", "Classify as Journal"}, h.shownLabels())

	// Descriptions (action names) match by word, labels fuzzily.
	h.keys("ctrl+u")
	h.typeText("entry_jour")
	assert.Equal(t, []string{"Keys · Add/edit dialog", "Classify as Journal"}, h.shownLabels())
	h.keys("ctrl+u")
	h.typeText("mvup")
	assert.Equal(t, []string{"Keys · Lists", "Move item up"}, h.shownLabels())
}

func TestSettingsFilterRebindWhileFiltered(t *testing.T) {
	h := newHarness(t)
	h.keys(",", "/")
	h.typeText("quit")
	h.keys("enter")
	require.Equal(t, config.Quit, h.m.settings.current().action)
	h.keys("enter", "Q")
	assert.Equal(t, []string{"Q"}, h.m.Keys[config.Quit])
	assert.Equal(t, config.Quit, h.m.settings.current().action, "the row stays selected")
	h.keys("backspace")
	assert.Equal(t, []string{"q", "ctrl+c"}, h.m.Keys[config.Quit])
}

func TestSettingsFilterNoMatches(t *testing.T) {
	h := newHarness(t)
	h.keys(",", "/")
	h.typeText("zzzz")
	assert.Empty(t, h.m.settings.shown)
	assert.Contains(t, h.screen(), "No matching settings")
	assert.Regexp(t, `0 of \d+`, h.screen())
	h.keys("down", "enter")
	for _, k := range []string{"j", "k", "g", "G", "enter", "right", "left", "backspace"} {
		h.keys(k)
		_ = h.screen()
	}
	assert.Nil(t, h.m.settings.current())
	h.keys("/", "backspace", "backspace", "backspace", "backspace")
	assert.NotEmpty(t, h.m.settings.shown, "refining the query brings rows back")
	assert.NotNil(t, h.m.settings.current())
}

func TestSettingsFilterMouse(t *testing.T) {
	h := newHarness(t)
	h.keys(",", "/")
	h.typeText("move to")
	h.keys("enter")
	i := slices.IndexFunc(h.m.settings.shown, func(r shownSetting) bool {
		return h.m.settings.items[r.item].label == "Move to Later"
	})
	require.Positive(t, i)
	h.send(tea.MouseClickMsg{X: 10, Y: contentTop + i - h.m.settings.offset, Button: tea.MouseLeft})
	assert.Equal(t, "Move to Later", h.m.settings.current().label)
}

func TestFilterTextInput(t *testing.T) {
	h := newHarness(t)
	h.keys(",", "/")
	h.typeText("a/b")
	assert.Equal(t, "a/b", h.m.settings.filter.query(), "the filter key is text at the prompt")
	h.keys("esc")
	assert.False(t, h.m.settings.filter.active(), "esc at the prompt clears")
	assert.Equal(t, modeSettings, h.m.mode)

	h.keys("/")
	h.send(tea.PasteMsg{Content: "backup"})
	assert.Equal(t, []string{"General", "Daily backup hour", "Backups kept"}, h.shownLabels())

	// The filter key can be rebound.
	h.keys("esc", "esc")
	h.m.Keys[config.Filter] = []string{"ctrl+f"}
	h.keys(",", "/")
	assert.False(t, h.m.settings.filter.editing)
	h.keys("ctrl+f")
	assert.True(t, h.m.settings.filter.editing)
}

func TestHelpFilter(t *testing.T) {
	h := newHarness(t)
	h.keys("?", "/")
	h.typeText("mvup")
	s := h.screen()
	assert.Contains(t, s, "Move item up")
	assert.Contains(t, s, "Lists")
	assert.NotContains(t, s, "Cursor down")
	assert.NotContains(t, s, "Global")
	assert.Regexp(t, `1 of \d+`, s)

	h.keys("enter", "j", "k")
	assert.Equal(t, modeHelp, h.m.mode)
	assert.Contains(t, h.screen(), "/ mvup")

	// Mouse and Files lines match by word.
	h.keys("/", "ctrl+u")
	h.typeText("wheel")
	s = h.screen()
	assert.Contains(t, s, "Mouse")
	assert.Contains(t, s, "wheel scrolls")
	assert.NotContains(t, s, "Move item up")

	h.keys("ctrl+u")
	h.typeText("zzzz")
	assert.Contains(t, h.screen(), "No matching key bindings")

	h.keys("enter", "esc")
	assert.Equal(t, modeHelp, h.m.mode)
	assert.Contains(t, h.screen(), "Cursor down")
	h.keys("esc")
	assert.Equal(t, modeList, h.m.mode)
}

func TestSettingsFilterMatchesWholeKeys(t *testing.T) {
	h := newHarness(t)
	h.keys(",", "/")
	h.typeText("ctrl+x")
	assert.Equal(t, []string{"Keys · Lists", "Reject item and open children"}, h.shownLabels())
	assert.Equal(t, []int{0, 1, 2, 3, 4, 5}, h.m.settings.shown[1].value, "the key is highlighted")

	h.keys("ctrl+u")
	h.typeText("ctrl")
	assert.Empty(t, h.m.settings.shown, "only whole keys match")

	// Keys ignore case: g finds both g and G.
	h.keys("ctrl+u")
	h.typeText("g")
	labels := h.shownLabels()
	assert.Contains(t, labels, "Cursor to top")
	assert.Contains(t, labels, "Cursor to bottom")

	// The filter key itself can be searched for. (A label with a "/" in it,
	// like "created/completed", matches too.)
	h.keys("ctrl+u")
	h.typeText("/")
	require.Equal(t, "Search the current list, or filter settings and help", h.m.settings.current().label)
	assert.Equal(t, []int{0}, h.m.settings.shown[h.m.settings.cursor].value)

	// A key in a later position is highlighted where it is shown: "q/ctrl+c".
	h.keys("ctrl+u")
	h.typeText("ctrl+c")
	assert.Equal(t, []string{"Keys · Global", "Quit"}, h.shownLabels())
	assert.Equal(t, []int{2, 3, 4, 5, 6, 7}, h.m.settings.shown[1].value)
}

func TestSettingsFilterRebindKeepsRowShown(t *testing.T) {
	h := newHarness(t)
	h.keys(",", "/")
	h.typeText("ctrl+x")
	h.keys("enter", "enter", "R")
	assert.Equal(t, []string{"R"}, h.m.Keys[config.Reject])
	assert.Equal(t, "Reject item and open children", h.m.settings.current().label,
		"the row no longer matches but stays under the cursor")
	assert.Empty(t, h.m.settings.shown[1].value, "the stale highlight is dropped")

	h.keys("backspace")
	assert.Equal(t, []int{0, 1, 2, 3, 4, 5}, h.m.settings.shown[1].value, "reset brings the match back")
}

func TestHelpFilterMatchesKeys(t *testing.T) {
	h := newHarness(t)
	h.keys("?", "/")
	h.typeText("ctrl+y")
	s := h.screen()
	assert.Contains(t, s, "Copy the selected item's ID")
	assert.Regexp(t, `1 of \d+`, s)
}
