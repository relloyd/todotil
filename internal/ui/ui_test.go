package ui

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/relloyd/todotil/internal/config"
	"github.com/relloyd/todotil/internal/links"
	"github.com/relloyd/todotil/internal/store"
	"github.com/relloyd/todotil/internal/todo"
)

var now0 = time.Date(2026, 9, 29, 10, 0, 0, 0, time.Local)

type harness struct {
	t       *testing.T
	m       *Model
	svc     *todo.Service
	copied  string
	cmds    []tea.Cmd
	pathsTo config.Paths
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{t: t}
	svc := todo.NewService(todo.NewBoard(), nil, 10)
	clock := now0
	svc.Now = func() time.Time { clock = clock.Add(time.Minute); return clock }
	h.svc = svc
	h.pathsTo = config.Paths{Home: t.TempDir()}
	h.m = New(Deps{
		Service:   svc,
		Paths:     h.pathsTo,
		Settings:  config.DefaultSettings(),
		Keys:      config.DefaultKeyMap(),
		Palette:   config.Themes[config.DefaultThemeName],
		Now:       func() time.Time { return clock },
		Clipboard: func(s string) error { h.copied = s; return nil },
	})
	h.send(tea.WindowSizeMsg{Width: 100, Height: 30})
	return h
}

// keyMsg builds a key press from a Bubble Tea key string such as "a",
// "enter", "ctrl+o", "alt+x" or "shift+up".
func keyMsg(s string) tea.KeyPressMsg {
	named := map[string]rune{
		"enter": tea.KeyEnter, "tab": tea.KeyTab, "esc": tea.KeyEscape, "space": tea.KeySpace,
		"up": tea.KeyUp, "down": tea.KeyDown, "left": tea.KeyLeft, "right": tea.KeyRight,
		"backspace": tea.KeyBackspace, "home": tea.KeyHome, "end": tea.KeyEnd,
	}
	var mod tea.KeyMod
	parts := strings.Split(s, "+")
	name := parts[len(parts)-1]
	if name == "" { // the "+" key itself
		name = "+"
		parts = parts[:len(parts)-1]
	}
	for _, p := range parts[:len(parts)-1] {
		switch p {
		case "ctrl":
			mod |= tea.ModCtrl
		case "alt":
			mod |= tea.ModAlt
		case "shift":
			mod |= tea.ModShift
		}
	}
	if r, ok := named[name]; ok {
		k := tea.KeyPressMsg{Code: r, Mod: mod}
		if r == tea.KeySpace && mod == 0 {
			k.Text = " "
		}
		return k
	}
	r, _ := utf8.DecodeRuneInString(name)
	k := tea.KeyPressMsg{Code: r, Mod: mod}
	if mod&(tea.ModCtrl|tea.ModAlt) == 0 {
		k.Text = name
	}
	return k
}

func (h *harness) send(msg tea.Msg) {
	h.t.Helper()
	_, cmd := h.m.Update(msg)
	h.cmds = append(h.cmds, cmd)
}

func (h *harness) keys(keys ...string) {
	h.t.Helper()
	for _, k := range keys {
		h.send(keyMsg(k))
	}
}

