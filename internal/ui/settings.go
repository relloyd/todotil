package ui

import (
	"fmt"
	"image/color"
	"os"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/relloyd/todotil/internal/config"
	"github.com/relloyd/todotil/internal/fuzzy"
)

// setting is one row of the settings pane. Rows with a header only are
// section titles.
type setting struct {
	header string
	path   string // file shown next to a header
	label  string
	desc   string
	value  func(m *Model) string
	change func(m *Model, delta int) tea.Cmd
	action config.Action // key binding rows
}

type settingsState struct {
	items []setting
	// shown are the rows the filter leaves, headers included; cursor and
	// offset index into it.
	shown     []shownSetting
	cursor    int
	offset    int
	capturing bool
	filter    filterState
	// matched and total count setting rows (not headers) for the prompt.
	matched, total int
}

// shownSetting is a visible settings row and the filter's matched rune
// positions in its value (key bindings), label and description.
type shownSetting struct {
	item               int
	value, label, desc []int
}

// current returns the selected setting, or nil when nothing is selectable
// (the filter matched nothing).
func (s *settingsState) current() *setting {
	if s.cursor < 0 || s.cursor >= len(s.shown) {
		return nil
	}
	it := &s.items[s.shown[s.cursor].item]
	if it.header != "" {
		return nil
	}
	return it
}

func (s *settingsState) isHeader(i int) bool { return s.items[s.shown[i].item].header != "" }

