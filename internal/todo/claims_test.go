package todo

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newAgentService(t *testing.T) (*Service, *memLog) {
	t.Helper()
	s, log := newTestService()
	// IDs long enough for prefix lookups.
	ids, claims := 0, 0
	s.NewID = func(time.Time) string { ids++; return fmt.Sprintf("%04d-item-id", ids) }
	s.NewClaim = func(time.Time) string { claims++; return fmt.Sprintf("c-%04d", claims) }
	return s, log
}

// as runs fn with the service acting as the named agent ("" is the user).
func as(s *Service, who string, fn func()) {
	prev := s.Actor
	s.Actor = who
	defer func() { s.Actor = prev }()
	fn()
}

func TestParseTarget(t *testing.T) {
	tests := []struct {
		arg     string
		want    Target
		wantErr bool
	}{
		{"next", Target{Next: true}, false},
		{"1", Target{Position: 1}, false},
		{"42", Target{Position: 42}, false},
		{"12345", Target{Position: 12345}, false},
		{"123456", Target{ID: "123456"}, false},
		{"1a0ee867f2", Target{ID: "1a0ee867f2"}, false},
		{"0", Target{}, true},
		{"", Target{}, true},
	}
	for _, tt := range tests {
		got, err := ParseTarget(tt.arg)
		if tt.wantErr {
			assert.Error(t, err, tt.arg)
			continue
		}
		require.NoError(t, err, tt.arg)
		assert.Equal(t, tt.want, got, tt.arg)
	}
}

func TestClaimTargets(t *testing.T) {
	s, _ := newAgentService(t)
	a := add(t, s, "a", Now)
	addChild(t, s, a, "a1", Now)
	add(t, s, "b", Now)
	c := add(t, s, "c", Later)
	s.Actor = "bot"

	tests := []struct {
		name   string
		target Target
		list   State
		want   string
		err    error
	}{
		{"position counts top-level rows", Target{Position: 2}, Now, "b", nil},
		{"position in another list", Target{Position: 1}, Later, "c", nil},
		{"position past the end", Target{Position: 9}, Now, "", ErrNotFound},
		{"id prefix", Target{ID: c.ID[:8]}, Now, "c", nil},
		{"short prefix is not an id", Target{ID: c.ID[:4]}, Now, "", ErrNotFound},
		{"child by id", Target{ID: s.Board().Children(a.ID)[0].ID}, Now, "a1", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tx := s.begin("test")
			it, err := tx.pick(tt.target, tt.list)
			if tt.err != nil {
				assert.ErrorIs(t, err, tt.err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, it.Title)
		})
	}
}

func TestClaimLifecycle(t *testing.T) {
	s, _ := newAgentService(t)
	p := add(t, s, "parent", Next)
	addChild(t, s, p, "same-state child", Next)
	add(t, s, "other", Now)

	_, err := s.Claim(Target{Position: 1}, Next, false)
	assert.ErrorIs(t, err, ErrNoAssignee)

	var res ClaimResult
	as(s, "bot-1", func() { res, err = s.Claim(Target{Position: 1}, Next, false) })
	require.NoError(t, err)
	assert.Equal(t, "parent", res.Item.Title)
	assert.Equal(t, "c-0001", res.Claim.ID)
	assert.Equal(t, "bot-1", res.Claim.Assignee)
	assert.Equal(t, Next, res.MovedFrom)
	assert.Equal(t, []string{"other", "parent", "  same-state child"}, titles(s.Board().ViewRows(Now)),
		"claiming moves to Now and same-state children follow")

	// Re-claiming your own item returns the same claim.
	as(s, "bot-1", func() { res, err = s.Claim(Target{ID: p.ID}, Now, false) })
	require.NoError(t, err)
	assert.True(t, res.AlreadyHeld)
	assert.Equal(t, "c-0001", res.Claim.ID)

	// Someone else is refused, unless they steal.
	as(s, "bot-2", func() { _, err = s.Claim(Target{ID: p.ID}, Now, false) })
	var ce *ClaimedError
	require.True(t, errors.As(err, &ce))
	assert.Equal(t, "bot-1", ce.Claim.Assignee)

	as(s, "bot-2", func() { res, err = s.Claim(Target{ID: p.ID}, Now, true) })
	require.NoError(t, err)
	assert.Equal(t, "bot-1", res.StoleFrom)
	assert.Equal(t, "c-0002", res.Claim.ID)

	// The old claim is now stale.
	as(s, "bot-1", func() { _, err = s.Finish("c-0001", false, "") })
	var ended *ClaimEndedError
	require.True(t, errors.As(err, &ended))
	assert.Equal(t, EndStolen, ended.Claim.End)

	// Done with open children needs --cascade.
	as(s, "bot-2", func() { _, err = s.Finish("c-0002", false, "") })
	var nc *NeedsConfirmError
	require.True(t, errors.As(err, &nc))
	require.Len(t, nc.Children, 1)
	assert.Equal(t, "same-state child", nc.Children[0].Title)

	var done DoneResult
	as(s, "bot-2", func() { done, err = s.Finish("c-0002", true, "all tests pass") })
	require.NoError(t, err)
	assert.Equal(t, 1, done.Cascaded)
	assert.True(t, done.Item.Done())
	assert.Equal(t, "bot-2", done.Item.CompletedBy)
	assert.Equal(t, EndDone, done.Claim.End)
	assert.Equal(t, "all tests pass", done.Item.Notes[0].Text)
	assert.Equal(t, []string{"other"}, titles(s.Board().ViewRows(Now)))

	// Finishing twice reports already done.
	as(s, "bot-2", func() { done, err = s.Finish("c-0002", false, "") })
	require.NoError(t, err)
	assert.True(t, done.AlreadyDone)
}

