// Package todo holds the domain model: items, the in-memory board built from
// the event log, the operations that change it, and undo.
package todo

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// State classifies an item. Completion is tracked separately via
// Item.Completed, so a done item keeps the state it was completed in.
type State string

const (
	Now     State = "now"
	Next    State = "next"
	Later   State = "later"
	Journal State = "journal"
)

// ActiveStates are the states that have their own list view.
var ActiveStates = []State{Now, Next, Later}

// Active reports whether s is one of Now, Next or Later.
func (s State) Active() bool { return s == Now || s == Next || s == Later }

// Label is the human readable name of the state.
func (s State) Label() string {
	switch s {
	case Now:
		return "Now"
	case Next:
		return "Next"
	case Later:
		return "Later"
	case Journal:
		return "Journal"
	}
	return string(s)
}

// ParseState converts a config string into a State.
func ParseState(s string) (State, error) {
	switch st := State(strings.ToLower(strings.TrimSpace(s))); st {
	case Now, Next, Later, Journal:
		return st, nil
	}
	return "", fmt.Errorf("unknown state %q", s)
}

// Item is the single record kept for every todo and journal entry.
type Item struct {
	ID        string     `json:"id"`
	Title     string     `json:"title"`
	Body      string     `json:"body,omitempty"`
	State     State      `json:"state"`
	Parent    string     `json:"parent,omitempty"`
	Order     float64    `json:"order"`
	Created   time.Time  `json:"created"`
	Completed *time.Time `json:"completed,omitempty"`
	// Source is the ID of the item whose body holds this item's checkbox
	// line. Empty when the item did not come from (or was detached from) a
	// checkbox line.
	Source string `json:"source,omitempty"`
	// Line is the checkbox ordinal within the source body at the last sync.
	// It is the lowest-confidence signal used when matching lines to items.
	Line int `json:"line,omitempty"`
}

// Done reports whether the item has been completed.
func (it *Item) Done() bool { return it.Completed != nil }

// Open reports whether the item is shown in one of the active views.
func (it *Item) Open() bool { return it.State.Active() && !it.Done() }

// Text joins title and body using the Git commit message convention.
func (it *Item) Text() string {
	if it.Body == "" {
		return it.Title
	}
	return it.Title + "\n\n" + it.Body
}

// Clone returns a deep copy.
func (it *Item) Clone() *Item {
	if it == nil {
		return nil
	}
	c := *it
	if it.Completed != nil {
		t := *it.Completed
		c.Completed = &t
	}
	return &c
}

// Equal reports whether two items hold the same data.
func (it *Item) Equal(o *Item) bool {
	if it == nil || o == nil {
		return it == o
	}
	if (it.Completed == nil) != (o.Completed == nil) {
		return false
	}
	if it.Completed != nil && !it.Completed.Equal(*o.Completed) {
		return false
	}
	return it.ID == o.ID && it.Title == o.Title && it.Body == o.Body &&
		it.State == o.State && it.Parent == o.Parent && it.Order == o.Order &&
		it.Created.Equal(o.Created) && it.Source == o.Source && it.Line == o.Line
}

// SplitText splits entry text into a title (first line) and body (everything
// after the first line, with the separating blank lines removed).
func SplitText(text string) (title, body string) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.Trim(text, "\n")
	title, body, _ = strings.Cut(text, "\n")
	title = strings.TrimSpace(title)
	body = strings.TrimRight(strings.TrimLeft(body, "\n"), " \t\n")
	return title, body
}

// NewID returns a time-sortable random identifier.
func NewID(now time.Time) string {
	var b [5]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("%011x%s", now.UnixMilli(), hex.EncodeToString(b[:]))
}
