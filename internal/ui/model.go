// Package ui is the Bubble Tea terminal interface.
package ui

import (
	"errors"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/relloyd/todotil/internal/config"
	"github.com/relloyd/todotil/internal/links"
	"github.com/relloyd/todotil/internal/store"
	"github.com/relloyd/todotil/internal/todo"
)

type tab int

const (
	tabNow tab = iota
	tabNext
	tabLater
	tabHistory
	numTabs
)

var tabStates = [numTabs]todo.State{todo.Now, todo.Next, todo.Later, todo.Journal}

func (t tab) label() string {
	if t == tabHistory {
		return "History"
	}
	return tabStates[t].Label()
}

func tabFor(s todo.State) tab {
	switch s {
	case todo.Now:
		return tabNow
	case todo.Next:
		return tabNext
	case todo.Later:
		return tabLater
	}
	return tabHistory
}

type mode int

const (
	modeList mode = iota
	modeDetail
	modeSettings
	modeHelp
)

// Deps are the collaborators the UI needs.
type Deps struct {
	Service  *todo.Service
	Log      *store.Log // for backups; nil disables them
	Paths    config.Paths
	Settings config.Settings
	Keys     config.KeyMap
	Palette  config.Palette
	Fetcher  *links.Fetcher // nil disables title fetching
	Now      func() time.Time
	// Clipboard writes to the system clipboard. When it fails the UI falls
	// back to the terminal's OSC 52 clipboard.
	Clipboard func(string) error
	// Warnings are shown in the status bar at startup.
	Warnings []string
	// SettingsInvalid and KeysInvalid are set when the file failed to parse;
	// the app then runs on defaults and won't overwrite the user's file.
	SettingsInvalid, KeysInvalid bool
}

// lineRef maps a rendered list line to a row, or -1 for a spacer.
type lineRef struct {
	row int
}

type jumpPos struct {
	tab    tab
	id     string
	detail bool
}

type statusKind int

const (
	statusInfo statusKind = iota
	statusWarn
	statusError
)

type confirmState struct {
	prompt string
	onYes  func() tea.Cmd
}

// Model is the root Bubble Tea model.
type Model struct {
	Deps
	st styles

	width, height int
	tab           tab
	mode          mode
	backMode      mode // mode to return to from settings/help

	rows   [numTabs][]todo.Row
	lines  [numTabs][]lineRef
	cursor [numTabs]int
	offset [numTabs]int

	detail   detailState
	editor   *editorState
	settings settingsState
	help     helpState

	jumps       []jumpPos
	status      string
	statusKind  statusKind
	statusSeq   int
	pendingMove bool
	confirm     *confirmState
	selectMode  bool

	linkInflight map[string]bool
	linkFailed   map[string]bool
	linkSem      chan struct{}

	nextBackup time.Time

	lastClickAt  time.Time
	lastClickRow int
}

// New builds the model.
func New(d Deps) *Model {
	if d.Now == nil {
		d.Now = time.Now
	}
	m := &Model{
		Deps:         d,
		st:           newStyles(d.Palette),
		width:        80,
		height:       24,
		linkInflight: map[string]bool{},
		linkFailed:   map[string]bool{},
		linkSem:      make(chan struct{}, 4),
		lastClickRow: -1,
	}
	m.refresh()
	if len(d.Warnings) > 0 {
		m.status, m.statusKind = strings.Join(d.Warnings, " · "), statusWarn
	}
	return m
}

// Init starts background work.
func (m *Model) Init() tea.Cmd {
	return tea.Batch(m.startupBackupCmd(), m.backupTick(), m.fetchVisibleLinks())
}

const maxJumps = 50

// board is shorthand for the current state.
func (m *Model) board() *todo.Board { return m.Service.Board() }

// contentHeight is the number of lines available between header and footer.
func (m *Model) contentHeight() int { return max(1, m.height-4) }

