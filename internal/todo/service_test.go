package todo

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// memLog is an in-memory Log. Events in pending were "written by another
// process" and are handed to the next Update or Poll.
type memLog struct {
	events  []Event
	pending []Event
	fail    error
	// race, if set, runs once at the start of the next Update, simulating
	// another process writing between computing a change and committing it.
	race func()
}

func (l *memLog) Update(fn func([]Event) ([]Event, error)) error {
	if l.fail != nil {
		return l.fail
	}
	if r := l.race; r != nil {
		l.race = nil
		r()
	}
	foreign := l.takePending()
	out, err := fn(foreign)
	if err != nil {
		return err
	}
	l.events = append(l.events, out...)
	return nil
}

func (l *memLog) Poll() ([]Event, error) { return l.takePending(), nil }

func (l *memLog) takePending() []Event {
	p := l.pending
	l.pending = nil
	l.events = append(l.events, p...)
	return p
}

var t0 = time.Date(2026, 9, 29, 9, 0, 0, 0, time.Local)

func newTestService() (*Service, *memLog) {
	log := &memLog{}
	s := NewService(NewBoard(), log, 10)
	clock := t0
	s.Now = func() time.Time { clock = clock.Add(time.Minute); return clock }
	n := 0
	s.NewID = func(time.Time) string { n++; return fmt.Sprintf("id%02d", n) }
	return s, log
}

func add(t *testing.T, s *Service, text string, st State) *Item {
	t.Helper()
	r, err := s.Add(text, st, "", Now)
	require.NoError(t, err)
	return r.Item
}

func addChild(t *testing.T, s *Service, parent *Item, text string, st State) *Item {
	t.Helper()
	r, err := s.Add(text, st, parent.ID, Now)
	require.NoError(t, err)
	return r.Item
}

func titles(rows []Row) []string {
	var out []string
	for _, r := range rows {
		if r.Kind != RowItem {
			continue
		}
		s := strings.Repeat("  ", r.Depth) + r.Item.Title
		if r.Context != "" {
			s = r.Context + " › " + s
		}
		out = append(out, s)
	}
	return out
}

func TestSplitText(t *testing.T) {
	tests := []struct {
		in, title, body string
	}{
		{"buy milk", "buy milk", ""},
		{"  title  \n\nbody line\nmore", "title", "body line\nmore"},
		{"title\nbody without blank", "title", "body without blank"},
		{"\n\ntitle\n\n\n\nbody\n\n", "title", "body"},
		{"title\r\n\r\nbody", "title", "body"},
		{"", "", ""},
	}
	for _, tt := range tests {
		title, body := SplitText(tt.in)
		assert.Equal(t, tt.title, title, tt.in)
		assert.Equal(t, tt.body, body, tt.in)
	}
}

func TestAddAndViews(t *testing.T) {
	s, log := newTestService()
	a := add(t, s, "alpha", Now)
	add(t, s, "beta", Next)
	add(t, s, "gamma", Now)
	j := add(t, s, "standup\n\nnotes here", Journal)

	assert.Equal(t, []string{"alpha", "gamma"}, titles(s.Board().ViewRows(Now)))
	assert.Equal(t, []string{"beta"}, titles(s.Board().ViewRows(Next)))
	assert.Empty(t, s.Board().ViewRows(Later))
	assert.Equal(t, "notes here", s.Board().Get(j.ID).Body)
	assert.Len(t, log.events, 4)

	_, err := s.Add("   \n\n", Now, "", Now)
	assert.ErrorIs(t, err, ErrEmpty)

	c := addChild(t, s, a, "child", Now)
	assert.Equal(t, []string{"alpha", "  child", "gamma"}, titles(s.Board().ViewRows(Now)))
	assert.Equal(t, a.ID, c.Parent)
}

