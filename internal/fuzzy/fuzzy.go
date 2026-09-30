// Package fuzzy matches filter queries against short texts such as setting
// labels or item titles.
//
// A query is split on spaces into terms, and every term must match. A term
// matches a text either fuzzily (its characters appear in order, not
// necessarily together) or by word (it appears as a whole run that starts at
// a word boundary or sits inside one word) or exactly (it is the whole text,
// as for a key binding). Fuzzy matching suits short labels; long sentences
// need word matching, because almost any short term is a loose subsequence of
// a sentence.
package fuzzy

import (
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Scoring, loosely after fzf: every matched character scores, characters at
// the start of a word and runs of consecutive characters score extra, and
// gaps between matched characters cost a little.
const (
	scoreMatch     = 16
	bonusBoundary  = 8
	bonusConsec    = 6
	penaltyGap     = 3
	penaltyGapStep = 1

	none = -1 << 30 // no alignment
)

// Terms splits a query into its lower-case terms.
func Terms(query string) []string {
	return strings.Fields(strings.ToLower(query))
}

// Match fuzzily matches one term against text, ignoring case. It returns the
// score of the best alignment and the rune positions in text it matched.
func Match(term, text string) (score int, pos []int, ok bool) {
	p := []rune(strings.ToLower(term))
	t := []rune(text)
	n, m := len(t), len(p)
	if m == 0 {
		return 0, nil, true
	}
	if m > n {
		return 0, nil, false
	}
	lower := make([]rune, n)
	for i, r := range t {
		lower[i] = unicode.ToLower(r)
	}

	// best[j][i] is the best score of p[:j+1] with p[j] matched at t[i], and
	// from[j][i] is where p[j-1] was matched on that path.
	//
	// After a gap, a match must start a word or stay in the word of the
	// previous match. Jumping into the middle of another word makes almost
	// any term match a sentence ("theme" in "the selected item").
	best := make([][]int, m)
	from := make([][]int, m)
	for j := range m {
		best[j] = make([]int, n)
		from[j] = make([]int, n)
		// gap is the best score to extend with a gap: p[j-1] matched at
		// gapAt, at least one rune before the current position. word is
		// the same, limited to matches in the current word.
		gap, gapAt := none, -1
		word, wordAt := none, -1
		for i := range n {
			best[j][i] = none
			if j > 0 && i >= 2 {
				gap, word = decay(gap), decay(word)
				if s := best[j-1][i-2]; s != none && s-penaltyGap > gap {
					gap, gapAt = s-penaltyGap, i-2
				}
				if !isWordRune(t[i-1]) {
					word = none
				} else if s := best[j-1][i-2]; s != none && s-penaltyGap > word {
					word, wordAt = s-penaltyGap, i-2
				}
			}
			if lower[i] != p[j] {
				continue
			}
			bonus := boundary(t, i)
			base := scoreMatch + bonus
			if j == 0 {
				best[j][i] = base
				continue
			}
			prev, at := word, wordAt
			if bonus > 0 {
				prev, at = gap, gapAt
			}
			if i > 0 && best[j-1][i-1] != none && best[j-1][i-1]+bonusConsec >= prev {
				prev, at = best[j-1][i-1]+bonusConsec, i-1
			}
			if prev != none {
				best[j][i], from[j][i] = base+prev, at
			}
		}
	}

	end := -1
	for i := range n {
		if best[m-1][i] != none && (end < 0 || best[m-1][i] > best[m-1][end]) {
			end = i
		}
	}
	if end < 0 {
		return 0, nil, false
	}
	pos = make([]int, m)
	for j, i := m-1, end; j >= 0; j-- {
		pos[j] = i
		i = from[j][i]
	}
	return best[m-1][end], pos, true
}

// Word matches term as a run of text, ignoring case, where the run starts at
// a word boundary or lies inside a single word. It returns the rune
// positions of the first such run.
func Word(term, text string) (pos []int, ok bool) {
	p := []rune(strings.ToLower(term))
	t := []rune(text)
	if len(p) == 0 {
		return nil, true
	}
	for i := 0; i+len(p) <= len(t); i++ {
		if !runEqual(t[i:i+len(p)], p) {
			continue
		}
		if boundary(t, i) == 0 && !inWord(t[i:i+len(p)]) {
			continue
		}
		pos = make([]int, len(p))
		for j := range p {
			pos[j] = i + j
		}
		return pos, true
	}
	return nil, false
}

// Mode is how a field is matched.
type Mode int

// Matching modes.
const (
	ByWord Mode = iota // see Word
	Fuzzy              // see Match
	Exact              // the term is the whole text, ignoring case
)

// Field is one text a query is matched against.
type Field struct {
	Text string
	Mode Mode
}

// MatchFields reports whether every term of query matches at least one of
// the fields. Each term is credited to the first field it matches, so list
// the most important field first. It returns the matched rune positions in
// each field, sorted and without duplicates. An empty query matches.
func MatchFields(query string, fields ...Field) ([][]int, bool) {
	out := make([][]int, len(fields))
	for _, term := range Terms(query) {
		found := false
		for f, fd := range fields {
			pos, ok := fd.match(term)
			if ok {
				out[f] = append(out[f], pos...)
				found = true
				break
			}
		}
		if !found {
			return nil, false
		}
	}
	for f := range out {
		slices.Sort(out[f])
		out[f] = slices.Compact(out[f])
	}
	return out, true
}

func (fd Field) match(term string) ([]int, bool) {
	switch fd.Mode {
	case Fuzzy:
		_, pos, ok := Match(term, fd.Text)
		return pos, ok
	case Exact:
		if !strings.EqualFold(term, fd.Text) {
			return nil, false
		}
		pos := make([]int, utf8.RuneCountInString(fd.Text))
		for i := range pos {
			pos[i] = i
		}
		return pos, true
	}
	return Word(term, fd.Text)
}

// decay applies one more rune of gap to a running gap score.
func decay(score int) int {
	if score == none {
		return none
	}
	return score - penaltyGapStep
}

// boundary returns the bonus for a match at t[i]: the start of the text, a
// letter or digit after anything else, or an upper-case letter after a
// lower-case one (camelCase).
func boundary(t []rune, i int) int {
	if !isWordRune(t[i]) {
		return 0
	}
	if i == 0 || !isWordRune(t[i-1]) || (unicode.IsUpper(t[i]) && unicode.IsLower(t[i-1])) {
		return bonusBoundary
	}
	return 0
}

func isWordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

func inWord(rs []rune) bool {
	for _, r := range rs {
		if !isWordRune(r) {
			return false
		}
	}
	return true
}

func runEqual(t, p []rune) bool {
	for i := range p {
		if unicode.ToLower(t[i]) != p[i] {
			return false
		}
	}
	return true
}
