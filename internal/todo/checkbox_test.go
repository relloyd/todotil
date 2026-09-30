package todo

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseCheckboxes(t *testing.T) {
	body := "intro\n- [ ] one\n  - [x] nested two\n* [X] three\n+ [ ]   \n- [] nope\n-[ ] nope\n- [ ] four  \n- [-] five\n- [~] nope"
	got := ParseCheckboxes(body)
	assert.Equal(t, []CheckLine{
		{LineNo: 1, Ordinal: 0, Text: "one"},
		{LineNo: 2, Ordinal: 1, Text: "nested two", Checked: true},
		{LineNo: 3, Ordinal: 2, Text: "three", Checked: true},
		{LineNo: 7, Ordinal: 3, Text: "four"},
		{LineNo: 8, Ordinal: 4, Text: "five", Rejected: true},
	}, got)
}

func TestSetCheckboxLine(t *testing.T) {
	tests := []struct {
		body string
		line int
		text string
		mark string
		want string
	}{
		{"a\n- [ ] x", 1, "y", markDone, "a\n- [x] y"},
		{"  * [x] x", 0, "x", markOpen, "  * [ ] x"},
		{"- [ ] x", 0, "x", markRejected, "- [-] x"},
		{"- [-] x", 0, "x", markOpen, "- [ ] x"},
		{"plain", 0, "y", markDone, "plain"},
		{"- [ ] x", 5, "y", markDone, "- [ ] x"},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, setCheckboxLine(tt.body, tt.line, tt.text, tt.mark))
	}
}

func TestMatchCheckboxes(t *testing.T) {
	kid := func(title string, line int) *Item { return &Item{Title: title, Line: line} }
	tests := []struct {
		name  string
		lines []string
		kids  []*Item
		want  []int
	}{
		{"exact", []string{"b", "a"}, []*Item{kid("a", 0), kid("b", 1)}, []int{1, 0}},
		{"normalised", []string{"Buy  MILK!"}, []*Item{kid("buy milk", 0)}, []int{0}},
		{"edit distance", []string{"buy oat milk"}, []*Item{kid("buy oatmilk", 0)}, []int{0}},
		{"position fallback", []string{"totally different"}, []*Item{kid("something else", 0)}, []int{0}},
		{"new line unmatched", []string{"a", "brand new"}, []*Item{kid("a", 0)}, []int{0, -1}},
		{"exact beats near", []string{"cat", "cats"}, []*Item{kid("cats", 0), kid("cat", 1)}, []int{1, 0}},
		{"duplicates pair in order", []string{"x", "x"}, []*Item{kid("x", 0), kid("x", 1)}, []int{0, 1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var lines []CheckLine
			for i, l := range tt.lines {
				lines = append(lines, CheckLine{Ordinal: i, Text: l})
			}
			assert.Equal(t, tt.want, MatchCheckboxes(lines, tt.kids))
		})
	}
}

func TestBodySync(t *testing.T) {
	s, _ := newTestService()
	r, err := s.Add("Notes\n\n- [ ] alpha\n- [ ] beta\n- [ ] gamma", Journal, "", Next)
	require.NoError(t, err)
	note := r.Item
	kids := s.Board().Children(note.ID)
	require.Len(t, kids, 3)
	alpha, beta, gamma := kids[0], kids[1], kids[2]

	// Complete gamma in the list: body line gets ticked.
	_, err = s.Complete(gamma.ID, false)
	require.NoError(t, err)
	assert.Contains(t, s.Board().Get(note.ID).Body, "- [x] gamma")

	// Edit body: reword alpha, tick beta, insert a new line, drop gamma.
	r, err = s.Edit(note.ID, "Notes\n\n- [ ] Alpha!\n- [ ] new one\n- [x] beta", Now)
	require.NoError(t, err)
	assert.Equal(t, SyncSummary{Created: 1, Rewritten: 1, Completed: 1, Detached: 1}, r.Sync)
	b := s.Board()
	assert.Equal(t, "Alpha!", b.Get(alpha.ID).Title)
	assert.True(t, b.Get(beta.ID).Done())
	assert.Empty(t, b.Get(gamma.ID).Source, "completed child detached, kept in history")
	assert.True(t, b.Get(gamma.ID).Done())
	assert.Equal(t, []string{"Notes › Alpha!", "Notes › new one"}, titles(b.ViewRows(Next)))

	// Removing an open line deletes the child.
	r, err = s.Edit(note.ID, "Notes\n\n- [x] beta", Now)
	require.NoError(t, err)
	assert.Equal(t, 2, r.Sync.Removed)
	assert.True(t, r.Sync.Bulk())
	assert.Nil(t, s.Board().Get(alpha.ID))
	assert.Empty(t, s.Board().ViewRows(Next))

	// Undo brings them back.
	_, err = s.Undo()
	require.NoError(t, err)
	assert.Equal(t, []string{"Notes › Alpha!", "Notes › new one"}, titles(s.Board().ViewRows(Next)))

	// Editing a child's title rewrites its body line.
	_, err = s.Edit(alpha.ID, "alpha again", Now)
	require.NoError(t, err)
	assert.Contains(t, s.Board().Get(note.ID).Body, "- [ ] alpha again")

	// Reopening beta unticks its line.
	_, err = s.Reopen(beta.ID)
	require.NoError(t, err)
	assert.Contains(t, s.Board().Get(note.ID).Body, "- [ ] beta")

	// Deleting a child removes its line and keeps later ordinals aligned.
	newOne := s.Board().ViewRows(Next)[1].Item
	require.NoError(t, s.Delete(alpha.ID))
	assert.Equal(t, "- [ ] new one\n- [ ] beta", s.Board().Get(note.ID).Body)
	_, err = s.Complete(newOne.ID, false)
	require.NoError(t, err)
	assert.Equal(t, "- [x] new one\n- [ ] beta", s.Board().Get(note.ID).Body)
}
