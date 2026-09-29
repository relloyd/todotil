package todo

import (
	"cmp"
	"slices"
	"time"
)

// Event is one line of the append-only log.
type Event struct {
	Time time.Time `json:"t"`
	Tx   uint64    `json:"tx,omitempty"`
	Op   Op        `json:"op"`
	Item *Item     `json:"item,omitempty"`
	ID   string    `json:"id,omitempty"`
	// URL and Title carry a cached link title for OpLink.
	URL   string `json:"url,omitempty"`
	Title string `json:"title,omitempty"`
	// Version is set on OpMeta, the first line of every log file.
	Version int `json:"v,omitempty"`
}

// Op is the kind of change an Event records.
type Op string

const (
	OpMeta Op = "meta" // file header
	OpPut  Op = "put"  // create or replace an item
	OpDel  Op = "del"  // delete an item
	OpLink Op = "link" // cache a link title
)

// Board is the in-memory state rebuilt by replaying the event log.
type Board struct {
	items    map[string]*Item
	children map[string][]string
	links    map[string]string
	lastTx   uint64
}

// NewBoard returns an empty board.
func NewBoard() *Board {
	return &Board{
		items:    map[string]*Item{},
		children: map[string][]string{},
		links:    map[string]string{},
	}
}

// Replay applies a logged event.
func (b *Board) Replay(e Event) {
	b.lastTx = max(b.lastTx, e.Tx)
	switch e.Op {
	case OpPut:
		if e.Item != nil && e.Item.ID != "" {
			b.put(e.Item.Clone())
		}
	case OpDel:
		b.del(e.ID)
	case OpLink:
		b.links[e.URL] = e.Title
	}
}

// Get returns the item with id, or nil. Callers must not modify it.
func (b *Board) Get(id string) *Item {
	if id == "" {
		return nil
	}
	return b.items[id]
}

// Len returns the number of items.
func (b *Board) Len() int { return len(b.items) }

// LinkTitle returns a cached link title.
func (b *Board) LinkTitle(url string) (string, bool) {
	t, ok := b.links[url]
	return t, ok
}

// All returns every item in creation order.
func (b *Board) All() []*Item {
	out := make([]*Item, 0, len(b.items))
	for _, it := range b.items {
		out = append(out, it)
	}
	slices.SortFunc(out, func(a, c *Item) int {
		return cmp.Or(a.Created.Compare(c.Created), cmp.Compare(a.ID, c.ID))
	})
	return out
}

// Children returns the children of id in sibling order.
func (b *Board) Children(id string) []*Item {
	ids := b.children[id]
	out := make([]*Item, 0, len(ids))
	for _, cid := range ids {
		if it := b.items[cid]; it != nil {
			out = append(out, it)
		}
	}
	sortByOrder(out)
	return out
}

// Descendants returns all items below id, depth first in sibling order.
func (b *Board) Descendants(id string) []*Item {
	var out []*Item
	seen := map[string]bool{id: true}
	var walk func(string)
	walk = func(pid string) {
		for _, c := range b.Children(pid) {
			if seen[c.ID] {
				continue
			}
			seen[c.ID] = true
			out = append(out, c)
			walk(c.ID)
		}
	}
	walk(id)
	return out
}

func (b *Board) clone() *Board {
	c := &Board{
		items:    make(map[string]*Item, len(b.items)),
		children: make(map[string][]string, len(b.children)),
		links:    b.links,
		lastTx:   b.lastTx,
	}
	for k, v := range b.items {
		c.items[k] = v
	}
	for k, v := range b.children {
		c.children[k] = slices.Clone(v)
	}
	return c
}

// MaxOrder returns the largest order value in use.
func (b *Board) MaxOrder() float64 {
	var m float64
	for _, it := range b.items {
		m = max(m, it.Order)
	}
	return m
}

func (b *Board) put(it *Item) {
	if old := b.items[it.ID]; old != nil && old.Parent != it.Parent {
		b.unlinkChild(old.Parent, it.ID)
	}
	if old := b.items[it.ID]; old == nil || old.Parent != it.Parent {
		b.children[it.Parent] = append(b.children[it.Parent], it.ID)
	}
	b.items[it.ID] = it
}

func (b *Board) del(id string) {
	old := b.items[id]
	if old == nil {
		return
	}
	b.unlinkChild(old.Parent, id)
	delete(b.items, id)
}

func (b *Board) unlinkChild(parent, id string) {
	ids := b.children[parent]
	if i := slices.Index(ids, id); i >= 0 {
		b.children[parent] = slices.Delete(ids, i, i+1)
	}
}

func sortByOrder(items []*Item) {
	slices.SortStableFunc(items, func(a, c *Item) int {
		return cmp.Or(cmp.Compare(a.Order, c.Order), a.Created.Compare(c.Created), cmp.Compare(a.ID, c.ID))
	})
}

