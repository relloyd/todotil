package ui

import (
	"strconv"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/relloyd/todotil/internal/config"
	"github.com/relloyd/todotil/internal/todo"
)

// editorState is the add/edit dialog.
type editorState struct {
	ta       textarea.Model
	state    todo.State // classification of a new entry
	editID   string     // set when editing an existing item
	parentID string     // set when adding a child
	// base is the item as it was when editing started. If an agent (or
	// another window) changes its text meanwhile, the first save warns and
	// a second save overwrites.
	base      *todo.Item
	conflict  string
	overwrite bool
}

func (m *Model) newTextarea() textarea.Model {
	ta := textarea.New()
	ta.ShowLineNumbers = false
	ta.Prompt = ""
	ta.CharLimit = 0
	// Grow with the content up to MaxHeight rows (set in layoutEditor),
	// then scroll. MaxContentHeight keeps MaxHeight from limiting how long
	// a note can be.
	ta.DynamicHeight = true
	ta.MinHeight = 3
	ta.MaxContentHeight = 10000
	ta.Placeholder = "Title on the first line, then a blank line and an optional body.\n- [ ] checkbox lines become child todos"
	ta.KeyMap.InsertNewline = key.NewBinding(key.WithKeys(m.Keys[config.Newline]...))
	// Keys that belong to the dialog must not reach the textarea.
	for _, a := range []config.Action{config.EntryNow, config.EntryNext, config.EntryLater, config.EntryJrnl} {
		for _, k := range m.Keys[a] {
			if k == "alt+l" {
				ta.KeyMap.LowercaseWordForward.SetEnabled(false)
			}
		}
	}
	bg := m.st.dialogBg
	c := lipgloss.Color
	s := textarea.DefaultStyles(true)
	for _, st := range []*textarea.StyleState{&s.Focused, &s.Blurred} {
		st.Base = lipgloss.NewStyle().Background(bg)
		st.Text = lipgloss.NewStyle().Foreground(c(m.Palette.Text)).Background(bg)
		st.CursorLine = lipgloss.NewStyle().Foreground(c(m.Palette.Text)).Background(bg)
		st.Placeholder = lipgloss.NewStyle().Foreground(c(m.Palette.Muted)).Background(bg)
		st.EndOfBuffer = lipgloss.NewStyle().Foreground(bg).Background(bg)
		st.Prompt = lipgloss.NewStyle().Background(bg)
	}
	s.Cursor.Color = c(m.Palette.Accent)
	ta.SetStyles(s)
	return ta
}

func (m *Model) layoutEditor() {
	m.editor.ta.MaxHeight = min(16, max(3, m.height-12))
	m.editor.ta.SetWidth(m.editorWidth() - 4)
}

func (m *Model) editorWidth() int { return min(90, max(30, m.width-6)) }

// openAdd opens the dialog for a new entry, classified by the current view.
func (m *Model) openAdd(parentID, initial string) tea.Cmd {
	st := tabStates[m.tab]
	if parentID != "" {
		if p := m.board().Get(parentID); p != nil && p.State.Active() {
			st = p.State
		} else if !st.Active() {
			st = todo.Now
		}
	}
	m.editor = &editorState{ta: m.newTextarea(), state: st, parentID: parentID}
	m.layoutEditor()
	if initial != "" {
		m.editor.ta.InsertString(initial)
	}
	m.pendingMove, m.confirm = false, nil
	return m.editor.ta.Focus()
}

func (m *Model) openEdit(id string) tea.Cmd {
	it := m.board().Get(id)
	if it == nil {
		return nil
	}
	m.editor = &editorState{ta: m.newTextarea(), state: it.State, editID: id, base: it}
	m.layoutEditor()
	m.editor.ta.SetValue(it.Text())
	m.editor.ta.MoveToBegin()
	return m.editor.ta.Focus()
}

func (m *Model) editorKey(msg tea.KeyPressMsg) tea.Cmd {
	e := m.editor
	k := func(a config.Action) bool { return m.keyMatches(a, msg) }
	if e.editID == "" {
		switch {
		case k(config.EntryNow):
			e.state = todo.Now
			return nil
		case k(config.EntryNext):
			e.state = todo.Next
			return nil
		case k(config.EntryLater):
			e.state = todo.Later
			return nil
		case k(config.EntryJrnl):
			e.state = todo.Journal
			return nil
		}
	}
	switch {
	case k(config.Cancel), msg.String() == "ctrl+c":
		m.editor = nil
		return nil
	case k(config.Submit):
		return m.submitEditor()
	}
	var cmd tea.Cmd
	e.ta, cmd = e.ta.Update(msg)
	return cmd
}

func (m *Model) handlePaste(msg tea.PasteMsg) tea.Cmd {
	if m.editor != nil {
		var cmd tea.Cmd
		m.editor.ta, cmd = m.editor.ta.Update(msg)
		return cmd
	}
	if f := m.currentFilter(); f != nil && f.editing {
		return m.filterInput(f, msg)
	}
	if m.mode == modeList && m.confirm == nil && strings.TrimSpace(msg.Content) != "" {
		return m.openAdd("", msg.Content)
	}
	return nil
}