func (h *harness) typeText(s string) {
	h.t.Helper()
	for _, r := range s {
		if r == '\n' {
			h.send(keyMsg("ctrl+j"))
			continue
		}
		h.send(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

func (h *harness) screen() string {
	return ansi.Strip(h.m.View().Content)
}

// add creates an entry through the dialog. extra keys (like "alt+x") are
// pressed after typing.
func (h *harness) add(text string, extra ...string) {
	h.t.Helper()
	h.keys("a")
	require.NotNil(h.t, h.m.editor)
	h.typeText(text)
	h.keys(extra...)
	h.keys("enter")
	require.Nil(h.t, h.m.editor, "dialog should close")
}

func (h *harness) selectedTitle() string {
	if it := h.m.selected(); it != nil {
		return it.Title
	}
	return ""
}

func (h *harness) viewTitles(t tab) []string {
	var out []string
	for _, r := range h.m.rows[t] {
		if r.Kind == todo.RowItem {
			out = append(out, r.Item.Title)
		}
	}
	return out
}

func TestAddUsesViewDefaultAndOverrides(t *testing.T) {
	tests := []struct {
		name  string
		view  string
		extra []string
		want  tab
	}{
		{"now view default", "1", nil, tabNow},
		{"next view default", "2", nil, tabNext},
		{"later view default", "3", nil, tabLater},
		{"history defaults to journal", "4", nil, tabHistory},
		{"override to next", "1", []string{"alt+x"}, tabNext},
		{"override to later", "1", []string{"alt+l"}, tabLater},
		{"override to journal", "2", []string{"alt+j"}, tabHistory},
		{"last override wins", "1", []string{"alt+j", "alt+n"}, tabNow},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			h.keys(tt.view)
			h.add("Buy milk", tt.extra...)
			assert.Contains(t, h.viewTitles(tt.want), "Buy milk")
			it := h.svc.Board().All()[0]
			assert.Equal(t, tt.want == tabHistory, it.State == todo.Journal)
		})
	}
}

func TestDialogShowsClassificationBadge(t *testing.T) {
	h := newHarness(t)
	h.keys("2", "a")
	assert.Contains(t, h.screen(), " Next ")
	h.keys("alt+j")
	assert.Contains(t, h.screen(), " Journal ")
	h.keys("esc")
	assert.Nil(t, h.m.editor)
	assert.Zero(t, h.svc.Board().Len())
}

func TestTitleBodyAndCheckboxes(t *testing.T) {
	h := newHarness(t)
	h.add("Standup\n\nNotes here\n- [ ] follow up with Sam\n- [ ] book room")
	assert.Equal(t, []string{"Standup", "follow up with Sam", "book room"}, h.viewTitles(tabNow))
	s := h.screen()
	assert.Contains(t, s, "Standup ≡")
	assert.Contains(t, s, "• follow up with Sam")
	assert.NotContains(t, s, "Notes here", "list shows only titles")

	h.keys("enter")
	require.Equal(t, modeDetail, h.m.mode)
	s = h.screen()
	assert.Contains(t, s, "Notes here")
	assert.Contains(t, s, "☐ follow up with Sam")
	h.keys("esc")
	assert.Equal(t, modeList, h.m.mode)
}

func TestNavigationKeys(t *testing.T) {
	h := newHarness(t)
	for _, s := range []string{"one", "two", "three"} {
		h.add(s)
	}
	h.keys("g")
	assert.Equal(t, "one", h.selectedTitle())
	h.keys("j", "j")
	assert.Equal(t, "three", h.selectedTitle())
	h.keys("k")
	assert.Equal(t, "two", h.selectedTitle())
	h.keys("G")
	assert.Equal(t, "three", h.selectedTitle())
	h.keys("up", "up", "up")
	assert.Equal(t, "one", h.selectedTitle())

	for _, want := range []tab{tabNext, tabLater, tabHistory, tabNow} {
		h.keys("tab")
		assert.Equal(t, want, h.m.tab)
	}
	h.keys("3")
	assert.Equal(t, tabLater, h.m.tab)
	h.keys("shift+tab")
	assert.Equal(t, tabNext, h.m.tab)
}

func TestSingleKeysDoNotFireWhileTyping(t *testing.T) {
	h := newHarness(t)
	h.add("jump to 4 quickly, m x")
	assert.Equal(t, tabNow, h.m.tab)
	assert.Equal(t, []string{"jump to 4 quickly, m x"}, h.viewTitles(tabNow))
}

func TestMoveKeys(t *testing.T) {
	tests := []struct {
		keys []string
		want tab
	}{
		{[]string{"m", "n"}, tabNow},
		{[]string{"m", "x"}, tabNext},
		{[]string{"m", "l"}, tabLater},
		{[]string{"m", "j"}, tabHistory},
		{[]string{"m", "esc"}, tabNow},
		{[]string{"m", "q"}, tabNow},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.keys, " "), func(t *testing.T) {
			h := newHarness(t)
			h.add("task")
			h.keys("m")
			assert.Contains(t, h.screen(), "Move to:")
			h.keys(tt.keys[1:]...)
			assert.False(t, h.m.pendingMove)
			assert.Contains(t, h.viewTitles(tt.want), "task")
			assert.Contains(t, h.viewTitles(tabHistory), "task")
		})
	}
}