func TestCheckboxExtractionOnAdd(t *testing.T) {
	tests := []struct {
		name      string
		state     State
		childSt   State
		wantState State
	}{
		{"todo children take parent state", Next, Now, Next},
		{"journal children take given state", Journal, Later, Later},
		{"journal children default to now", Journal, Journal, Now},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _ := newTestService()
			r, err := s.Add("Meeting\n\n- [ ] one\n- [x] two\nnot a box\n* [ ] three", tt.state, "", tt.childSt)
			require.NoError(t, err)
			kids := s.Board().Children(r.Item.ID)
			require.Len(t, kids, 3)
			assert.Equal(t, []string{"one", "two", "three"}, []string{kids[0].Title, kids[1].Title, kids[2].Title})
			for i, k := range kids {
				assert.Equal(t, tt.wantState, k.State)
				assert.Equal(t, r.Item.ID, k.Source)
				assert.Equal(t, i, k.Line)
			}
			assert.False(t, kids[0].Done())
			assert.True(t, kids[1].Done())
			assert.Equal(t, 3, r.Sync.Created)
		})
	}
}

func TestMoveFollowers(t *testing.T) {
	s, _ := newTestService()
	p := add(t, s, "parent", Now)
	c1 := addChild(t, s, p, "same state", Now)
	gc := addChild(t, s, c1, "grandchild", Now)
	c2 := addChild(t, s, p, "other state", Later)
	c3 := addChild(t, s, p, "done", Now)
	_, err := s.Complete(c3.ID, false)
	require.NoError(t, err)

	r, err := s.Move(p.ID, Next)
	require.NoError(t, err)
	assert.Equal(t, 2, r.Followers)
	b := s.Board()
	assert.Equal(t, Next, b.Get(c1.ID).State)
	assert.Equal(t, Next, b.Get(gc.ID).State)
	assert.Equal(t, Later, b.Get(c2.ID).State)
	assert.Equal(t, Now, b.Get(c3.ID).State)
	assert.Equal(t, []string{"parent", "  same state", "    grandchild"}, titles(b.ViewRows(Next)))
	assert.Equal(t, []string{"parent › other state"}, titles(b.ViewRows(Later)))

	// Demoting to journal leaves children behind with their parent link.
	_, err = s.Move(p.ID, Journal)
	require.NoError(t, err)
	b = s.Board()
	assert.Equal(t, Journal, b.Get(p.ID).State)
	assert.Equal(t, Next, b.Get(c1.ID).State)
	assert.Equal(t, []string{"parent › same state", "  grandchild"}, titles(b.ViewRows(Next)))
}

func TestMoveChildAlone(t *testing.T) {
	s, _ := newTestService()
	p := add(t, s, "parent", Now)
	c := addChild(t, s, p, "child", Now)
	_, err := s.Move(c.ID, Later)
	require.NoError(t, err)
	assert.Equal(t, p.ID, s.Board().Get(c.ID).Parent)
	assert.Equal(t, []string{"parent › child"}, titles(s.Board().ViewRows(Later)))
	assert.Equal(t, []string{"parent"}, titles(s.Board().ViewRows(Now)))
}

func TestDemoteAndPromote(t *testing.T) {
	s, _ := newTestService()
	it := add(t, s, "task", Now)
	_, err := s.Complete(it.ID, false)
	require.NoError(t, err)
	require.True(t, s.Board().Get(it.ID).Done())

	_, err = s.Move(it.ID, Journal)
	require.NoError(t, err)
	got := s.Board().Get(it.ID)
	assert.Equal(t, Journal, got.State)
	assert.False(t, got.Done(), "demotion clears completed date")
	assert.Equal(t, it.Created, got.Created)

	_, err = s.Undo()
	require.NoError(t, err)
	assert.True(t, s.Board().Get(it.ID).Done(), "undo restores completed date")

	_, err = s.Move(it.ID, Journal)
	require.NoError(t, err)
	_, err = s.Complete(it.ID, false)
	assert.ErrorIs(t, err, ErrJournalDone)

	_, err = s.Move(it.ID, Next)
	require.NoError(t, err)
	assert.Equal(t, []string{"task"}, titles(s.Board().ViewRows(Next)))
	assert.Equal(t, it.ID, s.Board().ViewRows(Next)[0].Item.ID, "same record")
}