func boolText(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

func cycle(opts []string, cur string, delta int) string {
	i := slices.Index(opts, cur)
	if i < 0 {
		return opts[0]
	}
	return opts[(i+delta%len(opts)+len(opts))%len(opts)]
}

func (m *Model) settingsItems() []setting {
	save := func(m *Model) tea.Cmd { return m.saveSettings() }
	items := []setting{
		{header: "General", path: tildify(m.Paths.Settings())},
		{
			label: "Link shortening", desc: "Show pasted URLs as page titles, fetched in the background.",
			value: func(m *Model) string { return boolText(m.Settings.ShortenLinks) },
			change: func(m *Model, _ int) tea.Cmd {
				m.Settings.ShortenLinks = !m.Settings.ShortenLinks
				return save(m)
			},
		},
		{
			label: "Undo depth", desc: "How many recent changes can be undone.",
			value: func(m *Model) string { return fmt.Sprint(m.Settings.UndoDepth) },
			change: func(m *Model, d int) tea.Cmd {
				m.Settings.UndoDepth = min(1000, max(1, m.Settings.UndoDepth+d))
				m.Service.UndoDepth = m.Settings.UndoDepth
				return save(m)
			},
		},
		{
			label: "Mouse", desc: "Capture the mouse for clicks and scrolling. Off leaves text selection to the terminal.",
			value: func(m *Model) string { return boolText(m.Settings.Mouse) },
			change: func(m *Model, _ int) tea.Cmd {
				m.Settings.Mouse = !m.Settings.Mouse
				return save(m)
			},
		},
		{
			label: "Theme", desc: "Colour theme. Override colours in " + tildify(m.Paths.Theme()) + ".",
			value: func(m *Model) string { return m.Settings.Theme },
			change: func(m *Model, d int) tea.Cmd {
				m.Settings.Theme = cycle(config.ThemeNames(), m.Settings.Theme, d)
				pal, err := config.LoadTheme(m.Paths, m.Settings.Theme)
				m.Palette = pal
				m.st = newStyles(pal)
				if cmd := save(m); cmd != nil || err == nil {
					return cmd
				}
				return m.fail(err)
			},
		},
		{
			label: "Journal checklists go to", desc: "Where checkbox todos in a journal note land when added from History.",
			value: func(m *Model) string { return m.Settings.JournalChildState },
			change: func(m *Model, d int) tea.Cmd {
				m.Settings.JournalChildState = cycle([]string{"now", "next", "later"}, m.Settings.JournalChildState, d)
				return save(m)
			},
		},
		{
			label: "History sort", desc: "Group and order History by created or completed/rejected date.",
			value:  func(m *Model) string { return m.Settings.History.Sort },
			change: func(m *Model, _ int) tea.Cmd { return m.toggleSortKey() },
		},
		{
			label: "History order", desc: "Show History newest or oldest first.",
			value: func(m *Model) string {
				if m.Settings.History.Descending {
					return "newest first"
				}
				return "oldest first"
			},
			change: func(m *Model, _ int) tea.Cmd { return m.toggleSortDir() },
		},
		{
			label: "Daily backup hour", desc: "Local hour for the daily backup while todotil is running.",
			value: func(m *Model) string { return fmt.Sprintf("%02d:00", m.Settings.BackupHour) },
			change: func(m *Model, d int) tea.Cmd {
				m.Settings.BackupHour = (m.Settings.BackupHour + d + 24) % 24
				m.nextBackup = m.nextBackupAfter(m.Now())
				return save(m)
			},
		},
		{
			label: "Backups kept", desc: "Days of backups to keep in " + tildify(m.Paths.Backups()) + ".",
			value: func(m *Model) string { return fmt.Sprintf("%d days", m.Settings.BackupKeepDays) },
			change: func(m *Model, d int) tea.Cmd {
				m.Settings.BackupKeepDays = min(365, max(1, m.Settings.BackupKeepDays+d))
				return save(m)
			},
		},
	}
	group := ""
	for _, a := range config.Actions {
		if a.Group != group {
			group = a.Group
			items = append(items, setting{header: "Keys · " + group, path: tildify(m.Paths.Keys())})
		}
		items = append(items, setting{
			label:  a.Desc,
			desc:   string(a.Action),
			action: a.Action,
			value:  func(m *Model) string { return m.Keys.Help(a.Action) },
		})
	}
	return items
}

func (m *Model) openSettings() {
	if m.mode != modeSettings && m.mode != modeHelp {
		m.backMode = m.mode
	}
	m.mode = modeSettings
	m.settings = settingsState{items: m.settingsItems()}
	m.filterSettings()
}

// matchSetting matches the query against setting i: its key bindings, its
// label (fuzzily) and its description (by word).
func (m *Model) matchSetting(q string, i int) (shownSetting, bool) {
	it := m.settings.items[i]
	var keys []string
	if it.action != "" {
		keys = m.Keys[it.action]
	}
	value, label, desc, ok := matchRow(q, keys, it.label, fuzzy.Fuzzy, it.desc)
	return shownSetting{item: i, value: value, label: label, desc: desc}, ok
}

// rematchSetting refreshes the highlights of the selected setting after its
// keys change. The row stays shown even if it no longer matches: dropping
// it would move the cursor to another setting under the user's hands.
func (m *Model) rematchSetting() {
	s := &m.settings
	if s.current() == nil {
		return
	}
	r := &s.shown[s.cursor]
	*r, _ = m.matchSetting(s.filter.query(), r.item)
}

// filterSettings recomputes the shown rows for the query, keeping the
// cursor on the same setting where it is still shown. A section's header is
// shown only while one of its settings is.
func (m *Model) filterSettings() {
	s := &m.settings
	prev := -1
	if s.current() != nil {
		prev = s.shown[s.cursor].item
	}
	q := s.filter.query()
	s.shown, s.matched, s.total = s.shown[:0], 0, 0
	header := -1
	for i, it := range s.items {
		if it.header != "" {
			header = i
			continue
		}
		s.total++
		row, ok := m.matchSetting(q, i)
		if !ok {
			continue
		}
		if header >= 0 {
			s.shown = append(s.shown, shownSetting{item: header})
			header = -1
		}
		s.shown = append(s.shown, row)
		s.matched++
	}
	s.cursor = slices.IndexFunc(s.shown, func(r shownSetting) bool { return r.item == prev })
	if s.cursor < 0 {
		s.cursor, s.offset = 0, 0
		m.settingsMove(1)
		return
	}
	m.settingsMove(0)
}

func (m *Model) closeOverlay() {
	m.mode = m.backMode
	if m.mode == modeDetail {
		m.buildDetail()
	}
}

func (m *Model) settingsMove(n int) {
	s := &m.settings
	c := s.cursor
	step := 1
	if n < 0 {
		step, n = -1, -n
	}
	for ; n > 0; n-- {
		next := c + step
		for next >= 0 && next < len(s.shown) && s.isHeader(next) {
			next += step
		}
		if next < 0 || next >= len(s.shown) {
			break
		}
		c = next
	}
	s.cursor = c
	h := m.contentHeight()
	s.offset = min(s.offset, max(0, len(s.shown)-h))
	if s.cursor-1 < s.offset {
		s.offset = max(0, s.cursor-1)
	}
	if s.cursor >= s.offset+h {
		s.offset = s.cursor - h + 1
	}
}

func (m *Model) settingsKey(msg tea.KeyPressMsg) tea.Cmd {
	s := &m.settings
	h := m.contentHeight()
	if s.filter.editing {
		switch msg.String() {
		case "down", "ctrl+n", "tab":
			m.settingsMove(1)
		case "up", "ctrl+p", "shift+tab":
			m.settingsMove(-1)
		case "pgdown":
			m.settingsMove(h / 2)
		case "pgup":
			m.settingsMove(-h / 2)
		default:
			changed, cmd := s.filter.key(msg)
			if changed {
				m.refilter()
			}
			return cmd
		}
		return nil
	}
	item := s.current()
	if s.capturing && item != nil {
		s.capturing = false
		if msg.String() == "esc" {
			return m.info("Rebinding cancelled")
		}
		k := msg.String()
		m.Keys[item.action] = []string{k}
		m.rematchSetting()
		if cmd := m.saveKeys(); cmd != nil {
			return cmd
		}
		return m.info(fmt.Sprintf("%s is now %s", item.label, k))
	}
	if m.keyMatches(config.Filter, msg) {
		return m.startFilter(&s.filter)
	}
	switch msg.String() {
	case "esc":
		if s.filter.active() {
			s.filter.clear()
			m.filterSettings()
			return nil
		}
		m.closeOverlay()
	case "q", m.Keys.First(config.OpenSettings):
		m.closeOverlay()
	case "down", "j", "ctrl+n", "tab":
		m.settingsMove(1)
	case "up", "k", "ctrl+p", "shift+tab":
		m.settingsMove(-1)
	case "ctrl+d", "pgdown":
		m.settingsMove(h / 2)
	case "ctrl+u", "pgup":
		m.settingsMove(-h / 2)
	case "g", "home":
		s.cursor = 0
		m.settingsMove(1)
	case "G", "end":
		s.cursor = len(s.shown) - 1
		m.settingsMove(-1)
		m.settingsMove(1)
	case "enter", "space", "right", "l", "+":
		return m.changeSetting(1)
	case "left", "h", "-":
		return m.changeSetting(-1)
	case "backspace", "delete":
		if item != nil && item.action != "" {
			m.Keys[item.action] = slices.Clone(config.Info(item.action).Default)
			m.rematchSetting()
			if cmd := m.saveKeys(); cmd != nil {
				return cmd
			}
			return m.info(item.label + " reset to " + m.Keys.Help(item.action))
		}
	}
	return nil
}

func (m *Model) changeSetting(delta int) tea.Cmd {
	s := &m.settings
	item := s.current()
	if item == nil {
		return nil
	}
	if item.action != "" {
		s.capturing = true
		return m.info("Press the new key for " + quote(item.label) + " (esc cancels)")
	}
	if item.change == nil {
		return nil
	}
	return item.change(m, delta)
}

func (m *Model) settingsView() string {
	s := m.settings
	h := m.contentHeight()
	labelW, valueW := 28, 18
	out := make([]string, 0, h)
	if len(s.shown) == 0 {
		return " " + m.st.muted.Render("No matching settings")
	}
	for i := s.offset; i < len(s.shown) && len(out) < h; i++ {
		row := s.shown[i]
		it := s.items[row.item]
		if it.header != "" {
			left := " " + m.st.dayHeader.Render(it.header)
			right := m.st.subtle.Render(it.path + " ")
			gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
			if gap < 2 {
				right, gap = "", m.width-lipgloss.Width(left)
			}
			out = append(out, left+strings.Repeat(" ", max(0, gap))+right)
			continue
		}
		sel := i == s.cursor
		gutter := seg{text: "  ", st: m.st.text}
		if sel {
			gutter = seg{text: "▌ ", st: m.st.accent}
		}
		value, valuePos := it.value(m), row.value
		if sel && s.capturing {
			value, valuePos = "press a key…", nil
		}
		left := []seg{gutter}
		left = append(left, fitSegs(it.label, row.label, labelW, m.st.text, m.st.match)...)
		left = append(left, fitSegs(value, valuePos, valueW, m.st.key, m.st.match)...)
		left = append(left, matchSegs(it.desc, row.desc, m.st.muted, m.st.match)...)
		var bg color.Color
		if sel {
			bg = m.st.selBg
		}
		out = append(out, renderRow(left, nil, m.width, bg))
	}
	return strings.Join(out, "\n")
}

// tildify shortens paths under the user's home directory.
func tildify(p string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" && strings.HasPrefix(p, home+string(os.PathSeparator)) {
		return "~" + p[len(home):]
	}
	return p
}

