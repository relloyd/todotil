package todo

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRejectConfirmCascadeAndUndo(t *testing.T) {
	s, _ := newTestService()
	parent := add(t, s, "parent", Now)
	child := addChild(t, s, parent, "child", Next)
	grandchild := addChild(t, s, child, "grandchild", Later)
	journal := addChild(t, s, parent, "journal", Journal)

	_, err := s.Reject(parent.ID, false)
	var confirm *NeedsConfirmError
	require.True(t, errors.As(err, &confirm))
	assert.Equal(t, 2, confirm.Open)
	assert.False(t, s.Board().Get(parent.ID).Rejected())

	result, err := s.Reject(parent.ID, true)
	require.NoError(t, err)
	assert.Equal(t, 2, result.Followers)
	for _, id := range []string{parent.ID, child.ID, grandchild.ID} {
		it := s.Board().Get(id)
		require.NotNil(t, it)
		assert.True(t, it.Rejected())
		assert.False(t, it.Done())
		assert.False(t, it.Open())
	}
	assert.False(t, s.Board().Get(journal.ID).Rejected())
	assert.Empty(t, s.Board().ViewRows(Now))
	assert.Empty(t, s.Board().ViewRows(Next))
	assert.Empty(t, s.Board().ViewRows(Later))

	_, err = s.Complete(parent.ID, false)
	assert.ErrorIs(t, err, ErrRejected)

	_, err = s.Undo()
	require.NoError(t, err)
	for _, id := range []string{parent.ID, child.ID, grandchild.ID} {
		assert.False(t, s.Board().Get(id).Rejected())
	}
	assert.True(t, s.Board().Get(parent.ID).Open())
	assert.True(t, s.Board().Get(child.ID).Open())
	assert.True(t, s.Board().Get(grandchild.ID).Open())
}

func TestRejectEndsActiveClaim(t *testing.T) {
	s, _ := newAgentService(t)
	it := add(t, s, "task", Now)
	s.Actor = "agent"
	claimed, err := s.Claim(Target{ID: it.ID}, Now, false)
	require.NoError(t, err)
	s.Actor = ""

	_, err = s.Reject(it.ID, false)
	require.NoError(t, err)

	rejected := s.Board().Get(it.ID)
	require.NotNil(t, rejected)
	require.Len(t, rejected.Claims, 1)
	assert.NotNil(t, rejected.RejectedAt)
	assert.Empty(t, rejected.RejectedBy)
	assert.Equal(t, EndRejected, rejected.Claims[0].End)

	s.Actor = "agent"
	_, err = s.Finish(claimed.Claim.ID, false, "")
	var ended *ClaimEndedError
	require.ErrorAs(t, err, &ended)
	assert.Equal(t, EndRejected, ended.Claim.End)

	s.Actor = ""
	_, err = s.Undo()
	require.NoError(t, err)
	restored := s.Board().Get(it.ID)
	assert.False(t, restored.Rejected())
	require.NotNil(t, restored.ActiveClaim())
	assert.True(t, restored.ActiveClaim().Active())
}

func TestRejectRequiresAnOpenItem(t *testing.T) {
	tests := []struct {
		name      string
		state     State
		complete  bool
		wantError error
	}{
		{"completed", Now, true, ErrCannotRejectDone},
		{"journal", Journal, false, ErrJournalReject},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _ := newTestService()
			it := add(t, s, tt.name, tt.state)
			if tt.complete {
				_, err := s.Complete(it.ID, false)
				require.NoError(t, err)
			}
			_, err := s.Reject(it.ID, false)
			assert.ErrorIs(t, err, tt.wantError)
		})
	}
}

func TestHistoryRowsFilteredByOutcome(t *testing.T) {
	s, _ := newTestService()
	completed := add(t, s, "completed", Now)
	rejected := add(t, s, "rejected", Next)
	open := add(t, s, "open", Later)
	journal := add(t, s, "journal", Journal)

	_, err := s.Complete(completed.ID, false)
	require.NoError(t, err)
	_, err = s.Reject(rejected.ID, false)
	require.NoError(t, err)

	tests := []struct {
		name   string
		filter HistoryFilter
		want   []string
	}{
		{"all", HistoryAll, []string{"completed", "rejected", "open", "journal"}},
		{"completed", HistoryCompleted, []string{"completed"}},
		{"rejected", HistoryRejected, []string{"rejected"}},
		{"journal", HistoryJournal, []string{"journal"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rows := s.Board().HistoryRowsFiltered(SortCreated, false, tt.filter)
			assert.Equal(t, tt.want, titles(rows))
		})
	}

	rejectedRows := s.Board().HistoryRowsFiltered(SortCompleted, false, HistoryRejected)
	assert.Equal(t, []string{rejected.Title}, titles(rejectedRows))
	assert.NotEmpty(t, s.Board().HistoryRows(SortCreated, false))
	assert.NotNil(t, s.Board().Get(open.ID))
	assert.NotNil(t, s.Board().Get(journal.ID))
}

func TestMovingOpenParentDoesNotMoveRejectedChildren(t *testing.T) {
	s, _ := newTestService()
	parent := add(t, s, "parent", Now)
	openChild := addChild(t, s, parent, "open child", Now)
	rejectedChild := addChild(t, s, parent, "rejected child", Now)
	_, err := s.Reject(rejectedChild.ID, false)
	require.NoError(t, err)

	_, err = s.Move(parent.ID, Next)
	require.NoError(t, err)

	assert.Equal(t, Next, s.Board().Get(parent.ID).State)
	assert.Equal(t, Next, s.Board().Get(openChild.ID).State)
	assert.Equal(t, Now, s.Board().Get(rejectedChild.ID).State)
	assert.True(t, s.Board().Get(rejectedChild.ID).Rejected())
}

func TestCheckboxTickCompletesRejectedChild(t *testing.T) {
	s, _ := newTestService()
	note := add(t, s, "note\n\n- [ ] child", Now)
	children := s.Board().Children(note.ID)
	require.Len(t, children, 1)
	child := children[0]
	_, err := s.Reject(child.ID, false)
	require.NoError(t, err)

	_, err = s.Edit(note.ID, "note\n\n- [x] child", Now)
	require.NoError(t, err)

	updated := s.Board().Get(child.ID)
	assert.True(t, updated.Done())
	assert.False(t, updated.Rejected())
	assert.Nil(t, updated.RejectedAt)
}

func TestRemovingRejectedCheckboxPreservesHistoryItem(t *testing.T) {
	s, _ := newTestService()
	note := add(t, s, "note\n\n- [ ] child", Now)
	children := s.Board().Children(note.ID)
	require.Len(t, children, 1)
	child := children[0]
	_, err := s.Reject(child.ID, false)
	require.NoError(t, err)

	result, err := s.Edit(note.ID, "note", Now)
	require.NoError(t, err)

	assert.Equal(t, 1, result.Sync.Detached)
	preserved := s.Board().Get(child.ID)
	require.NotNil(t, preserved)
	assert.Empty(t, preserved.Source)
	assert.True(t, preserved.Rejected())
}
