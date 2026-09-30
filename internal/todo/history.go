package todo

import (
	"cmp"
	"slices"
	"time"
)

// SortKey selects the date History groups and orders by. SortCompleted also
// uses the rejection timestamp for rejected items.
type SortKey string

const (
	SortCreated   SortKey = "created"
	SortCompleted SortKey = "completed"
)

// HistoryFilter selects which item outcomes are shown in History.
type HistoryFilter uint8

const (
	HistoryAll HistoryFilter = iota
	HistoryCompleted
	HistoryRejected
	HistoryJournal
)

// Label returns the user-facing name of a History filter.
func (f HistoryFilter) Label() string {
	switch f {
	case HistoryCompleted:
		return "Completed"
	case HistoryRejected:
		return "Rejected"
	case HistoryJournal:
		return "Journal"
	default:
		return "All"
	}
}

// HistoryRows returns every item grouped by local day of the sort key. A child
// is nested under its parent when both fall in the same day group; otherwise
// it is shown at the top level of its own group with its parent as context.
func (b *Board) HistoryRows(key SortKey, desc bool) []Row {
	return b.HistoryRowsFiltered(key, desc, HistoryAll)
}

// HistoryRowsFiltered returns rows for the selected History outcome filter.
func (b *Board) HistoryRowsFiltered(key SortKey, desc bool, filter HistoryFilter) []Row {
	matches := func(it *Item) bool {
		switch filter {
		case HistoryCompleted:
			return it.Done()
		case HistoryRejected:
			return it.Rejected()
		case HistoryJournal:
			return it.State == Journal
		default:
			return true
		}
	}
	keyOf := func(it *Item) (time.Time, bool) {
		if key == SortCompleted {
			if it.Completed != nil {
				return *it.Completed, true
			}
			if it.RejectedAt != nil {
				return *it.RejectedAt, true
			}
			return time.Time{}, false
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
		if !matches(it) {
			continue
		}
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
	// The "no outcome date" group sorts as the most recent.
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
