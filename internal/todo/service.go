package todo

import (
	"errors"
	"fmt"
	"slices"
	"time"
)

// Log persists events. Append must be durable before it returns.
type Log interface {
	Append(events []Event) error
}

// Errors returned by Service operations.
var (
	ErrEmpty         = errors.New("entry is empty")
	ErrNotFound      = errors.New("item not found")
	ErrNothingToUndo = errors.New("nothing to undo")
	ErrJournalDone   = errors.New("journal items can't be completed; promote it first")
	ErrNotInView     = errors.New("item is not in an active view")
	ErrCannotIndent  = errors.New("nothing above to indent under")
	ErrCannotOutdent = errors.New("already at the top level")
)

// NeedsConfirmError is returned by Complete when the item has open
// descendants and completion was not forced.
type NeedsConfirmError struct{ Open int }

func (e *NeedsConfirmError) Error() string {
	return fmt.Sprintf("item has %d open children", e.Open)
}

// Mutation is one item's change within a transaction. A nil Before is a
// create and a nil After is a delete.
type Mutation struct {
	Before, After *Item
}

// Record is an undoable transaction.
type Record struct {
	Label string
	Muts  []Mutation
}

// SyncSummary describes what a checkbox sync changed.
type SyncSummary struct {
	Created, Rewritten, Removed, Detached, Completed, Reopened int
}

// BulkThreshold is how many removals or rewrites in a single sync count as
// a bulk change worth flagging.
const BulkThreshold = 2

// Bulk reports whether the sync removed or rewrote several children at once.
func (s SyncSummary) Bulk() bool { return s.Removed+s.Rewritten >= BulkThreshold }

// Zero reports whether nothing changed.
func (s SyncSummary) Zero() bool { return s == SyncSummary{} }

// Result reports side effects of an operation for the UI.
type Result struct {
	Item      *Item
	Sync      SyncSummary
	Followers int // children that followed a move, or were completed with a parent
}

// Service applies operations to the board, persists them and keeps undo.
type Service struct {
	board     *Board
	log       Log
	undo      []Record
	UndoDepth int
	Now       func() time.Time
	NewID     func(time.Time) string
}

// NewService wraps a board replayed from log.
func NewService(b *Board, log Log, undoDepth int) *Service {
	return &Service{board: b, log: log, UndoDepth: undoDepth, Now: time.Now, NewID: NewID}
}

// Board returns the current state. Callers must not modify it.
func (s *Service) Board() *Board { return s.board }

// CanUndo reports whether there is anything to undo.
func (s *Service) CanUndo() bool { return len(s.undo) > 0 }

// tx stages changes on a copy of the board.
type tx struct {
	s       *Service
	w       *Board
	label   string
	touched []string
	now     time.Time
}

func (s *Service) begin(label string) *tx {
	return &tx{s: s, w: s.board.clone(), label: label, now: s.Now()}
}

// get returns a mutable copy of an item, or nil.
func (t *tx) get(id string) *Item { return t.w.Get(id).Clone() }

func (t *tx) touch(id string) {
	if !slices.Contains(t.touched, id) {
		t.touched = append(t.touched, id)
	}
}

func (t *tx) put(it *Item) {
	t.touch(it.ID)
	t.w.put(it)
}

func (t *tx) del(id string) {
	t.touch(id)
	t.w.del(id)
}

func (t *tx) nextOrder() float64 { return t.w.MaxOrder() + 1 }

// commit persists the staged changes and makes them current. When record is
// true the change is pushed onto the undo stack.
func (s *Service) commit(t *tx, record bool) error {
	var muts []Mutation
	for _, id := range t.touched {
		before, after := s.board.items[id], t.w.items[id]
		if before.Equal(after) {
			continue
		}
		muts = append(muts, Mutation{Before: before, After: after})
	}
	if len(muts) == 0 {
		return nil
	}
	txID := s.board.lastTx + 1
	events := make([]Event, 0, len(muts))
	for _, m := range muts {
		e := Event{Time: t.now, Tx: txID}
		if m.After == nil {
			e.Op, e.ID = OpDel, m.Before.ID
		} else {
			e.Op, e.Item = OpPut, m.After
		}
		events = append(events, e)
	}
	if s.log != nil {
		if err := s.log.Append(events); err != nil {
			return fmt.Errorf("saving: %w", err)
		}
	}
	t.w.lastTx = txID
	s.board = t.w
	if record && s.UndoDepth > 0 {
		s.undo = append(s.undo, Record{Label: t.label, Muts: muts})
		if over := len(s.undo) - s.UndoDepth; over > 0 {
			s.undo = slices.Delete(s.undo, 0, over)
		}
	}
	return nil
}

