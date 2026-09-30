package todo

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Errors specific to claims.
var (
	ErrNoAssignee   = errors.New("an assignee name is required")
	ErrNotClaimable = errors.New("item can't be claimed")
	ErrNotClaimed   = errors.New("item isn't claimed")
)

// ClaimedError is returned when someone else holds the claim.
type ClaimedError struct {
	Item  *Item
	Claim Claim
}

func (e *ClaimedError) Error() string {
	return fmt.Sprintf("%q is claimed by %s since %s", e.Item.Title, e.Claim.Assignee, e.Claim.At.Local().Format(time.RFC3339))
}

// ClaimEndedError is returned when a claim is no longer active.
type ClaimEndedError struct {
	Item  *Item
	Claim Claim
}

func (e *ClaimEndedError) Error() string {
	why := string(e.Claim.End)
	switch e.Claim.End {
	case EndStolen:
		why = "taken over by another assignee"
	case EndUnassigned:
		why = "unassigned by the user"
	case EndDemoted:
		why = "ended because the item became a journal note"
	}
	msg := fmt.Sprintf("claim %s on %q is no longer valid: %s", e.Claim.ID, e.Item.Title, why)
	if e.Claim.Reason != "" {
		msg += " (" + e.Claim.Reason + ")"
	}
	return msg
}

// Target selects an item to claim: the next unclaimed one, a 1-based
// position among a list's top-level rows, or an item ID (or prefix).
type Target struct {
	Next     bool
	Position int
	ID       string
}

// ParseTarget interprets a claim argument: "next", a number with fewer
// than MinPrefix digits (a position), or an ID.
func ParseTarget(arg string) (Target, error) {
	arg = strings.TrimSpace(arg)
	switch {
	case arg == "":
		return Target{}, errors.New("missing position, item ID or \"next\"")
	case arg == "next":
		return Target{Next: true}, nil
	case len(arg) < MinPrefix && isDigits(arg):
		var n int
		_, _ = fmt.Sscanf(arg, "%d", &n)
		if n < 1 {
			return Target{}, fmt.Errorf("positions start at 1, got %s", arg)
		}
		return Target{Position: n}, nil
	}
	return Target{ID: arg}, nil
}

func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

// TopLevel returns the top-level open items of an active list, in order.
// Positions are 1-based indexes into this slice.
func (b *Board) TopLevel(s State) []*Item {
	var out []*Item
	for _, r := range b.ViewRows(s) {
		if r.Depth == 0 {
			out = append(out, r.Item)
		}
	}
	return out
}

// ClaimResult describes a successful claim.
type ClaimResult struct {
	Item        *Item
	Claim       Claim
	AlreadyHeld bool   // the assignee already held it; the existing claim is returned
	StoleFrom   string // previous holder when stolen
	MovedFrom   State  // set when claiming moved the item to Now
}

// Claim assigns an item to the service's Actor and moves it to Now. The
// selection and the claim happen in one transaction, so concurrent agents
// never receive the same item.
func (s *Service) Claim(target Target, list State, steal bool) (ClaimResult, error) {
	var res ClaimResult
	if s.Actor == "" {
		return res, ErrNoAssignee
	}
	if !list.Active() {
		list = Now
	}
	err := s.run("claim", false, func(t *tx) error {
		res = ClaimResult{}
		it, err := t.pick(target, list)
		if err != nil {
			return err
		}
		if !it.Open() {
			return fmt.Errorf("%w: %q is %s", ErrNotClaimable, it.Title, describeState(it))
		}
		it = it.Clone()
		if c := it.ActiveClaim(); c != nil {
			switch {
			case c.Assignee == s.Actor:
				res.AlreadyHeld = true
			case !steal:
				return &ClaimedError{Item: it, Claim: *c}
			default:
				res.StoleFrom = c.Assignee
				it.endClaim(EndStolen, t.now, "taken by "+s.Actor)
			}
		}
		if !res.AlreadyHeld {
			it.Claims = append(it.Claims, Claim{ID: s.NewClaim(t.now), Assignee: s.Actor, At: t.now})
		}
		t.put(it)
		if it.State != Now {
			res.MovedFrom = it.State
			if _, _, err := t.move(it.ID, Now); err != nil {
				return err
			}
		}
		res.Item = it
		return nil
	})
	if err != nil {
		return ClaimResult{}, err
	}
	res.Item = s.board.Get(res.Item.ID)
	res.Claim = *res.Item.ActiveClaim()
	return res, nil
}