// refresh recomputes every tab's rows, keeping each cursor on the same item
// where it still exists.
func (m *Model) refresh() {
	b := m.board()
	for t := range numTabs {
		prevID := m.selectedIDIn(t)
		if t == tabHistory {
			key := todo.SortKey(m.Settings.History.Sort)
			m.rows[t] = b.HistoryRows(key, m.Settings.History.Descending)
		} else {
			m.rows[t] = b.ViewRows(tabStates[t])
		}
		m.lines[t] = buildLines(m.rows[t], t != tabHistory)
		if i := m.rowIndex(t, prevID); i >= 0 {
			m.cursor[t] = i
		}
		m.clampCursor(t, 1)
		m.ensureVisible(t)
	}
	if m.mode == modeDetail {
		m.buildDetail()
	}
}

// buildLines lays out rows as lines: active views get a separator before
// each top-level item, History a blank line before each day.
func buildLines(rows []todo.Row, separators bool) []lineRef {
	lines := make([]lineRef, 0, len(rows)*2)
	for i, r := range rows {
		if i > 0 && ((separators && r.Depth == 0) || r.Kind == todo.RowDay) {
			lines = append(lines, lineRef{row: -1})
		}
		lines = append(lines, lineRef{row: i})
	}
	return lines
}

func (m *Model) selectedIDIn(t tab) string {
	rows := m.rows[t]
	if c := m.cursor[t]; c >= 0 && c < len(rows) && rows[c].Kind == todo.RowItem {
		return rows[c].Item.ID
	}
	return ""
}

// selected returns the item under the cursor in the current tab.
func (m *Model) selected() *todo.Item {
	rows := m.rows[m.tab]
	if c := m.cursor[m.tab]; c >= 0 && c < len(rows) && rows[c].Kind == todo.RowItem {
		return rows[c].Item
	}
	return nil
}

func (m *Model) rowIndex(t tab, id string) int {
	if id == "" {
		return -1
	}
	return slices.IndexFunc(m.rows[t], func(r todo.Row) bool { return r.Kind == todo.RowItem && r.Item.ID == id })
}

// clampCursor keeps the cursor in range and on an item row, searching in
// direction dir first.
func (m *Model) clampCursor(t tab, dir int) {
	rows := m.rows[t]
	if len(rows) == 0 {
		m.cursor[t] = 0
		return
	}
	c := min(max(m.cursor[t], 0), len(rows)-1)
	if dir == 0 {
		dir = 1
	}
	for _, d := range []int{dir, -dir} {
		for i := c; i >= 0 && i < len(rows); i += d {
			if rows[i].Kind == todo.RowItem {
				m.cursor[t] = i
				return
			}
		}
	}
	m.cursor[t] = c
}

// moveCursor moves by n item rows (negative is up).
func (m *Model) moveCursor(n int) {
	t := m.tab
	rows := m.rows[t]
	c := m.cursor[t]
	step := 1
	if n < 0 {
		step, n = -1, -n
	}
	for ; n > 0; n-- {
		next := c + step
		for next >= 0 && next < len(rows) && rows[next].Kind != todo.RowItem {
			next += step
		}
		if next < 0 || next >= len(rows) {
			break
		}
		c = next
	}
	m.cursor[t] = c
	m.ensureVisible(t)
}

func (m *Model) cursorTo(i int, dir int) {
	m.cursor[m.tab] = i
	m.clampCursor(m.tab, dir)
	m.ensureVisible(m.tab)
}

// ensureVisible scrolls so the cursor line (and its day header) is shown.
func (m *Model) ensureVisible(t tab) {
	h := m.contentHeight()
	lines := m.lines[t]
	li := slices.IndexFunc(lines, func(l lineRef) bool { return l.row == m.cursor[t] })
	maxOff := max(0, len(lines)-h)
	if li < 0 {
		m.offset[t] = min(m.offset[t], maxOff)
		return
	}
	top := li
	if li > 0 && lines[li-1].row >= 0 && m.rows[t][lines[li-1].row].Kind == todo.RowDay {
		top = li - 1
	}
	if top < m.offset[t] {
		m.offset[t] = top
	}
	if li >= m.offset[t]+h {
		m.offset[t] = li - h + 1
	}
	m.offset[t] = min(max(m.offset[t], 0), maxOff)
}