// Undo reverts the most recent recorded transaction.
func (s *Service) Undo() (string, error) {
	if len(s.undo) == 0 {
		return "", ErrNothingToUndo
	}
	rec := s.undo[len(s.undo)-1]
	t := s.begin("undo " + rec.Label)
	for i := len(rec.Muts) - 1; i >= 0; i-- {
		m := rec.Muts[i]
		if m.Before == nil {
			t.del(m.After.ID)
		} else {
			t.put(m.Before.Clone())
		}
	}
	if err := s.commit(t, false); err != nil {
		return "", err
	}
	s.undo = s.undo[:len(s.undo)-1]
	return rec.Label, nil
}

// SetLinkTitle caches a fetched link title. It is not undoable.
func (s *Service) SetLinkTitle(url, title string) error {
	e := Event{Time: s.Now(), Op: OpLink, URL: url, Title: title}
	if s.log != nil {
		if err := s.log.Append([]Event{e}); err != nil {
			return err
		}
	}
	s.board.links[url] = title
	return nil
}

// Add creates an entry from text. childState is used for checkbox children
// when state is Journal.
func (s *Service) Add(text string, state State, parent string, childState State) (Result, error) {
	title, body := SplitText(text)
	if title == "" {
		return Result{}, ErrEmpty
	}
	t := s.begin("add")
	it := &Item{
		ID:      s.NewID(t.now),
		Title:   title,
		Body:    body,
		State:   state,
		Parent:  parent,
		Order:   t.nextOrder(),
		Created: t.now,
	}
	if t.w.Get(parent) == nil {
		it.Parent = ""
	}
	t.put(it)
	sum := t.syncBody(it.ID, childState)
	if err := s.commit(t, true); err != nil {
		return Result{}, err
	}
	return Result{Item: s.board.Get(it.ID), Sync: sum}, nil
}

// Edit replaces an item's title and body and syncs its checkbox children.
func (s *Service) Edit(id, text string, childState State) (Result, error) {
	title, body := SplitText(text)
	if title == "" {
		return Result{}, ErrEmpty
	}
	t := s.begin("edit")
	it := t.get(id)
	if it == nil {
		return Result{}, ErrNotFound
	}
	titleChanged, bodyChanged := it.Title != title, it.Body != body
	it.Title, it.Body = title, body
	t.put(it)
	if titleChanged && it.Source != "" {
		t.updateSourceLine(it)
	}
	var sum SyncSummary
	if bodyChanged {
		sum = t.syncBody(id, childState)
	}
	if err := s.commit(t, true); err != nil {
		return Result{}, err
	}
	return Result{Item: s.board.Get(id), Sync: sum}, nil
}

// Move changes an item's state. Moving between Now, Next and Later brings
// open children in the same state along; demoting to Journal does not, and
// clears the completed date. Moving a done item to an active state reopens it.
func (s *Service) Move(id string, target State) (Result, error) {
	t := s.begin("move to " + target.Label())
	it := t.get(id)
	if it == nil {
		return Result{}, ErrNotFound
	}
	res := Result{}
	if target == Journal {
		if it.State == Journal {
			return res, nil
		}
		it.State = Journal
		t.setDone(it, false)
	} else {
		wasOpen, old := it.Open(), it.State
		if wasOpen && old == target {
			return res, nil
		}
		it.State = target
		it.Order = t.nextOrder()
		t.setDone(it, false)
		if wasOpen {
			var follow func(pid string)
			follow = func(pid string) {
				for _, c := range t.w.Children(pid) {
					if c.State == old && !c.Done() {
						cc := c.Clone()
						cc.State = target
						t.put(cc)
						res.Followers++
						follow(c.ID)
					}
				}
			}
			follow(id)
		}
	}
	if err := s.commit(t, true); err != nil {
		return Result{}, err
	}
	res.Item = s.board.Get(id)
	return res, nil
}

// Complete marks an item done. If it has open descendants and force is
// false, a *NeedsConfirmError is returned and nothing changes; with force
// they are completed too.
func (s *Service) Complete(id string, force bool) (Result, error) {
	t := s.begin("complete")
	it := t.get(id)
	if it == nil {
		return Result{}, ErrNotFound
	}
	if it.State == Journal {
		return Result{}, ErrJournalDone
	}
	if it.Done() {
		return Result{Item: it}, nil
	}
	var open []*Item
	for _, d := range t.w.Descendants(id) {
		if d.Open() {
			open = append(open, d)
		}
	}
	if len(open) > 0 && !force {
		return Result{}, &NeedsConfirmError{Open: len(open)}
	}
	t.setDone(it, true)
	for _, d := range open {
		t.setDone(t.get(d.ID), true)
	}
	if err := s.commit(t, true); err != nil {
		return Result{}, err
	}
	return Result{Item: s.board.Get(id), Followers: len(open)}, nil
}

