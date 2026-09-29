package todo

import (
	"regexp"
	"strings"
	"unicode"
)

// CheckLine is a Markdown checkbox line found in a body.
type CheckLine struct {
	LineNo  int    // index into the body's lines
	Ordinal int    // index among checkbox lines
	Text    string // text after the checkbox
	Checked bool
}

var checkboxRe = regexp.MustCompile(`^(\s*[-*+]\s+\[)([ xX])(\]\s+)(.*?)\s*$`)

// ParseCheckboxes returns the checkbox lines of body in order.
func ParseCheckboxes(body string) []CheckLine {
	var out []CheckLine
	for i, line := range strings.Split(body, "\n") {
		m := checkboxRe.FindStringSubmatch(line)
		if m == nil || strings.TrimSpace(m[4]) == "" {
			continue
		}
		out = append(out, CheckLine{
			LineNo:  i,
			Ordinal: len(out),
			Text:    m[4],
			Checked: m[2] != " ",
		})
	}
	return out
}

// setCheckboxLine rewrites line lineNo of body with new text and state.
func setCheckboxLine(body string, lineNo int, text string, checked bool) string {
	lines := strings.Split(body, "\n")
	if lineNo < 0 || lineNo >= len(lines) {
		return body
	}
	m := checkboxRe.FindStringSubmatch(lines[lineNo])
	if m == nil {
		return body
	}
	mark := " "
	if checked {
		mark = "x"
	}
	lines[lineNo] = m[1] + mark + m[3] + text
	return strings.Join(lines, "\n")
}

// removeLine deletes line lineNo from body.
func removeLine(body string, lineNo int) string {
	lines := strings.Split(body, "\n")
	if lineNo < 0 || lineNo >= len(lines) {
		return body
	}
	return strings.Join(append(lines[:lineNo], lines[lineNo+1:]...), "\n")
}

// MatchCheckboxes pairs checkbox lines with existing child items in order of
// confidence: exact text, then near match (normalised text or small edit
// distance), then position (the child's last known ordinal). It returns
// match[lineOrdinal] = child index, or -1 for unmatched lines.
func MatchCheckboxes(lines []CheckLine, children []*Item) []int {
	match := make([]int, len(lines))
	for i := range match {
		match[i] = -1
	}
	used := make([]bool, len(children))

	// 1. Exact text.
	for i, l := range lines {
		for j, c := range children {
			if !used[j] && c.Title == l.Text {
				match[i], used[j] = j, true
				break
			}
		}
	}
	// 2. Near match: normalised equality scores 0; otherwise edit distance
	// within a threshold. Best-scoring pairs are taken first.
	for {
		bi, bj, best := -1, -1, 1<<30
		for i, l := range lines {
			if match[i] >= 0 {
				continue
			}
			nl := normalise(l.Text)
			for j, c := range children {
				if used[j] {
					continue
				}
				nc := normalise(c.Title)
				d := 0
				if nl != nc {
					d = levenshtein(nl, nc)
					if d > nearThreshold(nl, nc) {
						continue
					}
				}
				if d < best {
					bi, bj, best = i, j, d
				}
			}
		}
		if bi < 0 {
			break
		}
		match[bi], used[bj] = bj, true
	}
	// 3. Position.
	for i, l := range lines {
		if match[i] >= 0 {
			continue
		}
		for j, c := range children {
			if !used[j] && c.Line == l.Ordinal {
				match[i], used[j] = j, true
				break
			}
		}
	}
	return match
}

func nearThreshold(a, b string) int {
	n := max(len([]rune(a)), len([]rune(b)))
	return max(2, n/5)
}

func normalise(s string) string {
	var b strings.Builder
	space := false
	for _, r := range strings.ToLower(s) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			space = false
			b.WriteRune(r)
		default:
			space = true
		}
	}
	return b.String()
}

func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}