func TestClaimRefusals(t *testing.T) {
	s, _ := newAgentService(t)
	j := add(t, s, "journal", Journal)
	d := add(t, s, "done", Now)
	_, err := s.Complete(d.ID, false)
	require.NoError(t, err)
	s.Actor = "bot"
	for _, id := range []string{j.ID, d.ID} {
		_, err := s.Claim(Target{ID: id}, Now, false)
		assert.ErrorIs(t, err, ErrNotClaimable)
	}
	_, err = s.Claim(Target{Next: true}, Now, false)
	assert.ErrorIs(t, err, ErrNotFound)
	_, err = s.Finish("c-9999", false, "")
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestClaimNextSkipsClaimed(t *testing.T) {
	s, _ := newAgentService(t)
	add(t, s, "one", Now)
	add(t, s, "two", Now)
	var got []string
	for _, who := range []string{"a", "b"} {
		as(s, who, func() {
			res, err := s.Claim(Target{Next: true}, Now, false)
			require.NoError(t, err)
			got = append(got, res.Item.Title)
		})
	}
	assert.Equal(t, []string{"one", "two"}, got)
	as(s, "c", func() {
		_, err := s.Claim(Target{Next: true}, Now, false)
		assert.ErrorIs(t, err, ErrNotFound)
	})
}

func TestUserActionsEndClaims(t *testing.T) {
	tests := []struct {
		name    string
		act     func(s *Service, id string) error
		wantEnd ClaimEnd
		done    bool
	}{
		{"user completes", func(s *Service, id string) error { _, err := s.Complete(id, false); return err }, EndDone, true},
		{"user unassigns", func(s *Service, id string) error { _, err := s.Unassign(id); return err }, EndUnassigned, false},
		{"user demotes", func(s *Service, id string) error { _, err := s.Move(id, Journal); return err }, EndDemoted, false},
		{"agent releases", func(s *Service, _ string) error {
			var err error
			as(s, "bot", func() { _, err = s.Release("c-0001", "blocked on review") })
			return err
		}, EndReleased, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _ := newAgentService(t)
			it := add(t, s, "task", Now)
			as(s, "bot", func() {
				_, err := s.Claim(Target{ID: it.ID}, Now, false)
				require.NoError(t, err)
			})
			require.NoError(t, tt.act(s, it.ID))
			got := s.Board().Get(it.ID)
			assert.Nil(t, got.ActiveClaim())
			assert.Equal(t, tt.wantEnd, got.Claims[0].End)

			var res DoneResult
			var err error
			as(s, "bot", func() { res, err = s.Finish("c-0001", false, "") })
			if tt.done {
				require.NoError(t, err)
				assert.True(t, res.AlreadyDone)
				assert.Empty(t, res.Item.CompletedBy, "completed by the user")
				return
			}
			var ended *ClaimEndedError
			assert.True(t, errors.As(err, &ended), "%v", err)
		})
	}
}

func TestNotes(t *testing.T) {
	s, _ := newAgentService(t)
	it := add(t, s, "task\n\n- [ ] sub", Now)
	s.Actor = "bot"
	_, err := s.Claim(Target{ID: it.ID}, Now, false)
	require.NoError(t, err)
	_, _, err = s.AddNote("c-0001", "  ")
	assert.ErrorIs(t, err, ErrEmpty)
	got, note, err := s.AddNote("c-0001", "- [ ] looks like a checkbox")
	require.NoError(t, err)
	assert.Equal(t, "bot", note.By)
	assert.Len(t, got.Notes, 1)
	assert.Len(t, s.Board().Children(it.ID), 1, "notes never create checkbox children")
	assert.Equal(t, "- [ ] sub", got.Body, "notes don't touch the body")
}

