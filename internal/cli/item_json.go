package cli

import (
	"time"

	"github.com/relloyd/todotil/internal/todo"
)

// refJSON identifies an item briefly.
type refJSON struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	State    string `json:"state"`
	Done     bool   `json:"done"`
	Rejected bool   `json:"rejected"`
	Assignee string `json:"assignee,omitempty"`
}

func ref(it *todo.Item) refJSON {
	r := refJSON{ID: it.ID, Title: it.Title, State: string(it.State), Done: it.Done(), Rejected: it.Rejected()}
	if c := it.ActiveClaim(); c != nil {
		r.Assignee = c.Assignee
	}
	return r
}

// itemJSON is the full representation of an item.
type itemJSON struct {
	Position     int          `json:"position,omitempty"`
	ID           string       `json:"id"`
	Title        string       `json:"title"`
	Body         string       `json:"body,omitempty"`
	State        string       `json:"state"`
	Done         bool         `json:"done"`
	Rejected     bool         `json:"rejected"`
	Created      time.Time    `json:"created"`
	CreatedBy    string       `json:"created_by,omitempty"`
	Completed    *time.Time   `json:"completed,omitempty"`
	CompletedBy  string       `json:"completed_by,omitempty"`
	RejectedAt   *time.Time   `json:"rejected_at,omitempty"`
	RejectedBy   string       `json:"rejected_by,omitempty"`
	Parent       *refJSON     `json:"parent,omitempty"`
	Claim        *todo.Claim  `json:"claim,omitempty"`
	OpenChildren int          `json:"open_children"`
	Children     []itemJSON   `json:"children,omitempty"`
	Claims       []todo.Claim `json:"claims,omitempty"`
	Notes        []todo.Note  `json:"notes,omitempty"`
}

// item builds the JSON for it. full adds body, claim history and notes.
func item(b *todo.Board, it *todo.Item, full bool) itemJSON {
	j := itemJSON{
		ID: it.ID, Title: it.Title, State: string(it.State), Done: it.Done(), Rejected: it.Rejected(),
		Created: it.Created, CreatedBy: it.CreatedBy, Completed: it.Completed, CompletedBy: it.CompletedBy,
		RejectedAt: it.RejectedAt, RejectedBy: it.RejectedBy, Claim: it.ActiveClaim(),
	}
	if p := b.Get(it.Parent); p != nil {
		r := ref(p)
		j.Parent = &r
	}
	for _, c := range b.Descendants(it.ID) {
		if c.Open() {
			j.OpenChildren++
		}
	}
	if full {
		j.Body, j.Claims, j.Notes = it.Body, it.Claims, it.Notes
		for _, c := range b.Children(it.ID) {
			j.Children = append(j.Children, item(b, c, false))
		}
	}
	return j
}
