package ui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/relloyd/todotil/internal/config"
	"github.com/relloyd/todotil/internal/todo"
)

func (m *Model) keyMatches(a config.Action, msg tea.KeyPressMsg) bool {
	return m.Keys.Matches(a, msg.String(), msg.Keystroke())
}

func (m *Model) handleKey(msg tea.KeyPressMsg) tea.Cmd {
	switch {
	case m.editor != nil:
		return m.editorKey(msg)
	case m.confirm != nil:
		return m.confirmKey(msg)
	case m.pendingMove:
		return m.moveKey(msg)
	case m.mode == modeSettings:
		return m.settingsKey(msg)
	case m.mode == modeHelp:
		return m.helpKey(msg)
	}
	if m.selectMode && (m.keyMatches(config.SelectMode, msg) || msg.String() == "esc") {
		m.selectMode = false
		return m.info("Mouse captured again")
	}
	if cmd, ok := m.globalKey(msg); ok {
		return cmd
	}
	if m.mode == modeDetail {
		return m.detailKey(msg)
	}
	return m.listKey(msg)
}

// globalKey handles keys that work in both the lists and the detail view.
func (m *Model) globalKey(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	k := func(a config.Action) bool { return m.keyMatches(a, msg) }
	switch {
	case k(config.Quit):
		return func() tea.Msg { return quitMsg{} }, true
	case k(config.NextView):
		m.switchTab((m.tab + 1) % numTabs)
	case k(config.PrevView):
		m.switchTab((m.tab + numTabs - 1) % numTabs)
	case k(config.ViewNow):
		m.switchTab(tabNow)
	case k(config.ViewNext):
		m.switchTab(tabNext)
	case k(config.ViewLater):
		m.switchTab(tabLater)
	case k(config.ViewHistory):
		m.switchTab(tabHistory)
	case k(config.OpenSettings):
		m.openSettings()
	case k(config.Help):
		m.openHelp()
	case k(config.Undo):
		return m.undo(), true
	case k(config.SelectMode):
		m.selectMode = true
		return m.info("Mouse released: select text with the mouse, then press " + m.Keys.First(config.SelectMode) + " or esc"), true
	case k(config.JumpParent):
		return m.jumpParent(), true
	case k(config.JumpChild):
		return m.jumpChild(), true
	case k(config.JumpBack):
		return m.jumpBack(), true
	default:
		return nil, false
	}
	return nil, true
}

func (m *Model) switchTab(t tab) {
	m.tab = t
	m.mode = modeList
	m.ensureVisible(t)
}

func (m *Model) listKey(msg tea.KeyPressMsg) tea.Cmd {
	k := func(a config.Action) bool { return m.keyMatches(a, msg) }
	half := max(1, m.contentHeight()/2)
	it := m.selected()
	switch {
	case k(config.Down):
		m.moveCursor(1)
	case k(config.Up):
		m.moveCursor(-1)
	case k(config.Top):
		m.cursorTo(0, 1)
	case k(config.Bottom):
		m.cursorTo(len(m.rows[m.tab])-1, -1)
	case k(config.HalfDown):
		m.moveCursor(half)
	case k(config.HalfUp):
		m.moveCursor(-half)
	case k(config.PageDown):
		m.moveCursor(m.contentHeight())
	case k(config.PageUp):
		m.moveCursor(-m.contentHeight())
	case k(config.Add):
		return m.openAdd("", "")
	case k(config.AddChild):
		if it == nil {
			return m.info("Nothing selected")
		}
		return m.openAdd(it.ID, "")
	case k(config.SortKey) && m.tab == tabHistory:
		return m.toggleSortKey()
	case k(config.SortDir) && m.tab == tabHistory:
		return m.toggleSortDir()
	case k(config.HistoryFilter) && m.tab == tabHistory:
		return m.cycleHistoryFilter()
	case it == nil:
		return nil
	case k(config.Open):
		m.openDetail(it.ID)
	case k(config.Edit):
		return m.openEdit(it.ID)
	case k(config.Done):
		return m.toggleDone(it)
	case k(config.Reject) && it.Open():
		return m.reject(it)
	case k(config.Delete):
		return m.askDelete(it)
	case k(config.Copy):
		return m.copyItem(it)
	case k(config.CopyID):
		return m.copyItemID(it)
	case k(config.Unassign):
		return m.unassign(it)
	case k(config.Move):
		m.pendingMove = true
	case k(config.ItemUp):
		return m.reorder(it, todo.Up)
	case k(config.ItemDown):
		return m.reorder(it, todo.Down)
	case k(config.ItemTop):
		return m.reorder(it, todo.Top)
	case k(config.ItemBottom):
		return m.reorder(it, todo.Bottom)
	case (k(config.Indent) || k(config.Outdent)) && m.tab == tabHistory:
		return m.info("Indent and outdent in Now, Next or Later")
	case k(config.Indent):
		return m.run(m.Service.Indent(it.ID))
	case k(config.Outdent):
		return m.run(m.Service.Outdent(it.ID))
	}
	return nil
}

func (m *Model) confirmKey(msg tea.KeyPressMsg) tea.Cmd {
	c := m.confirm
	m.confirm = nil
	switch msg.String() {
	case "y", "Y", "enter":
		return c.onYes()
	}
	return m.info("Cancelled")
}

// currentItem is the item actions apply to: the detail item or the list
// selection.
func (m *Model) currentItem() *todo.Item {
	if m.mode == modeDetail {
		return m.board().Get(m.detail.id)
	}
	return m.selected()
}

func (m *Model) moveKey(msg tea.KeyPressMsg) tea.Cmd {
	m.pendingMove = false
	it := m.currentItem()
	if it == nil {
		return nil
	}
	k := func(a config.Action) bool { return m.keyMatches(a, msg) }
	var target todo.State
	switch {
	case k(config.MoveNow):
		target = todo.Now
	case k(config.MoveNext):
		target = todo.Next
	case k(config.MoveLater):
		target = todo.Later
	case k(config.MoveJournal):
		target = todo.Journal
	default:
		return m.info("Move cancelled")
	}
	return m.move(it, target)
}