func TestCheckboxChildDoneTicksParentLine(t *testing.T) {
	s, _ := newAgentService(t)
	note := add(t, s, "meeting\n\n- [ ] follow up", Journal)
	kid := s.Board().Children(note.ID)[0]
	s.Actor = "bot"
	res, err := s.Claim(Target{ID: kid.ID}, Now, false)
	require.NoError(t, err)
	_, err = s.Finish(res.Claim.ID, false, "")
	require.NoError(t, err)
	assert.Equal(t, "- [x] follow up", s.Board().Get(note.ID).Body)
}

func TestAgentProvenance(t *testing.T) {
	s, log := newAgentService(t)
	s.Actor = "bot"
	r, err := s.Add("found a bug\n\n- [ ] repro", Now, "", Now)
	require.NoError(t, err)
	assert.Equal(t, "bot", r.Item.CreatedBy)
	assert.Equal(t, "bot", s.Board().Children(r.Item.ID)[0].CreatedBy)
	assert.Equal(t, "bot", log.events[0].By)
	assert.Equal(t, "bot", s.Board().LastActor(r.Item.ID))
}

// foreignPut simulates another process rewriting an item.
func foreignPut(log *memLog, it *Item, by string, change func(*Item)) {
	c := it.Clone()
	change(c)
	log.pending = append(log.pending, Event{Op: OpPut, Item: c, By: by, Tx: 999})
}

func TestUndoRefusedAfterForeignChange(t *testing.T) {
	s, log := newAgentService(t)
	it := add(t, s, "task", Now)
	other := add(t, s, "other", Now)
	_, err := s.Move(it.ID, Next)
	require.NoError(t, err)

	foreignPut(log, s.Board().Get(it.ID), "bot", func(c *Item) { c.Title = "renamed by bot" })
	_, err = s.Undo()
	var uc *UndoConflictError
	require.True(t, errors.As(err, &uc))
	assert.Equal(t, "bot", uc.By)
	assert.Contains(t, err.Error(), "can't undo move to Next: bot changed it since")
	assert.Equal(t, Next, s.Board().Get(it.ID).State, "nothing reverted")

	// The conflicting entry is dropped; earlier ones still work.
	_, err = s.Undo()
	require.NoError(t, err)
	assert.Nil(t, s.Board().Get(other.ID))
}

func TestCommitRetriesAfterRace(t *testing.T) {
	s, log := newAgentService(t)
	it := add(t, s, "task", Now)
	s.Actor = "bot-1"
	// Another agent claims the item after we picked it but before we wrote.
	log.race = func() {
		foreignPut(log, s.Board().Get(it.ID), "bot-2", func(c *Item) {
			c.Claims = append(c.Claims, Claim{ID: "c-other", Assignee: "bot-2", At: t0})
		})
	}
	_, err := s.Claim(Target{Next: true}, Now, false)
	assert.ErrorIs(t, err, ErrNotFound, "retry sees the item is taken and nothing else is free")
	assert.Equal(t, "bot-2", s.Board().Get(it.ID).ActiveClaim().Assignee)
}

func TestForeignEventsBecomeVisibleOnSync(t *testing.T) {
	s, log := newAgentService(t)
	it := add(t, s, "task", Now)
	foreignPut(log, s.Board().Get(it.ID), "bot", func(c *Item) { c.State = Later })
	changed, err := s.Sync()
	require.NoError(t, err)
	assert.True(t, changed)
	assert.Equal(t, []string{"task"}, titles(s.Board().ViewRows(Later)))
}

func TestShortIDs(t *testing.T) {
	b := NewBoard()
	for _, id := range []string{
		"1a0ee867f2a41eba68c68", // old timestamp IDs sharing a long prefix
		"1a0ee867f2b99eba68c68",
		"9f3c2d1e0a4b5c6d",
		"9f3c2d1e77777777",
		"abcdef0123456789",
	} {
		b.put(&Item{ID: id, State: Now})
	}
	assert.Equal(t, map[string]string{
		"1a0ee867f2a41eba68c68": "1a0ee867f2a",
		"1a0ee867f2b99eba68c68": "1a0ee867f2b",
		"9f3c2d1e0a4b5c6d":      "9f3c2d1e0",
		"9f3c2d1e77777777":      "9f3c2d1e7",
		"abcdef0123456789":      "abcdef01",
	}, b.ShortIDs())
	for id, short := range b.ShortIDs() {
		got, err := b.Resolve(short)
		require.NoError(t, err)
		assert.Equal(t, id, got.ID)
	}
}
