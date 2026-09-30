package ui

import (
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/relloyd/todotil/internal/fuzzy"
)

// filterState is a fuzzy filter over a list. The query is typed at a prompt
// that takes over the status line; enter keeps the filter and hands the keys
// back to the list.
type filterState struct {
	input   textinput.Model
	editing bool
}

// query is the text being filtered on.
func (f *filterState) query() string { return strings.TrimSpace(f.input.Value()) }

// active reports whether the list is filtered or the prompt is open.
func (f *filterState) active() bool { return f.editing || f.query() != "" }

// start opens the prompt, keeping any query so it can be refined.
func (m *Model) startFilter(f *filterState) tea.Cmd {
	if !f.active() {
		f.input = m.newFilterInput()
	}
	f.editing = true
	f.input.CursorEnd()
	return f.input.Focus()
}

func (m *Model) newFilterInput() textinput.Model {
	in := textinput.New()
	in.Prompt = "/ "
	in.Placeholder = "type to filter"
	s := textinput.DefaultStyles(true)
	for _, st := range []*textinput.StyleState{&s.Focused, &s.Blurred} {
		st.Prompt = m.st.key
		st.Text = m.st.text
		st.Placeholder = m.st.muted
	}
	s.Cursor.Color = lipgloss.Color(m.Palette.Accent)
	// A steady cursor needs no blink ticks.
	s.Cursor.Blink = false
	in.SetStyles(s)
	return in
}

// clear drops the query and closes the prompt.
func (f *filterState) clear() {
	f.input.Reset()
	f.input.Blur()
	f.editing = false
}

// key handles a key typed at the prompt and reports whether the query
// changed. Navigation keys are the caller's to handle first.
func (f *filterState) key(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	before := f.query()
	switch msg.String() {
	case "enter":
		f.editing = false
		f.input.Blur()
		return false, nil
	case "esc", "ctrl+c":
		f.clear()
		return before != "", nil
	}
	var cmd tea.Cmd
	f.input, cmd = f.input.Update(msg)
	return f.query() != before, cmd
}

// filterInput passes a message other than a key press (a paste) to the
// prompt and refilters.
func (m *Model) filterInput(f *filterState, msg tea.Msg) tea.Cmd {
	before := f.query()
	var cmd tea.Cmd
	f.input, cmd = f.input.Update(msg)
	if f.query() != before {
		m.refilter()
	}
	return cmd
}

// refilter applies a changed query to the open pane.
func (m *Model) refilter() {
	switch m.mode {
	case modeSettings:
		m.filterSettings()
	case modeHelp:
		m.help.offset = 0
	}
}

// currentFilter is the filter of the open settings or help pane, if any.
func (m *Model) currentFilter() *filterState {
	switch m.mode {
	case modeSettings:
		return &m.settings.filter
	case modeHelp:
		return &m.help.filter
	}
	return nil
}

// filterCounts returns how many rows match the current filter out of how
// many there are.
func (m *Model) filterCounts() (int, int) {
	switch m.mode {
	case modeSettings:
		return m.settings.matched, m.settings.total
	case modeHelp:
		_, matched, total := m.helpRows()
		return matched, total
	}
	return 0, 0
}

// filterView renders the prompt, or the applied query, with the match count
// on the right.
func (m *Model) filterView(f *filterState) string {
	matched, total := m.filterCounts()
	right := m.st.muted.Render(fmt.Sprintf("%d of %d ", matched, total))
	var left string
	if f.editing {
		in := f.input
		in.SetWidth(max(1, m.width-lipgloss.Width(right)-6))
		left = " " + in.View()
	} else {
		left = " " + m.st.key.Render("/ ") + m.st.text.Render(clean(f.query()))
	}
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		return ansi.Truncate(left, m.width, "…")
	}
	return left + strings.Repeat(" ", gap) + right
}

// matchSegs splits text into segments, styling the runes at the matched
// positions with hl. Positions are rune indexes into text.
func matchSegs(text string, pos []int, st, hl lipgloss.Style) []seg {
	if len(pos) == 0 {
		return []seg{{text: text, st: st}}
	}
	var out []seg
	var run []rune
	matched, p := false, 0
	flush := func() {
		if len(run) > 0 {
			s := st
			if matched {
				s = hl
			}
			out = append(out, seg{text: string(run), st: s})
			run = run[:0]
		}
	}
	for i, r := range []rune(text) {
		for p < len(pos) && pos[p] < i {
			p++
		}
		isMatch := p < len(pos) && pos[p] == i
		if isMatch != matched {
			flush()
			matched = isMatch
		}
		run = append(run, r)
	}
	flush()
	return out
}

// fitSegs fits text into a column of w: it truncates the plain text to
// leave at least one space, splits it into segments with the matched
// positions highlighted, and pads to exactly w.
func fitSegs(text string, pos []int, w int, st, hl lipgloss.Style) []seg {
	cut := ansi.Truncate(text, w-1, "…")
	if cut != text {
		// Don't highlight the ellipsis in place of a cut-off match.
		n := utf8.RuneCountInString(cut) - 1
		pos = slices.DeleteFunc(slices.Clone(pos), func(p int) bool { return p >= n })
	}
	out := matchSegs(cut, pos, st, hl)
	if pad := w - ansi.StringWidth(cut); pad > 0 {
		out = append(out, seg{text: strings.Repeat(" ", pad), st: st})
	}
	return out
}

// matchRow matches a query against a list row: its key bindings, then its
// text (fuzzily or by word), then its description (by word). A term finds a
// binding only as a whole key, ignoring case, so "ctrl+x" or "x" finds one
// but "ctrl" doesn't. It returns the matched rune positions in the keys as
// KeyMap.Help joins them, in the text and in the description.
func matchRow(query string, keys []string, text string, mode fuzzy.Mode, desc string) (keyPos, textPos, descPos []int, ok bool) {
	fields := make([]fuzzy.Field, 0, len(keys)+2)
	for _, k := range keys {
		fields = append(fields, fuzzy.Field{Text: k, Mode: fuzzy.Exact})
	}
	fields = append(fields, fuzzy.Field{Text: text, Mode: mode}, fuzzy.Field{Text: desc})
	pos, ok := fuzzy.MatchFields(query, fields...)
	if !ok {
		return nil, nil, nil, false
	}
	offset := 0
	for i, k := range keys {
		for _, p := range pos[i] {
			keyPos = append(keyPos, offset+p)
		}
		offset += utf8.RuneCountInString(k) + 1 // the "/" separator
	}
	return keyPos, pos[len(keys)], pos[len(keys)+1], true
}
