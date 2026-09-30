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
	items     []setting
	cursor    int
	offset    int
	capturing bool
}

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
	m.settings = settingsState{items: m.settingsItems(), cursor: 1}
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
		for next >= 0 && next < len(s.items) && s.items[next].header != "" {
			next += step
		}
		if next < 0 || next >= len(s.items) {
			break
		}
		c = next
	}
	s.cursor = c
	h := m.contentHeight()
	if s.cursor-1 < s.offset {
		s.offset = max(0, s.cursor-1)
	}
	if s.cursor >= s.offset+h {
		s.offset = s.cursor - h + 1
	}
}

func (m *Model) settingsKey(msg tea.KeyPressMsg) tea.Cmd {
	s := &m.settings
	item := s.items[s.cursor]
	if s.capturing {
		s.capturing = false
		if msg.String() == "esc" {
			return m.info("Rebinding cancelled")
		}
		k := msg.String()
		m.Keys[item.action] = []string{k}
		if cmd := m.saveKeys(); cmd != nil {
			return cmd
		}
		return m.info(fmt.Sprintf("%s is now %s", item.label, k))
	}
	h := m.contentHeight()
	switch msg.String() {
	case "esc", "q", m.Keys.First(config.OpenSettings):
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
		s.cursor = len(s.items) - 1
		m.settingsMove(-1)
		m.settingsMove(1)
	case "enter", "space", "right", "l", "+":
		return m.changeSetting(1)
	case "left", "h", "-":
		return m.changeSetting(-1)
	case "backspace", "delete":
		if item.action != "" {
			m.Keys[item.action] = slices.Clone(config.Info(item.action).Default)
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
	item := s.items[s.cursor]
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
	for i := s.offset; i < len(s.items) && len(out) < h; i++ {
		it := s.items[i]
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
		value := it.value(m)
		if sel && s.capturing {
			value = "press a key…"
		}
		left := []seg{
			gutter,
			{text: fit(it.label, labelW), st: m.st.text},
			{text: fit(value, valueW), st: m.st.key},
			{text: it.desc, st: m.st.muted},
		}
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
}

func (m *Model) openHelp() {
	if m.mode != modeSettings && m.mode != modeHelp {
		m.backMode = m.mode
	}
	m.mode = modeHelp
	m.help = helpState{}
}

func (m *Model) helpLines() []string {
	var out []string
	group := ""
	for _, a := range config.Actions {
		if a.Group != group {
			if group != "" {
				out = append(out, "")
			}
			group = a.Group
			out = append(out, " "+m.st.dayHeader.Render(group))
		}
		keys := m.Keys.Help(a.Action)
		out = append(out, "   "+m.st.key.Render(fit(keys, 22))+m.st.text.Render(a.Desc))
	}
	out = append(out, "",
		" "+m.st.dayHeader.Render("Mouse"),
		"   "+m.st.text.Render("Click to select · double-click to open · click a dimmed parent to jump to it · wheel scrolls"),
		"   "+m.st.text.Render("Press "+m.Keys.First(config.SelectMode)+" to release the mouse for text selection (or hold shift/option while dragging)."),
		"",
		" "+m.st.dayHeader.Render("Files"),
		"   "+m.st.muted.Render(tildify(m.Paths.Home)),
	)
	return out
}

func (m *Model) helpKey(msg tea.KeyPressMsg) tea.Cmd {
	h := m.contentHeight()
	total := len(m.helpLines())
	scroll := func(n int) { m.help.offset = min(max(0, m.help.offset+n), max(0, total-h)) }
	switch msg.String() {
	case "esc", "q", "?", m.Keys.First(config.Help):
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
	end := min(len(lines), m.help.offset+h)
	return strings.Join(lines[m.help.offset:end], "\n")
}
