// Package todo holds the domain model: items, the in-memory board built from
// the event log, the operations that change it, and undo.
package todo

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"slices"
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
	// CreatedBy and CompletedBy name the agent responsible; empty means the
	// user in the TUI.
	CreatedBy   string `json:"created_by,omitempty"`
	CompletedBy string `json:"completed_by,omitempty"`
	// Claims is every claim ever made on the item, oldest first. At most
	// the last one is active.
	Claims []Claim `json:"claims,omitempty"`
	Notes  []Note  `json:"notes,omitempty"`
}

// Claim records an agent taking an item to work on.
type Claim struct {
	ID       string     `json:"id"`
	Assignee string     `json:"assignee"`
	At       time.Time  `json:"at"`
	Ended    *time.Time `json:"ended,omitempty"`
	// End says why the claim ended; Reason is free text given on release.
	End    ClaimEnd `json:"end,omitempty"`
	Reason string   `json:"reason,omitempty"`
}

// ClaimEnd is why a claim stopped being active.
type ClaimEnd string

const (
	EndDone       ClaimEnd = "done"
	EndReleased   ClaimEnd = "released"
	EndStolen     ClaimEnd = "stolen"
	EndUnassigned ClaimEnd = "unassigned"
	EndDemoted    ClaimEnd = "demoted"
)

// Active reports whether the claim is still held.
func (c Claim) Active() bool { return c.Ended == nil }

// Note is a timestamped progress note from an agent.
type Note struct {
	At   time.Time `json:"at"`
	By   string    `json:"by,omitempty"`
	Text string    `json:"text"`
}

// ActiveClaim returns the claim currently held on the item, or nil.
func (it *Item) ActiveClaim() *Claim {
	if n := len(it.Claims); n > 0 && it.Claims[n-1].Active() {
		return &it.Claims[n-1]
	}
	return nil
}

// endClaim ends the active claim, if any.
func (it *Item) endClaim(end ClaimEnd, at time.Time, reason string) {
	if c := it.ActiveClaim(); c != nil {
		c.Ended, c.End, c.Reason = &at, end, reason
	}
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
	c.Completed = cloneTime(it.Completed)
	if it.Claims != nil {
		c.Claims = make([]Claim, len(it.Claims))
		for i, cl := range it.Claims {
			cl.Ended = cloneTime(cl.Ended)
			c.Claims[i] = cl
		}
	}
	if it.Notes != nil {
		c.Notes = slices.Clone(it.Notes)
	}
	return &c
}

func cloneTime(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	c := *t
	return &c
}

func timePtrEqual(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Equal(*b)
}

// Equal reports whether two items hold the same data.
func (it *Item) Equal(o *Item) bool {
	if it == nil || o == nil {
		return it == o
	}
	if !timePtrEqual(it.Completed, o.Completed) {
		return false
	}
	claimEq := func(a, b Claim) bool {
		return a.ID == b.ID && a.Assignee == b.Assignee && a.At.Equal(b.At) &&
			timePtrEqual(a.Ended, b.Ended) && a.End == b.End && a.Reason == b.Reason
	}
	noteEq := func(a, b Note) bool { return a.At.Equal(b.At) && a.By == b.By && a.Text == b.Text }
	return it.ID == o.ID && it.Title == o.Title && it.Body == o.Body &&
		it.State == o.State && it.Parent == o.Parent && it.Order == o.Order &&
		it.Created.Equal(o.Created) && it.Source == o.Source && it.Line == o.Line &&
		it.CreatedBy == o.CreatedBy && it.CompletedBy == o.CompletedBy &&
		slices.EqualFunc(it.Claims, o.Claims, claimEq) && slices.EqualFunc(it.Notes, o.Notes, noteEq)
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

// NewID returns a random identifier. IDs used to start with a timestamp,
// which made short prefixes ambiguous for items created together; older
// IDs in the log keep that form and still resolve.
func NewID(time.Time) string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// NewClaimID returns a random claim identifier. The "c-" prefix keeps claim
// IDs visibly distinct from item IDs.
func NewClaimID(time.Time) string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return "c-" + hex.EncodeToString(b[:])
}