// RowKind distinguishes list rows.
type RowKind int

const (
	RowItem RowKind = iota
	RowDay
)

// Row is one line of a rendered list.
type Row struct {
	Kind  RowKind
	Item  *Item
	Depth int
	// Context is the parent's title when the item's parent is not shown
	// directly above it in the same list.
	Context string
	// Day is the local calendar day of a RowDay header; zero means "not
	// completed" when grouping by completed date.
	Day time.Time
}

// ViewRows returns the rows of an active view: open items in state s, as a
// tree. Items whose parent is not in the view are shown at the top level
// with their parent's title as context.
func (b *Board) ViewRows(s State) []Row {
	in := func(it *Item) bool { return it.State == s && !it.Done() }
	var tops []*Item
	for _, it := range b.items {
		if !in(it) {
			continue
		}
		if p := b.items[it.Parent]; p == nil || !in(p) {
			tops = append(tops, it)
		}
	}
	sortByOrder(tops)
	var rows []Row
	seen := map[string]bool{}
	var walk func(it *Item, depth int)
	walk = func(it *Item, depth int) {
		if seen[it.ID] {
			return
		}
		seen[it.ID] = true
		r := Row{Kind: RowItem, Item: it, Depth: depth}
		if depth == 0 {
			if p := b.items[it.Parent]; p != nil {
				r.Context = p.Title
			}
		}
		rows = append(rows, r)
		for _, c := range b.Children(it.ID) {
			if in(c) {
				walk(c, depth+1)
			}
		}
	}
	for _, it := range tops {
		walk(it, 0)
	}
	return rows
}

// SortKey selects the date History groups and orders by.
type SortKey string

const (
	SortCreated   SortKey = "created"
	SortCompleted SortKey = "completed"
)

// HistoryRows returns every item grouped by local day of the sort key. A child
// is nested under its parent when both fall in the same day group; otherwise
// it is shown at the top level of its own group with its parent as context.
func (b *Board) HistoryRows(key SortKey, desc bool) []Row {
	keyOf := func(it *Item) (time.Time, bool) {
		if key == SortCompleted {
			if it.Completed == nil {
				return time.Time{}, false
			}
			return *it.Completed, true
		}
		return it.Created, true
	}
	type group struct {
		day   time.Time
		items []*Item
		set   map[string]bool
	}
	groups := map[time.Time]*group{}
	for _, it := range b.items {
		t, ok := keyOf(it)
		var day time.Time
		if ok {
			lt := t.Local()
			day = time.Date(lt.Year(), lt.Month(), lt.Day(), 0, 0, 0, 0, time.Local)
		}
		g := groups[day]
		if g == nil {
			g = &group{day: day, set: map[string]bool{}}
			groups[day] = g
		}
		g.items = append(g.items, it)
		g.set[it.ID] = true
	}
	ordered := make([]*group, 0, len(groups))
	for _, g := range groups {
		ordered = append(ordered, g)
	}
	// The "not completed" group (zero day) sorts as the most recent.
	dayCmp := func(a, c time.Time) int {
		switch {
		case a.IsZero() && c.IsZero():
			return 0
		case a.IsZero():
			return 1
		case c.IsZero():
			return -1
		}
		return a.Compare(c)
	}
	itemCmp := func(a, c *Item) int {
		ta, _ := keyOf(a)
		tc, _ := keyOf(c)
		return cmp.Or(ta.Compare(tc), a.Created.Compare(c.Created), cmp.Compare(a.ID, c.ID))
	}
	slices.SortFunc(ordered, func(a, c *group) int {
		if desc {
			return dayCmp(c.day, a.day)
		}
		return dayCmp(a.day, c.day)
	})
	var rows []Row
	seen := map[string]bool{}
	for _, g := range ordered {
		rows = append(rows, Row{Kind: RowDay, Day: g.day})
		var tops []*Item
		for _, it := range g.items {
			if !g.set[it.Parent] {
				tops = append(tops, it)
			}
		}
		slices.SortFunc(tops, func(a, c *Item) int {
			if desc {
				return itemCmp(c, a)
			}
			return itemCmp(a, c)
		})
		var walk func(it *Item, depth int)
		walk = func(it *Item, depth int) {
			if seen[it.ID] {
				return
			}
			seen[it.ID] = true
			r := Row{Kind: RowItem, Item: it, Depth: depth}
			if depth == 0 {
				if p := b.items[it.Parent]; p != nil {
					r.Context = p.Title
				}
			}
			rows = append(rows, r)
			for _, c := range b.Children(it.ID) {
				if g.set[c.ID] {
					walk(c, depth+1)
				}
			}
		}
		for _, it := range tops {
			walk(it, 0)
		}
	}
	return rows
}
