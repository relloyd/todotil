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
	historyFilter todo.HistoryFilter
	mode          mode
	backMode      mode // mode to return to from settings/help

	rows [numTabs][]todo.Row
	// filters are the per-tab searches; rows holds only what they match, and
	// totals counts the items each tab would show unfiltered.
	filters [numTabs]filterState
	totals  [numTabs]int
	lines   [numTabs][]lineRef
	cursor  [numTabs]int
	offset  [numTabs]int

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

	nextBackup  time.Time
	lastSyncErr string

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
	return tea.Batch(m.startupBackupCmd(), m.backupTick(), m.syncTick(), m.fetchVisibleLinks())
}

const maxJumps = 50

// board is shorthand for the current state.
func (m *Model) board() *todo.Board { return m.Service.Board() }

// contentHeight is the number of lines available between header and footer.
func (m *Model) contentHeight() int { return max(1, m.height-4) }

// refresh recomputes every tab's rows, keeping each cursor on the same item
// where it still exists.
func (m *Model) refresh() {
	for t := range numTabs {
		m.refreshTab(t)
	}
	if m.mode == modeDetail {
		m.buildDetail()
	}
}

// refreshTab recomputes one tab's rows, applying its search.
func (m *Model) refreshTab(t tab) {
	b := m.board()
	prevID := m.selectedIDIn(t)
	var rows []todo.Row
	if t == tabHistory {
		key := todo.SortKey(m.Settings.History.Sort)
		rows = b.HistoryRows(key, m.Settings.History.Descending, m.historyFilter)
	} else {
		rows = b.ViewRows(tabStates[t])
	}
	m.totals[t] = countItems(rows)
	m.rows[t] = m.filterRows(t, rows)
	m.lines[t] = buildLines(m.rows[t], t != tabHistory)
	if i := m.rowIndex(t, prevID); i >= 0 {
		m.cursor[t] = i
	}
	m.clampCursor(t, 1)
	m.ensureVisible(t)
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
	for _, v := range []error{todo.ErrEmpty, todo.ErrNotFound, todo.ErrJournalDone, todo.ErrJournalReject,
		todo.ErrCannotRejectDone, todo.ErrRejected, todo.ErrNotInView,
		todo.ErrCannotIndent, todo.ErrCannotOutdent, todo.ErrNotClaimed, todo.ErrConflict} {
		if errors.Is(err, v) {
			kind = statusWarn
		}
	}
	var uc *todo.UndoConflictError
	if errors.As(err, &uc) {
		kind = statusWarn
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
	case syncTickMsg:
		cmd = m.handleSyncTick()
	case backupDoneMsg:
		if msg.err != nil {
			cmd = m.fail(msg.err)
		}
	case quitMsg:
		return m, tea.Quit
	default:
		if m.editor != nil {
			m.editor.ta, cmd = m.editor.ta.Update(msg)
		} else if f := m.currentFilter(); f != nil && f.editing {
			cmd = m.filterInput(f, msg) // e.g. text pasted with ctrl+v
		}
	}
	return m, tea.Batch(cmd, m.fetchVisibleLinks())
}

type quitMsg struct{}

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
