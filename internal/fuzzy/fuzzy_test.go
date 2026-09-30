package fuzzy

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMatch(t *testing.T) {
	tests := []struct {
		term, text string
		ok         bool
		pos        []int
	}{
		{"", "anything", true, nil},
		{"mvup", "Move item up", true, []int{0, 2, 10, 11}},
		{"up", "Cursor up", true, []int{7, 8}},          // the word, not "u" of Cursor
		{"undo", "Undo depth", true, []int{0, 1, 2, 3}}, // case-insensitive
		{"dep", "Undo depth", true, []int{5, 6, 7}},     // consecutive run
		{"hsort", "History sort", true, []int{0, 8, 9, 10, 11}},
		{"ls", "Link shortening", true, []int{0, 5}},   // word starts
		{"theme", "Theme", true, []int{0, 1, 2, 3, 4}}, // exact
		{"xyz", "Theme", false, nil},
		{"mouses", "Mouse", false, nil}, // longer than text
		{"ou", "Mouse", true, []int{1, 2}},
		{"theme", "Copy the selected item's ID", false, nil}, // no jumps into mid-word
		{"theme", "Release the mouse", true, []int{8, 9, 10, 12, 16}},
		{"ndo", "Undo depth", true, []int{1, 2, 3}},    // may start mid-word
		{"dp", "Undo depth", true, []int{5, 7}},        // not d of Undo, then mid-word p
		{"sf", "HistoryFilter", true, []int{2, 7}},     // camelCase starts a word
		{"éa", "Éclair à la crème", true, []int{0, 3}}, // folds case beyond ASCII
	}
	for _, tt := range tests {
		t.Run(tt.term+"/"+tt.text, func(t *testing.T) {
			_, pos, ok := Match(tt.term, tt.text)
			assert.Equal(t, tt.ok, ok)
			assert.Equal(t, tt.pos, pos)
		})
	}
}

func TestMatchScoresBetterAlignmentsHigher(t *testing.T) {
	word, _, _ := Match("up", "Move item up")
	scattered, _, _ := Match("up", "Cursor to top")
	assert.Greater(t, word, scattered)
}

func TestWord(t *testing.T) {
	tests := []struct {
		term, text string
		ok         bool
		pos        []int
	}{
		{"", "anything", true, nil},
		{"undo", "How many recent changes can be undone.", true, []int{31, 32, 33, 34}},
		{"ndo", "undone", true, []int{1, 2, 3}}, // inside one word
		{"down", "half_page_down", true, []int{10, 11, 12, 13}},
		{"page_do", "half_page_down", true, []int{5, 6, 7, 8, 9, 10, 11}}, // starts at a boundary
		{"e_d", "item_down", false, nil},                                  // spans words, mid-word start
		{"und", "Release the mouse for text selection", false, nil},
		{"RECENT", "How many recent changes", true, []int{9, 10, 11, 12, 13, 14}},
	}
	for _, tt := range tests {
		t.Run(tt.term+"/"+tt.text, func(t *testing.T) {
			pos, ok := Word(tt.term, tt.text)
			assert.Equal(t, tt.ok, ok)
			assert.Equal(t, tt.pos, pos)
		})
	}
}

func TestMatchFields(t *testing.T) {
	label := Field{Text: "Undo depth", Mode: Fuzzy}
	desc := Field{Text: "How many recent changes can be undone."}

	pos, ok := MatchFields("", label, desc)
	assert.True(t, ok)
	assert.Equal(t, [][]int{nil, nil}, pos)

	pos, ok = MatchFields("undo recent", label, desc)
	assert.True(t, ok)
	assert.Equal(t, [][]int{{0, 1, 2, 3}, {9, 10, 11, 12, 13, 14}}, pos, "each term credited to the first field it matches")

	pos, ok = MatchFields("dp dpt", label, desc)
	assert.True(t, ok)
	assert.Equal(t, [][]int{{5, 7, 8}, nil}, pos, "overlapping positions are merged")

	_, ok = MatchFields("undo missing", label, desc)
	assert.False(t, ok, "every term must match")

	_, ok = MatchFields("mnrc", label, desc)
	assert.False(t, ok, "descriptions do not match fuzzily")
}

func TestMatchFieldsExact(t *testing.T) {
	keys := []Field{{Text: "x", Mode: Exact}, {Text: "ctrl+x", Mode: Exact}, {Text: "G", Mode: Exact}}
	label := Field{Text: "Next view", Mode: Fuzzy}

	pos, ok := MatchFields("ctrl+x", append(keys, label)...)
	assert.True(t, ok)
	assert.Equal(t, [][]int{nil, {0, 1, 2, 3, 4, 5}, nil, nil}, pos)

	pos, ok = MatchFields("g", append(keys, label)...)
	assert.True(t, ok, "keys ignore case")
	assert.Equal(t, [][]int{nil, nil, {0}, nil}, pos)

	_, ok = MatchFields("ctrl", keys...)
	assert.False(t, ok, "only whole keys match")

	pos, ok = MatchFields("x", append(keys, label)...)
	assert.True(t, ok)
	assert.Equal(t, [][]int{{0}, nil, nil, nil}, pos, "credited to the first field that matches")
}

func TestTerms(t *testing.T) {
	assert.Equal(t, []string{"move", "up"}, Terms("  Move   UP "))
	assert.Empty(t, Terms("   "))
}