func TestCompleteParentAsksFirst(t *testing.T) {
	h := newHarness(t)
	h.add("parent\n\n- [ ] kid")
	h.keys("g", "x")
	require.NotNil(t, h.m.confirm)
	assert.Contains(t, h.screen(), "Complete “parent” and its 1 open child?")
	h.keys("n")
	assert.Equal(t, []string{"parent", "kid"}, h.viewTitles(tabNow))

	h.keys("x", "y")
	assert.Empty(t, h.viewTitles(tabNow))
	h.keys("4")
	assert.Contains(t, h.screen(), "✓ parent")

	h.keys("u")
	assert.Equal(t, []string{"parent", "kid"}, h.viewTitles(tabNow))
}

func TestReorderAndIndent(t *testing.T) {
	h := newHarness(t)
	for _, s := range []string{"a", "b", "c"} {
		h.add(s)
	}
	h.keys("G", "K")
	assert.Equal(t, []string{"c", "a", "b"}, h.viewTitles(tabNow))
	h.keys("shift+down")
	assert.Equal(t, []string{"a", "c", "b"}, h.viewTitles(tabNow))
	h.keys("alt+k")
	assert.Equal(t, []string{"c", "a", "b"}, h.viewTitles(tabNow))
	h.keys("J")
	assert.Equal(t, []string{"a", "b", "c"}, h.viewTitles(tabNow))
	assert.Equal(t, "c", h.selectedTitle())

	h.keys(">")
	assert.Contains(t, h.screen(), "• c")
	h.keys("<")
	assert.NotContains(t, h.screen(), "• c")
}

func TestJumpParentChildAndBack(t *testing.T) {
	h := newHarness(t)
	h.add("parent note\n\n- [ ] child todo", "alt+j")
	// The child landed in Now; the parent is a journal note in History.
	assert.Equal(t, []string{"child todo"}, h.viewTitles(tabNow))
	assert.Contains(t, h.screen(), "parent note › child todo")

	h.keys("p")
	assert.Equal(t, tabHistory, h.m.tab)
	assert.Equal(t, "parent note", h.selectedTitle())

	h.keys("ctrl+o")
	assert.Equal(t, tabNow, h.m.tab)
	assert.Equal(t, "child todo", h.selectedTitle())

	h.keys("4")
	for h.selectedTitle() != "parent note" {
		h.keys("j")
	}
	h.keys("c")
	assert.Equal(t, tabNow, h.m.tab)
	assert.Equal(t, "child todo", h.selectedTitle())

	h.keys("c")
	assert.Contains(t, h.screen(), "has no children")
	h.keys("ctrl+o", "ctrl+o")
	assert.Contains(t, h.screen(), "No earlier position")
}

func TestJumpFromDetailReturnsToDetail(t *testing.T) {
	h := newHarness(t)
	h.add("parent\n\n- [ ] child")
	h.keys("G", "enter")
	require.Equal(t, modeDetail, h.m.mode)
	h.keys("p")
	assert.Equal(t, modeList, h.m.mode)
	assert.Equal(t, "parent", h.selectedTitle())
	h.keys("ctrl+o")
	assert.Equal(t, modeDetail, h.m.mode)
	assert.Equal(t, "child", h.svc.Board().Get(h.m.detail.id).Title)
}

func TestHistorySortPersists(t *testing.T) {
	h := newHarness(t)
	h.add("first")
	h.add("second")
	h.keys("4")
	assert.Equal(t, []string{"second", "first"}, h.viewTitles(tabHistory))
	h.keys("r")
	assert.Equal(t, []string{"first", "second"}, h.viewTitles(tabHistory))
	h.keys("s")
	saved, err := config.LoadSettings(h.pathsTo)
	require.NoError(t, err)
	assert.Equal(t, config.History{Sort: "completed", Descending: false}, saved.History)
	assert.Contains(t, h.screen(), "Not completed")
}

