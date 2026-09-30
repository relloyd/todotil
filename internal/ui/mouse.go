package ui

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/relloyd/todotil/internal/todo"
)

// contentTop is the screen row where the content area starts.
const contentTop = 2

const doubleClick = 400 * time.Millisecond

func (m *Model) handleClick(ms tea.Mouse) tea.Cmd {
	if m.editor != nil || m.confirm != nil || ms.Button != tea.MouseLeft {
		return nil
	}
	m.pendingMove = false
	if ms.Y == 0 {
		if t := m.tabAt(ms.X); t >= 0 {
			m.switchTab(t)
		}
		return nil
	}
	line := ms.Y - contentTop
	if line < 0 || line >= m.contentHeight() {
		return nil
	}
	switch m.mode {
	case modeList:
		return m.clickList(line, ms.X)
	case modeDetail:
		if id, ok := m.detail.targets[m.detail.offset+line]; ok {
			return m.jumpTo(id)
		}
	case modeSettings:
		i := m.settings.offset + line
		if i < len(m.settings.shown) && !m.settings.isHeader(i) {
			again := i == m.settings.cursor && time.Since(m.lastClickAt) < doubleClick
			m.settings.cursor = i
			m.lastClickAt = time.Now()
			if again {
				return m.changeSetting(1)
			}
		}
	}
	return nil
}

func (m *Model) clickList(line, x int) tea.Cmd {
	t := m.tab
	li := m.offset[t] + line
	if li >= len(m.lines[t]) {
		return nil
	}
	ri := m.lines[t][li].row
	if ri < 0 || m.rows[t][ri].Kind != todo.RowItem {
		return nil
	}
	r := m.rows[t][ri]
	if x0, x1, ok := m.contextSpan(r); ok && x >= x0 && x < x1 {
		m.cursor[t] = ri
		return m.jumpTo(r.Item.Parent)
	}
	now := time.Now()
	double := ri == m.lastClickRow && m.cursor[t] == ri && now.Sub(m.lastClickAt) < doubleClick
	m.cursor[t] = ri
	m.lastClickRow, m.lastClickAt = ri, now
	if double {
		m.lastClickRow = -1
		m.openDetail(r.Item.ID)
	}
	return nil
}

func (m *Model) handleWheel(ms tea.Mouse) {
	n := 0
	switch ms.Button {
	case tea.MouseWheelDown:
		n = 3
	case tea.MouseWheelUp:
		n = -3
	default:
		return
	}
	switch {
	case m.editor != nil:
	case m.mode == modeList:
		m.moveCursor(n)
	case m.mode == modeDetail:
		m.scrollDetail(n)
	case m.mode == modeSettings:
		m.settingsMove(n)
	case m.mode == modeHelp:
		total := len(m.helpLines())
		m.help.offset = min(max(0, m.help.offset+n), max(0, total-m.contentHeight()))
	}
}