func TestCompleteConfirm(t *testing.T) {
	s, _ := newTestService()
	p := add(t, s, "parent", Now)
	c := addChild(t, s, p, "child", Later)
	j := addChild(t, s, p, "journal child", Journal)

	_, err := s.Complete(p.ID, false)
	var nc *NeedsConfirmError
	require.True(t, errors.As(err, &nc))
	assert.Equal(t, 1, nc.Open)
	assert.False(t, s.Board().Get(p.ID).Done(), "declined completion changes nothing")

	r, err := s.Complete(p.ID, true)
	require.NoError(t, err)
	assert.Equal(t, 1, r.Followers)
	assert.True(t, s.Board().Get(c.ID).Done())
	assert.False(t, s.Board().Get(j.ID).Done())
	assert.Empty(t, s.Board().ViewRows(Now))
	assert.Empty(t, s.Board().ViewRows(Later))

	_, err = s.Undo()
	require.NoError(t, err)
	assert.False(t, s.Board().Get(p.ID).Done())
	assert.False(t, s.Board().Get(c.ID).Done())
}

func TestReorder(t *testing.T) {
	tests := []struct {
		name string
		move string
		dir  Direction
		want []string
	}{
		{"up", "c", Up, []string{"a", "c", "  c1", "b", "  b1"}},
		{"up at top is noop", "a", Up, []string{"a", "b", "  b1", "c", "  c1"}},
		{"down", "a", Down, []string{"b", "  b1", "a", "c", "  c1"}},
		{"top", "c", Top, []string{"c", "  c1", "a", "b", "  b1"}},
		{"bottom", "a", Bottom, []string{"b", "  b1", "c", "  c1", "a"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _ := newTestService()
			ids := map[string]*Item{}
			for _, n := range []string{"a", "b", "c"} {
				ids[n] = add(t, s, n, Now)
			}
			addChild(t, s, ids["b"], "b1", Now)
			addChild(t, s, ids["c"], "c1", Now)
			require.NoError(t, s.Reorder(ids[tt.move].ID, tt.dir))
			assert.Equal(t, tt.want, titles(s.Board().ViewRows(Now)))
		})
	}
}

func TestReorderChildren(t *testing.T) {
	s, _ := newTestService()
	p := add(t, s, "p", Now)
	addChild(t, s, p, "one", Now)
	two := addChild(t, s, p, "two", Now)
	require.NoError(t, s.Reorder(two.ID, Top))
	assert.Equal(t, []string{"p", "  two", "  one"}, titles(s.Board().ViewRows(Now)))
	_, err := s.Undo()
	require.NoError(t, err)
	assert.Equal(t, []string{"p", "  one", "  two"}, titles(s.Board().ViewRows(Now)))
}

func TestIndentOutdent(t *testing.T) {
	s, _ := newTestService()
	a := add(t, s, "a", Now)
	b := add(t, s, "b", Now)
	c := add(t, s, "c", Now)

	assert.ErrorIs(t, s.Indent(a.ID), ErrCannotIndent)
	require.NoError(t, s.Indent(b.ID))
	assert.Equal(t, []string{"a", "  b", "c"}, titles(s.Board().ViewRows(Now)))
	require.NoError(t, s.Indent(c.ID))
	require.NoError(t, s.Indent(c.ID))
	assert.Equal(t, []string{"a", "  b", "    c"}, titles(s.Board().ViewRows(Now)))

	require.NoError(t, s.Outdent(c.ID))
	assert.Equal(t, []string{"a", "  b", "  c"}, titles(s.Board().ViewRows(Now)))
	require.NoError(t, s.Outdent(b.ID))
	assert.Equal(t, []string{"a", "  c", "b"}, titles(s.Board().ViewRows(Now)))
	assert.ErrorIs(t, s.Outdent(b.ID), ErrCannotOutdent)
}