// Reopen clears an item's completed date.
func (s *Service) Reopen(id string) (Result, error) {
	t := s.begin("reopen")
	it := t.get(id)
	if it == nil {
		return Result{}, ErrNotFound
	}
	t.setDone(it, false)
	if err := s.commit(t, true); err != nil {
		return Result{}, err
	}
	return Result{Item: s.board.Get(id)}, nil
}

// Delete removes an item. Its children move up to its parent.
func (s *Service) Delete(id string) error {
	t := s.begin("delete")
	it := t.get(id)
	if it == nil {
		return ErrNotFound
	}
	for _, c := range t.w.Children(id) {
		cc := c.Clone()
		cc.Parent = it.Parent
		t.put(cc)
	}
	for _, x := range t.w.items {
		if x.Source == id {
			xc := x.Clone()
			xc.Source, xc.Line = "", 0
			t.put(xc)
		}
	}
	if it.Source != "" {
		t.removeSourceLine(it)
	}
	t.del(id)
	return s.commit(t, true)
}

// Direction for Reorder.
type Direction int

const (
	Up Direction = iota
	Down
	Top
	Bottom
)

// Reorder moves an open item among its siblings in its view.
func (s *Service) Reorder(id string, dir Direction) error {
	t := s.begin("reorder")
	it := t.get(id)
	if it == nil {
		return ErrNotFound
	}
	if !it.Open() {
		return ErrNotInView
	}
	sibs := t.w.viewSiblings(it)
	i := slices.IndexFunc(sibs, func(x *Item) bool { return x.ID == id })
	switch {
	case dir == Up && i > 0:
		o := sibs[i-1].Clone()
		it.Order, o.Order = o.Order, it.Order
		t.put(o)
	case dir == Down && i < len(sibs)-1:
		o := sibs[i+1].Clone()
		it.Order, o.Order = o.Order, it.Order
		t.put(o)
	case dir == Top && i > 0:
		it.Order = sibs[0].Order - 1
	case dir == Bottom && i < len(sibs)-1:
		it.Order = sibs[len(sibs)-1].Order + 1
	default:
		return nil
	}
	t.put(it)
	return s.commit(t, true)
}

// Indent makes an open item the last child of the sibling above it.
func (s *Service) Indent(id string) error {
	t := s.begin("indent")
	it := t.get(id)
	if it == nil {
		return ErrNotFound
	}
	if !it.Open() {
		return ErrNotInView
	}
	sibs := t.w.viewSiblings(it)
	i := slices.IndexFunc(sibs, func(x *Item) bool { return x.ID == id })
	if i <= 0 {
		return ErrCannotIndent
	}
	it.Parent = sibs[i-1].ID
	it.Order = t.nextOrder()
	t.put(it)
	return s.commit(t, true)
}

// Outdent moves an item up one level, placing it just after its old parent.
func (s *Service) Outdent(id string) error {
	t := s.begin("outdent")
	it := t.get(id)
	if it == nil {
		return ErrNotFound
	}
	if it.Parent == "" {
		return ErrCannotOutdent
	}
	p := t.w.Get(it.Parent)
	if p == nil {
		it.Parent = ""
		t.put(it)
		return s.commit(t, true)
	}
	it.Parent = p.Parent
	it.Order = p.Order + 1
	sibs := t.w.Children(p.Parent)
	if i := slices.IndexFunc(sibs, func(x *Item) bool { return x.ID == p.ID }); i >= 0 && i+1 < len(sibs) {
		it.Order = (p.Order + sibs[i+1].Order) / 2
	}
	t.put(it)
	return s.commit(t, true)
}

// viewSiblings returns the items displayed at the same level as it in its
// active view, in order.
func (b *Board) viewSiblings(it *Item) []*Item {
	in := func(x *Item) bool { return x.State == it.State && !x.Done() }
	var sibs []*Item
	if p := b.Get(it.Parent); p != nil && in(p) {
		for _, c := range b.Children(p.ID) {
			if in(c) {
				sibs = append(sibs, c)
			}
		}
		return sibs
	}
	for _, x := range b.items {
		if !in(x) {
			continue
		}
		if p := b.Get(x.Parent); p == nil || !in(p) {
			sibs = append(sibs, x)
		}
	}
	sortByOrder(sibs)
	return sibs
}

// setDone sets or clears the completed date on it (a mutable copy), stages
// it and mirrors the change onto its checkbox line.
func (t *tx) setDone(it *Item, done bool) {
	switch {
	case done && !it.Done():
		now := t.now
		it.Completed = &now
	case !done:
		it.Completed = nil
	}
	t.put(it)
	if it.Source != "" {
		t.updateSourceLine(it)
	}
}