func (m *Model) submitEditor() tea.Cmd {
	e := m.editor
	text := e.ta.Value()
	if strings.TrimSpace(text) == "" {
		m.editor = nil
		return nil
	}
	var (
		res todo.Result
		err error
	)
	if e.editID != "" {
		if blocked := m.editConflict(); blocked {
			return nil
		}
		res, err = m.Service.Edit(e.editID, text, m.childState())
	} else {
		res, err = m.Service.Add(text, e.state, e.parentID, m.childState())
	}
	if err != nil {
		return m.fail(err)
	}
	m.editor = nil
	m.refresh()
	var msgs []string
	if e.editID != "" {
		msgs = append(msgs, "Saved "+quote(res.Item.Title))
	} else {
		t := tabOf(res.Item)
		if t == m.tab {
			i := m.rowIndex(t, res.Item.ID)
			m.cursor[t] = max(0, i)
			m.ensureVisible(t)
			msgs = append(msgs, "Added "+quote(res.Item.Title))
			if i < 0 && m.filters[t].active() {
				msgs = append(msgs, "hidden by the search")
			}
		} else {
			msgs = append(msgs, "Added "+quote(res.Item.Title)+" to "+t.label())
		}
	}
	if s := syncText(res.Sync); s != "" {
		msgs = append(msgs, s)
	}
	text = strings.Join(msgs, " · ")
	if res.Sync.Bulk() {
		return m.warn(text + m.undoHint())
	}
	return m.info(text)
}

// editConflict reports whether saving should wait because the item's text
// changed elsewhere since the dialog opened. The first save shows a warning
// in the dialog; saving again overwrites.
func (m *Model) editConflict() bool {
	e := m.editor
	if _, err := m.Service.Sync(); err == nil {
		m.refresh()
	}
	cur := m.board().Get(e.editID)
	if cur == nil {
		e.conflict = "This item was deleted elsewhere. Copy anything you need, then press esc."
		return true
	}
	if e.overwrite || (cur.Title == e.base.Title && cur.Body == e.base.Body) {
		return false
	}
	who := m.board().LastActor(e.editID)
	if who == "" {
		who = "another todotil window"
	}
	e.conflict = who + " changed this text since you opened it. Press " +
		m.Keys.First(config.Submit) + " again to overwrite, or esc to cancel."
	e.overwrite = true
	return true
}

// syncText summarises a checkbox sync for the status bar.
func syncText(s todo.SyncSummary) string {
	var parts []string
	add := func(n int, what string) {
		if n > 0 {
			parts = append(parts, strconv.Itoa(n)+" "+what)
		}
	}
	add(s.Created, "created")
	add(s.Rewritten, "reworded")
	add(s.Removed, "removed")
	add(s.Detached, "detached")
	add(s.Completed, "completed")
	add(s.Rejected, "rejected")
	add(s.Reopened, "reopened")
	if len(parts) == 0 {
		return ""
	}
	return "child todos: " + strings.Join(parts, ", ")
}

func (m *Model) editorView() string {
	e := m.editor
	w := m.editorWidth()
	inner := w - 4
	bg := lipgloss.NewStyle().Background(m.st.dialogBg)
	var title string
	switch {
	case e.editID != "":
		title = "Edit"
	case e.parentID != "":
		title = "New child of " + quote(m.board().Get(e.parentID).Title)
	default:
		title = "New entry"
	}
	head := m.st.dialogTitle.Render(title)
	if e.editID == "" {
		badge := m.st.badge(e.state)
		gap := inner - lipgloss.Width(head) - lipgloss.Width(badge)
		head += bg.Render(strings.Repeat(" ", max(1, gap))) + badge
	}
	keys := func(a config.Action) string { return m.Keys.First(a) }
	hints := []string{keys(config.Submit) + " save", keys(config.Newline) + " newline"}
	if e.editID == "" {
		hints = append(hints, keys(config.EntryNow)+"/"+strings.TrimPrefix(keys(config.EntryNext), "alt+")+"/"+
			strings.TrimPrefix(keys(config.EntryLater), "alt+")+"/"+strings.TrimPrefix(keys(config.EntryJrnl), "alt+")+" classify")
	}
	hints = append(hints, keys(config.Cancel)+" cancel")
	hint := m.st.muted.Background(m.st.dialogBg).Width(inner).Render(strings.Join(hints, " · "))
	parts := []string{
		bg.Width(inner).Render(head),
		bg.Width(inner).Render(""),
		e.ta.View(),
		bg.Width(inner).Render(""),
	}
	if e.conflict != "" {
		parts = append(parts, m.st.warn.Background(m.st.dialogBg).Width(inner).Render(e.conflict))
	}
	body := lipgloss.JoinVertical(lipgloss.Left, append(parts, hint)...)
	return m.st.dialog.Width(w).Render(body)
}