// helpState is the key binding reference.
type helpState struct {
	offset int
	filter filterState
}

func (m *Model) openHelp() {
	if m.mode != modeSettings && m.mode != modeHelp {
		m.backMode = m.mode
	}
	m.mode = modeHelp
	m.help = helpState{}
}

// helpRow is one entry of the help screen: a section header, a key
// binding, or a line of text.
type helpRow struct {
	header string
	keys   []string
	text   string
	name   string // action name, matched by word; empty for plain text
	st     lipgloss.Style
}

func (m *Model) helpItems() []helpRow {
	var out []helpRow
	group := ""
	for _, a := range config.Actions {
		if a.Group != group {
			group = a.Group
			out = append(out, helpRow{header: group})
		}
		out = append(out, helpRow{keys: m.Keys[a.Action], text: a.Desc, name: string(a.Action), st: m.st.text})
	}
	return append(out,
		helpRow{header: "Mouse"},
		helpRow{text: "Click to select · double-click to open · click a dimmed parent to jump to it · wheel scrolls", st: m.st.text},
		helpRow{text: "Press " + m.Keys.First(config.SelectMode) + " to release the mouse for text selection (or hold shift/option while dragging).", st: m.st.text},
		helpRow{header: "Files"},
		helpRow{text: tildify(m.Paths.Home), st: m.st.muted},
	)
}

