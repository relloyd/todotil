package ui

import (
	"errors"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/relloyd/todotil/internal/config"
	"github.com/relloyd/todotil/internal/todo"
)

// quote shortens a title for status messages.
func quote(s string) string { return "“" + ansi.Truncate(clean(s), 40, "…") + "”" }

// run refreshes after an operation, reporting any error.
func (m *Model) run(err error) tea.Cmd {
	if err != nil {
		return m.fail(err)
	}
	m.refresh()
	return nil
}

// childState is where checkbox children of a journal note go when created
// from the current view.
func (m *Model) childState() todo.State {
	if m.tab != tabHistory {
		return tabStates[m.tab]
	}
	st, err := todo.ParseState(m.Settings.JournalChildState)
	if err != nil || !st.Active() {
		return todo.Now
	}
	return st
}

func (m *Model) undoHint() string { return " — " + m.Keys.First(config.Undo) + " to undo" }

func (m *Model) undo() tea.Cmd {
	label, err := m.Service.Undo()
	if errors.Is(err, todo.ErrNothingToUndo) {
		return m.info("Nothing to undo")
	}
	if err != nil {
		return m.fail(err)
	}
	m.refresh()
	return m.info("Undid " + label)
}

func (m *Model) move(it *todo.Item, target todo.State) tea.Cmd {
	res, err := m.Service.Move(it.ID, target)
	if err != nil {
		return m.fail(err)
	}
	m.refresh()
	if res.Item == nil {
		return m.info("Already in " + target.Label())
	}
	msg := "Moved " + quote(it.Title) + " to " + target.Label()
	if target == todo.Journal {
		msg += " (it stays in History)"
	}
	if res.Followers > 0 {
		msg += fmt.Sprintf(" with %d %s", res.Followers, plural(res.Followers, "child", "children"))
	}
	return m.info(msg + m.undoHint())
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func (m *Model) toggleDone(it *todo.Item) tea.Cmd {
	if it.Done() || it.Rejected() {
		if _, err := m.Service.Reopen(it.ID); err != nil {
			return m.fail(err)
		}
		m.refresh()
		return m.info("Reopened " + quote(it.Title))
	}
	return m.closeItem(it, "Complete", "Completed", m.Service.Complete)
}

func (m *Model) reject(it *todo.Item) tea.Cmd {
	if it.Rejected() {
		return m.info(quote(it.Title) + " is already rejected; " + m.Keys.First(config.Done) + " reopens it")
	}
	return m.closeItem(it, "Reject", "Rejected", m.Service.Reject)
}

// closeItem completes or rejects it through fn. If it has open children,
// fn refuses with *todo.NeedsConfirmError and the user is asked before fn
// runs again with force, taking the children too.
func (m *Model) closeItem(it *todo.Item, verb, done string, fn func(id string, force bool) (todo.Result, error)) tea.Cmd {
	_, err := fn(it.ID, false)
	var nc *todo.NeedsConfirmError
	switch {
	case errors.As(err, &nc):
		id, title := it.ID, it.Title
		m.confirm = &confirmState{
			prompt: fmt.Sprintf("%s %s and its %d open %s?", verb, quote(title), nc.Open, plural(nc.Open, "child", "children")),
			onYes: func() tea.Cmd {
				res, err := fn(id, true)
				if err != nil {
					return m.fail(err)
				}
				m.refresh()
				msg := done + " " + quote(title)
				if res.Followers > 0 {
					msg += " and its children"
				}
				return m.info(msg + m.undoHint())
			},
		}
		return nil
	case err != nil:
		return m.fail(err)
	}
	m.refresh()
	return m.info(done + " " + quote(it.Title) + m.undoHint())
}

func (m *Model) unassign(it *todo.Item) tea.Cmd {
	c, err := m.Service.Unassign(it.ID)
	if err != nil {
		return m.fail(err)
	}
	m.refresh()
	return m.info("Unassigned " + c.Assignee + " from " + quote(it.Title) + m.undoHint())
}

func (m *Model) askDelete(it *todo.Item) tea.Cmd {
	id, title := it.ID, it.Title
	prompt := "Delete " + quote(title) + "?"
	if len(m.board().Children(id)) > 0 {
		prompt = "Delete " + quote(title) + "? Its children move up a level."
	}
	m.confirm = &confirmState{prompt: prompt, onYes: func() tea.Cmd {
		if err := m.Service.Delete(id); err != nil {
			return m.fail(err)
		}
		if m.mode == modeDetail && m.detail.id == id {
			m.mode = modeList
		}
		m.refresh()
		return m.info("Deleted " + quote(title) + m.undoHint())
	}}
	return nil
}

func (m *Model) reorder(it *todo.Item, dir todo.Direction) tea.Cmd {
	if m.tab == tabHistory {
		return m.info("History is ordered by date; reorder in Now, Next or Later")
	}
	return m.run(m.Service.Reorder(it.ID, dir))
}

func (m *Model) saveSettings() tea.Cmd {
	if m.SettingsInvalid {
		return m.warn("Not saved: fix the errors in " + tildify(m.Paths.Settings()) + " first")
	}
	if err := config.SaveSettings(m.Paths, m.Settings); err != nil {
		return m.fail(err)
	}
	return nil
}

func (m *Model) saveKeys() tea.Cmd {
	if m.KeysInvalid {
		return m.warn("Not saved: fix the errors in " + tildify(m.Paths.Keys()) + " first")
	}
	if err := config.SaveKeys(m.Paths, m.Keys); err != nil {
		return m.fail(err)
	}
	return nil
}

func (m *Model) toggleSortKey() tea.Cmd {
	if m.Settings.History.Sort == string(todo.SortCreated) {
		m.Settings.History.Sort = string(todo.SortCompleted)
	} else {
		m.Settings.History.Sort = string(todo.SortCreated)
	}
	m.refresh()
	if cmd := m.saveSettings(); cmd != nil {
		return cmd
	}
	sortLabel := "created"
	if m.Settings.History.Sort == string(todo.SortCompleted) {
		sortLabel = "completed/rejected"
	}
	return m.info("History sorted by " + sortLabel + " date")
}

func (m *Model) cycleHistoryFilter() tea.Cmd {
	m.historyFilter++
	if m.historyFilter > todo.HistoryJournal {
		m.historyFilter = todo.HistoryAll
	}
	m.refresh()
	return m.info("History filter: " + m.historyFilter.Label())
}

func (m *Model) toggleSortDir() tea.Cmd {
	m.Settings.History.Descending = !m.Settings.History.Descending
	m.refresh()
	dir := "newest first"
	if !m.Settings.History.Descending {
		dir = "oldest first"
	}
	if cmd := m.saveSettings(); cmd != nil {
		return cmd
	}
	return m.info("History " + dir)
}

// here captures the current position for the jump history.
func (m *Model) here() jumpPos {
	if m.mode == modeDetail {
		return jumpPos{tab: m.tab, id: m.detail.id, detail: true}
	}
	return jumpPos{tab: m.tab, id: m.selectedIDIn(m.tab)}
}

// tabOf returns the view that shows an item.
func tabOf(it *todo.Item) tab {
	if it.Open() {
		return tabFor(it.State)
	}
	return tabHistory
}

// jumpTo moves to the view holding id and selects it, recording where we
// were so jumpBack can return.
func (m *Model) jumpTo(id string) tea.Cmd {
	it := m.board().Get(id)
	if it == nil {
		return m.info("That item no longer exists")
	}
	t := tabOf(it)
	i := m.rowIndex(t, id)
	cleared := false
	if i < 0 && t == tabHistory && m.historyFilter != todo.HistoryAll {
		// The History filter hides it: show everything rather than fail.
		m.historyFilter = todo.HistoryAll
		m.refresh()
		i, cleared = m.rowIndex(t, id), true
	}
	if i < 0 {
		return m.info("Can't find " + quote(it.Title))
	}
	m.jumps = append(m.jumps, m.here())
	if len(m.jumps) > maxJumps {
		m.jumps = m.jumps[len(m.jumps)-maxJumps:]
	}
	m.mode = modeList
	m.tab = t
	m.cursor[t] = i
	m.ensureVisible(t)
	if cleared {
		return m.info("History filter cleared to show " + quote(it.Title))
	}
	return nil
}

func (m *Model) jumpParent() tea.Cmd {
	it := m.currentItem()
	if it == nil {
		return nil
	}
	if m.board().Get(it.Parent) == nil {
		return m.info(quote(it.Title) + " has no parent")
	}
	return m.jumpTo(it.Parent)
}

// firstChild is the first open child in sibling order, or else the first
// child.
func firstChild(b *todo.Board, id string) *todo.Item {
	kids := b.Children(id)
	for _, k := range kids {
		if k.Open() {
			return k
		}
	}
	if len(kids) > 0 {
		return kids[0]
	}
	return nil
}

func (m *Model) jumpChild() tea.Cmd {
	it := m.currentItem()
	if it == nil {
		return nil
	}
	c := firstChild(m.board(), it.ID)
	if c == nil {
		return m.info(quote(it.Title) + " has no children")
	}
	return m.jumpTo(c.ID)
}

func (m *Model) jumpBack() tea.Cmd {
	if len(m.jumps) == 0 {
		return m.info("No earlier position")
	}
	p := m.jumps[len(m.jumps)-1]
	m.jumps = m.jumps[:len(m.jumps)-1]
	m.tab = p.tab
	m.mode = modeList
	if i := m.rowIndex(p.tab, p.id); i >= 0 {
		m.cursor[p.tab] = i
	}
	m.ensureVisible(p.tab)
	if p.detail && m.board().Get(p.id) != nil {
		m.openDetail(p.id)
	}
	return nil
}

// itemMarkdown renders an item and its non-checkbox children as text.
func itemMarkdown(b *todo.Board, it *todo.Item) string {
	var sb strings.Builder
	sb.WriteString(it.Text())
	var walk func(id string, depth int)
	first := true
	walk = func(id string, depth int) {
		for _, c := range b.Children(id) {
			if depth == 0 && c.Source == it.ID {
				continue // already a line in the body
			}
			if first {
				sb.WriteString("\n\n")
				first = false
			}
			mark := " "
			switch {
			case c.Done():
				mark = "x"
			case c.Rejected():
				mark = "-" // body lines of rejected checkbox children match
			}
			fmt.Fprintf(&sb, "%s- [%s] %s\n", strings.Repeat("  ", depth), mark, c.Title)
			walk(c.ID, depth+1)
		}
	}
	walk(it.ID, 0)
	if len(it.Notes) > 0 {
		sb.WriteString("\n\nNotes:\n")
		for _, n := range it.Notes {
			who := n.By
			if who == "" {
				who = "you"
			}
			fmt.Fprintf(&sb, "- %s %s: %s\n", n.At.Local().Format("2006-01-02 15:04"), who, n.Text)
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

func (m *Model) copyItem(it *todo.Item) tea.Cmd {
	return m.copyText(itemMarkdown(m.board(), it), "Copied "+quote(it.Title))
}

func (m *Model) copyItemID(it *todo.Item) tea.Cmd {
	return m.copyText(it.ID, "Copied ID "+it.ID)
}

func (m *Model) copyText(s, ok string) tea.Cmd {
	if m.Clipboard != nil {
		if err := m.Clipboard(s); err == nil {
			return m.info(ok)
		}
	}
	return tea.Batch(tea.SetClipboard(s), m.info(ok+" (via terminal clipboard)"))
}
