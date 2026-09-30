package todo

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// Event is one line of the append-only log.
type Event struct {
	Time time.Time `json:"t"`
	Tx   uint64    `json:"tx,omitempty"`
	Op   Op        `json:"op"`
	// By is the agent that made the change; empty means the TUI user.
	By   string `json:"by,omitempty"`
	Item *Item  `json:"item,omitempty"`
	ID   string `json:"id,omitempty"`
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
	actor    map[string]string // who last changed each item
	lastTx   uint64
}

// NewBoard returns an empty board.
func NewBoard() *Board {
	return &Board{
		items:    map[string]*Item{},
		children: map[string][]string{},
		links:    map[string]string{},
		actor:    map[string]string{},
	}
}

// Replay applies a logged event.
func (b *Board) Replay(e Event) {
	b.lastTx = max(b.lastTx, e.Tx)
	switch e.Op {
	case OpPut:
		if e.Item != nil && e.Item.ID != "" {
			b.put(e.Item.Clone())
			b.actor[e.Item.ID] = e.By
		}
	case OpDel:
		b.del(e.ID)
		b.actor[e.ID] = e.By
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

// LastActor returns who last changed an item: an agent name, or "" for
// the TUI user.
func (b *Board) LastActor(id string) string { return b.actor[id] }

// ErrAmbiguous is returned when an ID prefix matches several items.
var ErrAmbiguous = errors.New("ambiguous ID prefix")

// MinPrefix is the shortest ID prefix accepted.
const MinPrefix = 6

// Resolve finds an item by full ID or unique prefix of at least MinPrefix
// characters.
func (b *Board) Resolve(ref string) (*Item, error) {
	if it := b.items[ref]; it != nil {
		return it, nil
	}
	if len(ref) < MinPrefix {
		return nil, fmt.Errorf("%w: %q", ErrNotFound, ref)
	}
	var found *Item
	for id, it := range b.items {
		if strings.HasPrefix(id, ref) {
			if found != nil {
				return nil, fmt.Errorf("%w: %q", ErrAmbiguous, ref)
			}
			found = it
		}
	}
	if found == nil {
		return nil, fmt.Errorf("%w: %q", ErrNotFound, ref)
	}
	return found, nil
}

// ShortIDLen is the shortest abbreviation ShortIDs produces.
const ShortIDLen = 8

// ShortIDs abbreviates every item ID to its shortest unique prefix of at
// least ShortIDLen characters, like git's abbreviated hashes.
func (b *Board) ShortIDs() map[string]string {
	ids := make([]string, 0, len(b.items))
	for id := range b.items {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	common := func(a, c string) int {
		n := 0
		for n < len(a) && n < len(c) && a[n] == c[n] {
			n++
		}
		return n
	}
	out := make(map[string]string, len(ids))
	for i, id := range ids {
		n := ShortIDLen
		if i > 0 {
			n = max(n, common(id, ids[i-1])+1)
		}
		if i+1 < len(ids) {
			n = max(n, common(id, ids[i+1])+1)
		}
		out[id] = id[:min(n, len(id))]
	}
	return out
}

// FindClaim finds a claim, active or ended, by full ID or unique prefix.
func (b *Board) FindClaim(ref string) (*Item, Claim, error) {
	var (
		item  *Item
		claim Claim
		n     int
	)
	for _, it := range b.items {
		for _, c := range it.Claims {
			if c.ID == ref {
				return it, c, nil
			}
			if len(ref) >= MinPrefix && strings.HasPrefix(c.ID, ref) {
				item, claim = it, c
				n++
			}
		}
	}
	switch n {
	case 0:
		return nil, Claim{}, fmt.Errorf("%w: claim %q", ErrNotFound, ref)
	case 1:
		return item, claim, nil
	}
	return nil, Claim{}, fmt.Errorf("%w: claim %q", ErrAmbiguous, ref)
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
		actor:    b.actor,
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
	// Day is the local calendar day of a RowDay header; zero means there is
	// no outcome date when grouping by completion or rejection.
	Day time.Time
}

// ViewRows returns the rows of an active view: open items in state s, as a
// tree. Items whose parent is not in the view are shown at the top level
// with their parent's title as context.
func (b *Board) ViewRows(s State) []Row {
	in := func(it *Item) bool { return it.State == s && !it.Done() && !it.Rejected() }
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