// sourceKids returns the items whose checkbox lines live in id's body.
func (b *Board) sourceKids(id string) []*Item {
	var out []*Item
	for _, x := range b.items {
		if x.Source == id {
			out = append(out, x)
		}
	}
	sortByOrder(out)
	return out
}

// updateSourceLine rewrites the checkbox line backing child so it matches
// the child's title and done state.
func (t *tx) updateSourceLine(child *Item) {
	src := t.get(child.Source)
	if src == nil {
		return
	}
	for _, l := range ParseCheckboxes(src.Body) {
		if l.Ordinal != child.Line {
			continue
		}
		if l.Text != child.Title || l.Checked != child.Done() {
			src.Body = setCheckboxLine(src.Body, l.LineNo, child.Title, child.Done())
			t.put(src)
		}
		return
	}
}

// removeSourceLine deletes child's checkbox line from its source body and
// shifts the ordinals of the lines after it.
func (t *tx) removeSourceLine(child *Item) {
	src := t.get(child.Source)
	if src == nil {
		return
	}
	for _, l := range ParseCheckboxes(src.Body) {
		if l.Ordinal != child.Line {
			continue
		}
		src.Body = removeLine(src.Body, l.LineNo)
		t.put(src)
		for _, k := range t.w.sourceKids(src.ID) {
			if k.ID != child.ID && k.Line > child.Line {
				kc := k.Clone()
				kc.Line--
				t.put(kc)
			}
		}
		return
	}
}

// syncBody reconciles the checkbox lines in id's body with its checkbox
// children. The body is the source of truth for wording and, since the body
// was just edited, for any state the user changed on a line.
func (t *tx) syncBody(id string, childState State) SyncSummary {
	var sum SyncSummary
	note := t.w.Get(id)
	if note == nil {
		return sum
	}
	lines := ParseCheckboxes(note.Body)
	kids := t.w.sourceKids(id)
	// New children join the note's state; for a journal note they join
	// their open siblings, falling back to the state the caller chose.
	if note.State.Active() {
		childState = note.State
	} else if i := slices.IndexFunc(kids, (*Item).Open); i >= 0 {
		childState = kids[i].State
	}
	if !childState.Active() {
		childState = Now
	}
	match := MatchCheckboxes(lines, kids)

	orderOf := make([]float64, len(lines))
	has := make([]bool, len(lines))
	for i, j := range match {
		if j >= 0 {
			orderOf[i], has[i] = kids[j].Order, true
		}
	}
	for i, l := range lines {
		if j := match[i]; j >= 0 {
			c := kids[j].Clone()
			if c.Title != l.Text {
				c.Title = l.Text
				sum.Rewritten++
			}
			c.Line = l.Ordinal
			if l.Checked != c.Done() && c.State.Active() {
				if l.Checked {
					now := t.now
					c.Completed = &now
					sum.Completed++
				} else {
					c.Completed = nil
					sum.Reopened++
				}
			}
			t.put(c)
			continue
		}
		c := &Item{
			ID:      t.s.NewID(t.now),
			Title:   l.Text,
			State:   childState,
			Parent:  id,
			Source:  id,
			Line:    l.Ordinal,
			Created: t.now,
			Order:   t.orderBetween(orderOf, has, i),
		}
		if l.Checked {
			now := t.now
			c.Completed = &now
		}
		orderOf[i], has[i] = c.Order, true
		t.put(c)
		sum.Created++
	}
	used := map[int]bool{}
	for _, j := range match {
		used[j] = true
	}
	for j, k := range kids {
		if used[j] {
			continue
		}
		if k.Done() {
			kc := k.Clone()
			kc.Source, kc.Line = "", 0
			t.put(kc)
			sum.Detached++
			continue
		}
		for _, gc := range t.w.Children(k.ID) {
			gcc := gc.Clone()
			gcc.Parent = k.Parent
			t.put(gcc)
		}
		t.del(k.ID)
		sum.Removed++
	}
	return sum
}

// orderBetween picks an order for a new checkbox child at line i so that it
// sits between the children of its neighbouring lines.
func (t *tx) orderBetween(orderOf []float64, has []bool, i int) float64 {
	prev, next := -1, -1
	for k := i - 1; k >= 0; k-- {
		if has[k] {
			prev = k
			break
		}
	}
	for k := i + 1; k < len(has); k++ {
		if has[k] {
			next = k
			break
		}
	}
	switch {
	case prev >= 0 && next >= 0 && orderOf[prev] < orderOf[next]:
		return (orderOf[prev] + orderOf[next]) / 2
	case prev < 0 && next >= 0:
		return orderOf[next] - 1
	}
	return t.nextOrder()
}