func describeState(it *Item) string {
	switch {
	case it.Done():
		return "already done"
	case it.Rejected():
		return "already rejected"
	case it.State == Journal:
		return "a journal note"
	}
	return string(it.State)
}

// pick resolves a claim target against the working board.
func (t *tx) pick(target Target, list State) (*Item, error) {
	switch {
	case target.ID != "":
		return t.w.Resolve(target.ID)
	case target.Next:
		for _, it := range t.w.TopLevel(list) {
			if it.ActiveClaim() == nil {
				return it, nil
			}
		}
		return nil, fmt.Errorf("%w: nothing unclaimed in %s", ErrNotFound, list.Label())
	}
	tops := t.w.TopLevel(list)
	if target.Position > len(tops) {
		return nil, fmt.Errorf("%w: position %d is past the end of %s (%d items)", ErrNotFound, target.Position, list.Label(), len(tops))
	}
	return tops[target.Position-1], nil
}

// activeClaim finds an item by claim ID and checks the claim is still held.
func (t *tx) activeClaim(claimID string) (*Item, Claim, error) {
	it, c, err := t.w.FindClaim(claimID)
	if err != nil {
		return nil, c, err
	}
	if !c.Active() {
		return it, c, &ClaimEndedError{Item: it, Claim: c}
	}
	return it.Clone(), c, nil
}

// DoneResult describes the outcome of finishing a claim.
type DoneResult struct {
	Item        *Item
	Claim       Claim
	AlreadyDone bool // completed earlier, for example by the user
	Cascaded    int  // open children completed along with it
}

// Finish completes the item behind an active claim. If the claim already
// ended because the item was completed, it succeeds with AlreadyDone. With
// open children it returns *NeedsConfirmError unless cascade is set. An
// optional note is recorded first.
func (s *Service) Finish(claimID string, cascade bool, note string) (DoneResult, error) {
	var res DoneResult
	err := s.run("done", false, func(t *tx) error {
		res = DoneResult{}
		it, c, err := t.activeClaim(claimID)
		var ended *ClaimEndedError
		if errors.As(err, &ended) && c.End == EndDone {
			res = DoneResult{Item: it, Claim: c, AlreadyDone: true}
			return nil
		}
		if err != nil {
			return err
		}
		if note = strings.TrimSpace(note); note != "" {
			it.Notes = append(it.Notes, Note{At: t.now, By: s.Actor, Text: note})
			t.put(it)
		}
		res.Cascaded, err = t.complete(it.ID, cascade)
		res.Item, res.Claim = it, c
		return err
	})
	if err != nil {
		return DoneResult{}, err
	}
	if !res.AlreadyDone {
		res.Item = s.board.Get(res.Item.ID)
		for _, c := range res.Item.Claims {
			if c.ID == res.Claim.ID {
				res.Claim = c
			}
		}
	}
	return res, nil
}

// Release gives up an active claim. The item stays where it is.
func (s *Service) Release(claimID, reason string) (*Item, error) {
	var id string
	err := s.run("release", false, func(t *tx) error {
		it, _, err := t.activeClaim(claimID)
		if err != nil {
			return err
		}
		it.endClaim(EndReleased, t.now, strings.TrimSpace(reason))
		t.put(it)
		id = it.ID
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.board.Get(id), nil
}

// AddNote records a progress note against an active claim.
func (s *Service) AddNote(claimID, text string) (*Item, Note, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, Note{}, ErrEmpty
	}
	var (
		id   string
		note Note
	)
	err := s.run("note", false, func(t *tx) error {
		it, _, err := t.activeClaim(claimID)
		if err != nil {
			return err
		}
		note = Note{At: t.now, By: s.Actor, Text: text}
		it.Notes = append(it.Notes, note)
		t.put(it)
		id = it.ID
		return nil
	})
	if err != nil {
		return nil, Note{}, err
	}
	return s.board.Get(id), note, nil
}

// Unassign ends the active claim on an item on the user's behalf. It is
// undoable.
func (s *Service) Unassign(id string) (Claim, error) {
	var ended Claim
	err := s.run("unassign", true, func(t *tx) error {
		it := t.get(id)
		if it == nil {
			return ErrNotFound
		}
		c := it.ActiveClaim()
		if c == nil {
			return ErrNotClaimed
		}
		ended = *c
		it.endClaim(EndUnassigned, t.now, "")
		t.put(it)
		return nil
	})
	return ended, err
}