func TestUndoDepth(t *testing.T) {
	s, _ := newTestService()
	s.UndoDepth = 3
	for i := range 5 {
		add(t, s, fmt.Sprint("item", i), Now)
	}
	for range 3 {
		_, err := s.Undo()
		require.NoError(t, err)
	}
	_, err := s.Undo()
	assert.ErrorIs(t, err, ErrNothingToUndo)
	assert.Equal(t, []string{"item0", "item1"}, titles(s.Board().ViewRows(Now)))
}

func TestFailedAppendLeavesStateUnchanged(t *testing.T) {
	s, log := newTestService()
	add(t, s, "a", Now)
	log.fail = errors.New("disk full")
	_, err := s.Add("b", Now, "", Now)
	assert.Error(t, err)
	assert.Equal(t, []string{"a"}, titles(s.Board().ViewRows(Now)))
	assert.False(t, s.CanUndo() && len(s.undo) > 1)
}

func TestReplayMatchesLiveState(t *testing.T) {
	s, log := newTestService()
	p := add(t, s, "note\n\n- [ ] x\n- [ ] y", Now)
	_, err := s.Move(p.ID, Later)
	require.NoError(t, err)
	kids := s.Board().Children(p.ID)
	_, err = s.Complete(kids[0].ID, false)
	require.NoError(t, err)
	require.NoError(t, s.Delete(kids[1].ID))
	require.NoError(t, s.SetLinkTitle("https://x.test", "X"))
	_, err = s.Undo()
	require.NoError(t, err)

	b := NewBoard()
	for _, e := range log.events {
		b.Replay(e)
	}
	require.Equal(t, s.Board().Len(), b.Len())
	for _, it := range s.Board().All() {
		assert.True(t, it.Equal(b.Get(it.ID)), it.Title)
	}
	title, ok := b.LinkTitle("https://x.test")
	assert.True(t, ok)
	assert.Equal(t, "X", title)
}

func TestDeleteReparentsChildren(t *testing.T) {
	s, _ := newTestService()
	a := add(t, s, "a", Now)
	b := addChild(t, s, a, "b", Now)
	addChild(t, s, b, "c", Now)
	require.NoError(t, s.Delete(b.ID))
	assert.Equal(t, []string{"a", "  c"}, titles(s.Board().ViewRows(Now)))
}

func TestHistoryRows(t *testing.T) {
	s, _ := newTestService()
	day := 0
	s.Now = func() time.Time { return t0.AddDate(0, 0, day) }
	p := add(t, s, "parent", Now)
	c := addChild(t, s, p, "same-day child", Now)
	day = 1
	late := addChild(t, s, p, "next-day child", Now)
	j := add(t, s, "journal", Journal)
	day = 2
	_, err := s.Complete(late.ID, false)
	require.NoError(t, err)
	day = 3
	_, err = s.Complete(p.ID, true)
	require.NoError(t, err)
	_ = c
	_ = j

	type line struct {
		day   string
		title string
	}
	flatten := func(rows []Row) []line {
		var out []line
		cur := ""
		for _, r := range rows {
			if r.Kind == RowDay {
				cur = "none"
				if !r.Day.IsZero() {
					cur = r.Day.Format("01-02")
				}
				continue
			}
			ti := strings.Repeat("  ", r.Depth) + r.Item.Title
			if r.Context != "" {
				ti = r.Context + " › " + ti
			}
			out = append(out, line{cur, ti})
		}
		return out
	}
	tests := []struct {
		name string
		key  SortKey
		desc bool
		want []line
	}{
		{"created desc", SortCreated, true, []line{
			{"09-30", "journal"}, {"09-30", "parent › next-day child"},
			{"09-29", "parent"}, {"09-29", "  same-day child"},
		}},
		{"created asc", SortCreated, false, []line{
			{"09-29", "parent"}, {"09-29", "  same-day child"},
			{"09-30", "parent › next-day child"}, {"09-30", "journal"},
		}},
		{"completed desc", SortCompleted, true, []line{
			{"none", "journal"},
			{"10-02", "parent"}, {"10-02", "  same-day child"},
			{"10-01", "parent › next-day child"},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, flatten(s.Board().HistoryRows(tt.key, tt.desc)))
		})
	}
}