func TestSettingsPane(t *testing.T) {
	h := newHarness(t)
	h.keys(",")
	require.Equal(t, modeSettings, h.m.mode)
	s := h.screen()
	assert.Contains(t, s, "Link shortening")
	assert.Contains(t, s, "Show pasted URLs as page titles")

	h.keys("enter") // toggle link shortening
	saved, err := config.LoadSettings(h.pathsTo)
	require.NoError(t, err)
	assert.False(t, saved.ShortenLinks)

	h.keys("ctrl+n", "right", "right") // undo depth 12
	assert.Equal(t, 12, h.svc.UndoDepth)

	// Rebind quit to Q.
	for h.m.settings.items[h.m.settings.cursor].action != config.Quit {
		h.keys("down")
	}
	h.keys("enter", "Q")
	keys, err := config.LoadKeys(h.pathsTo)
	require.NoError(t, err)
	assert.Equal(t, []string{"Q"}, keys[config.Quit])
	h.keys("backspace")
	assert.Equal(t, []string{"q", "ctrl+c"}, h.m.Keys[config.Quit])

	h.keys("esc")
	assert.Equal(t, modeList, h.m.mode)
}

func TestCopyAndDelete(t *testing.T) {
	h := newHarness(t)
	h.add("note\n\nbody\n- [ ] boxed")
	h.keys("g", "A")
	h.typeText("extra child")
	h.keys("enter")
	h.keys("g", "y")
	assert.Equal(t, "note\n\nbody\n- [ ] boxed\n\n- [ ] extra child", h.copied)

	h.keys("G", "D")
	assert.Contains(t, h.screen(), "Delete “extra child”?")
	h.keys("y")
	assert.Equal(t, []string{"note", "boxed"}, h.viewTitles(tabNow))
}

func TestMouse(t *testing.T) {
	h := newHarness(t)
	h.add("parent note\n\n- [ ] kid", "alt+j")
	h.add("other")
	// Row layout in Now: line 0 "parent note › kid", line 1 separator, line 2 "other".
	h.send(tea.MouseClickMsg{X: 30, Y: contentTop + 2, Button: tea.MouseLeft})
	assert.Equal(t, "other", h.selectedTitle())

	h.send(tea.MouseClickMsg{X: 6, Y: contentTop, Button: tea.MouseLeft})
	assert.Equal(t, tabHistory, h.m.tab, "clicking the dimmed parent jumps to it")
	assert.Equal(t, "parent note", h.selectedTitle())

	x := h.m.tabAt(0)
	assert.Equal(t, tab(-1), x)
	for col := range 60 {
		if h.m.tabAt(col) == tabLater {
			h.send(tea.MouseClickMsg{X: col, Y: 0, Button: tea.MouseLeft})
			break
		}
	}
	assert.Equal(t, tabLater, h.m.tab)
}

func TestLinksShortened(t *testing.T) {
	h := newHarness(t)
	require.NoError(t, h.svc.SetLinkTitle("https://example.test/a", "Example Page"))
	h.add("read https://example.test/a and [docs](https://docs.test)")
	s := h.screen()
	assert.Contains(t, s, "read Example Page and docs")
	h.m.Settings.ShortenLinks = false
	assert.Contains(t, h.screen(), "read https://example.test/a and [docs](https://docs.test)")
}

func TestPasteOpensDialog(t *testing.T) {
	h := newHarness(t)
	h.send(tea.PasteMsg{Content: "https://example.test"})
	require.NotNil(t, h.m.editor)
	assert.Equal(t, "https://example.test", h.m.editor.ta.Value())
}