// helpRows renders the help lines the filter leaves, and counts the
// matching and total non-header rows.
func (m *Model) helpRows() (lines []string, matched, total int) {
	q := m.help.filter.query()
	header := ""
	for _, r := range m.helpItems() {
		if r.header != "" {
			header = r.header
			continue
		}
		total++
		// Binding descriptions are short, so they match fuzzily; the
		// Mouse and Files sentences only by word.
		mode := fuzzy.ByWord
		if r.name != "" {
			mode = fuzzy.Fuzzy
		}
		keyPos, textPos, _, ok := matchRow(q, r.keys, r.text, mode, r.name)
		if !ok {
			continue
		}
		matched++
		if header != "" {
			lines = append(lines, " "+m.st.dayHeader.Render(header))
			header = ""
		}
		var segs []seg
		if r.name != "" {
			segs = append(segs, fitSegs(strings.Join(r.keys, "/"), keyPos, 22, m.st.key, m.st.match)...)
		}
		segs = append(segs, matchSegs(r.text, textPos, r.st, m.st.match)...)
		lines = append(lines, "   "+renderRow(segs, nil, max(0, m.width-3), nil))
	}
	if matched == 0 {
		lines = []string{" " + m.st.muted.Render("No matching key bindings")}
	}
	return lines, matched, total
}

func (m *Model) helpLines() []string {
	lines, _, _ := m.helpRows()
	return lines
}

func (m *Model) helpKey(msg tea.KeyPressMsg) tea.Cmd {
	h := m.contentHeight()
	total := len(m.helpLines())
	scroll := func(n int) { m.help.offset = min(max(0, m.help.offset+n), max(0, total-h)) }
	f := &m.help.filter
	if f.editing {
		switch msg.String() {
		case "down", "ctrl+n":
			scroll(1)
		case "up", "ctrl+p":
			scroll(-1)
		case "pgdown":
			scroll(h / 2)
		case "pgup":
			scroll(-h / 2)
		default:
			changed, cmd := f.key(msg)
			if changed {
				m.refilter()
			}
			return cmd
		}
		return nil
	}
	if m.keyMatches(config.Filter, msg) {
		return m.startFilter(f)
	}
	switch msg.String() {
	case "esc":
		if f.active() {
			f.clear()
			m.help.offset = 0
			return nil
		}
		m.closeOverlay()
	case "q", "?", m.Keys.First(config.Help):
		m.closeOverlay()
	case "down", "j", "ctrl+n":
		scroll(1)
	case "up", "k", "ctrl+p":
		scroll(-1)
	case "ctrl+d", "pgdown", "space":
		scroll(h / 2)
	case "ctrl+u", "pgup":
		scroll(-h / 2)
	case "g", "home":
		m.help.offset = 0
	case "G", "end":
		scroll(total)
	}
	return nil
}

func (m *Model) helpView() string {
	lines := m.helpLines()
	h := m.contentHeight()
	start := min(m.help.offset, max(0, len(lines)-1))
	end := min(len(lines), start+h)
	return strings.Join(lines[start:end], "\n")
}