func (m *Model) setStatus(kind statusKind, s string) tea.Cmd {
	m.status, m.statusKind = s, kind
	m.statusSeq++
	seq := m.statusSeq
	d := 4 * time.Second
	if kind != statusInfo {
		d = 10 * time.Second
	}
	return tea.Tick(d, func(time.Time) tea.Msg { return clearStatusMsg{seq} })
}

func (m *Model) info(s string) tea.Cmd { return m.setStatus(statusInfo, s) }
func (m *Model) warn(s string) tea.Cmd { return m.setStatus(statusWarn, s) }

// fail reports an error. Refusals of an operation (such as completing a
// journal item) are warnings; anything else, like a failed save, is an error.
func (m *Model) fail(err error) tea.Cmd {
	kind := statusError
	for _, v := range []error{todo.ErrEmpty, todo.ErrNotFound, todo.ErrJournalDone, todo.ErrNotInView, todo.ErrCannotIndent, todo.ErrCannotOutdent} {
		if errors.Is(err, v) {
			kind = statusWarn
		}
	}
	return m.setStatus(kind, capitalise(err.Error()))
}

func capitalise(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

type clearStatusMsg struct{ seq int }

// Update handles messages.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		if m.editor != nil {
			m.layoutEditor()
		}
		m.refresh()
	case tea.KeyPressMsg:
		cmd = m.handleKey(msg)
	case tea.PasteMsg:
		cmd = m.handlePaste(msg)
	case tea.MouseClickMsg:
		cmd = m.handleClick(msg.Mouse())
	case tea.MouseWheelMsg:
		m.handleWheel(msg.Mouse())
	case clearStatusMsg:
		if msg.seq == m.statusSeq {
			m.status = ""
		}
	case linkTitleMsg:
		cmd = m.handleLinkTitle(msg)
	case backupTickMsg:
		cmd = m.handleBackupTick(time.Time(msg))
	case backupDoneMsg:
		if msg.err != nil {
			cmd = m.fail(msg.err)
		}
	case quitMsg:
		return m, tea.Quit
	default:
		if m.editor != nil {
			m.editor.ta, cmd = m.editor.ta.Update(msg)
		}
	}
	return m, tea.Batch(cmd, m.fetchVisibleLinks())
}

type quitMsg struct{}

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
	case it == nil:
		return nil
	case k(config.Open):
		m.openDetail(it.ID)
	case k(config.Edit):
		return m.openEdit(it.ID)
	case k(config.Done):
		return m.toggleDone(it)
	case k(config.Delete):
		return m.askDelete(it)
	case k(config.Copy):
		return m.copyItem(it)
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

// View renders the screen.
func (m *Model) View() tea.View {
	var content string
	switch m.mode {
	case modeDetail:
		content = m.detailView()
	case modeSettings:
		content = m.settingsView()
	case modeHelp:
		content = m.helpView()
	default:
		content = m.listView()
	}
	screen := lipgloss.JoinVertical(lipgloss.Left,
		m.headerView(),
		"",
		lipgloss.NewStyle().Height(m.contentHeight()).MaxHeight(m.contentHeight()).Render(content),
		m.statusView(),
		m.hintsView(),
	)
	if m.editor != nil {
		dlg := m.editorView()
		x := max(0, (m.width-lipgloss.Width(dlg))/2)
		y := max(0, (m.height-lipgloss.Height(dlg))/3)
		screen = lipgloss.NewCompositor(
			lipgloss.NewLayer(screen),
			lipgloss.NewLayer(dlg).X(x).Y(y).Z(1),
		).Render()
	}
	v := tea.NewView(screen)
	v.AltScreen = true
	v.WindowTitle = "todotil"
	if m.Settings.Mouse && !m.selectMode {
		v.MouseMode = tea.MouseModeCellMotion
	}
	return v
}