func TestSmallTerminalDoesNotPanic(t *testing.T) {
	h := newHarness(t)
	h.add("a fairly long title that will not fit in a tiny terminal window")
	for _, size := range [][2]int{{10, 5}, {1, 1}, {0, 0}, {200, 60}} {
		h.send(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		for _, k := range []string{"1", "4", ",", "esc", "?", "esc", "enter", "esc", "a"} {
			h.keys(k)
			_ = h.screen()
		}
		h.keys("esc")
	}
}

func TestMain(m *testing.M) {
	// Rendering tests should not depend on the developer's terminal.
	os.Setenv("TERM", "xterm-256color")
	os.Exit(m.Run())
}

// runCmd executes a command that must not block, expanding batches.
func runCmd(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range batch {
			out = append(out, runCmd(c)...)
		}
		return out
	}
	return []tea.Msg{msg}
}

func TestLinkTitlesFetchedInBackground(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<title>Fetched Title</title>"))
	}))
	defer srv.Close()
	h := newHarness(t)
	h.add("see " + srv.URL + "/page")
	assert.Contains(t, h.screen(), "see "+srv.URL+"/page", "raw URL until the title arrives")

	h.m.Fetcher = links.NewFetcher()
	msgs := runCmd(h.m.fetchVisibleLinks())
	require.Len(t, msgs, 1)
	assert.Nil(t, h.m.fetchVisibleLinks(), "no duplicate fetch while in flight")
	h.send(msgs[0])
	assert.Contains(t, h.screen(), "see Fetched Title")
	title, ok := h.svc.Board().LinkTitle(srv.URL + "/page")
	assert.True(t, ok)
	assert.Equal(t, "Fetched Title", title)
}

func TestLinkFetchFailureKeepsRawURL(t *testing.T) {
	h := newHarness(t)
	h.add("see http://127.0.0.1:1/nope")
	h.send(linkTitleMsg{url: "http://127.0.0.1:1/nope", err: errors.New("refused")})
	assert.Contains(t, h.screen(), "see http://127.0.0.1:1/nope")
	h.m.Fetcher = links.NewFetcher()
	assert.Nil(t, h.m.fetchVisibleLinks(), "failed links are not retried this session")
}

func TestBackups(t *testing.T) {
	h := newHarness(t)
	log, _, err := store.Open(h.pathsTo.Data())
	require.NoError(t, err)
	defer log.Close()
	h.m.Log = log
	clock := time.Date(2026, 9, 29, 9, 30, 0, 0, time.Local)
	h.m.Now = func() time.Time { return clock }

	// No backup yet, so one is made at launch.
	msgs := runCmd(h.m.startupBackupCmd())
	require.Len(t, msgs, 1)
	done := msgs[0].(backupDoneMsg)
	require.NoError(t, done.err)
	assert.DirExists(t, done.path)
	assert.Equal(t, time.Date(2026, 9, 29, 11, 0, 0, 0, time.Local), h.m.nextBackup)

	// A recent backup exists, so the next launch skips it.
	msgs = runCmd(h.m.startupBackupCmd())
	assert.Equal(t, []tea.Msg{backupDoneMsg{}}, msgs)

	// Ticks before 11:00 do nothing; the first tick after runs the backup
	// and schedules tomorrow's.
	h.m.handleBackupTick(clock.Add(time.Hour))
	assert.Equal(t, time.Date(2026, 9, 29, 11, 0, 0, 0, time.Local), h.m.nextBackup)
	h.m.handleBackupTick(time.Date(2026, 9, 29, 11, 0, 30, 0, time.Local))
	assert.Equal(t, time.Date(2026, 9, 30, 11, 0, 0, 0, time.Local), h.m.nextBackup)
}

func TestInvalidConfigFilesAreNotOverwritten(t *testing.T) {
	h := newHarness(t)
	h.m.SettingsInvalid, h.m.KeysInvalid = true, true
	h.keys("4", "s")
	assert.Contains(t, h.screen(), "Not saved: fix the errors in")
	assert.NoFileExists(t, h.pathsTo.Settings())

	h.keys(",")
	for h.m.settings.items[h.m.settings.cursor].action != config.Quit {
		h.keys("down")
	}
	h.keys("enter", "Q")
	assert.NoFileExists(t, h.pathsTo.Keys())
	assert.Equal(t, []string{"Q"}, h.m.Keys[config.Quit], "still applies for this session")
}
