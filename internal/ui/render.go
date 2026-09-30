package ui

import (
	"fmt"
	"image/color"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/relloyd/todotil/internal/config"
	"github.com/relloyd/todotil/internal/links"
	"github.com/relloyd/todotil/internal/todo"
)

// seg is a run of text with one style, optionally hyperlinked.
type seg struct {
	text string
	st   lipgloss.Style
	link string
}

func segsWidth(segs []seg) int {
	w := 0
	for _, s := range segs {
		w += ansi.StringWidth(s.text)
	}
	return w
}

// truncateSegs cuts segs to width columns, ending with an ellipsis.
func truncateSegs(segs []seg, width int) []seg {
	if segsWidth(segs) <= width {
		return segs
	}
	var out []seg
	used := 0
	for _, s := range segs {
		w := ansi.StringWidth(s.text)
		if used+w < width {
			out = append(out, s)
			used += w
			continue
		}
		s.text = ansi.Truncate(s.text, max(0, width-used), "…")
		if width-used >= 1 && s.text == "" {
			s.text = "…"
		}
		out = append(out, s)
		break
	}
	return out
}

// renderSegs styles segs, applying bg (if set) to every segment.
func renderSegs(segs []seg, bg color.Color) string {
	var b strings.Builder
	for _, s := range segs {
		st := s.st
		if bg != nil {
			st = st.Background(bg)
		}
		if s.link != "" {
			st = st.Hyperlink(s.link)
		}
		b.WriteString(st.Render(s.text))
	}
	return b.String()
}

// renderRow lays out left and right segments across width, truncating the
// left side as needed.
func renderRow(left, right []seg, width int, bg color.Color) string {
	rw := segsWidth(right)
	avail := width - rw
	if rw > 0 {
		avail--
	}
	if avail < 8 {
		right, rw, avail = nil, 0, width
	}
	left = truncateSegs(left, avail)
	gap := width - segsWidth(left) - rw
	pad := lipgloss.NewStyle()
	if bg != nil {
		pad = pad.Background(bg)
	}
	return renderSegs(left, bg) + pad.Render(strings.Repeat(" ", max(0, gap))) + renderSegs(right, bg)
}

// clean makes text safe for a single line.
func clean(s string) string {
	s = strings.ReplaceAll(s, "\t", "  ")
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
}

// textSegs splits text into plain and link segments. With link shortening
// on, links show their Markdown label or fetched page title.
func (m *Model) textSegs(text string, st lipgloss.Style) []seg {
	text = clean(text)
	spans := links.Find(text)
	if len(spans) == 0 {
		return []seg{{text: text, st: st}}
	}
	var out []seg
	prev := 0
	for _, sp := range spans {
		if sp.Start > prev {
			out = append(out, seg{text: text[prev:sp.Start], st: st})
		}
		out = append(out, seg{text: m.linkLabel(sp), st: m.st.link, link: sp.URL})
		prev = sp.End
	}
	if prev < len(text) {
		out = append(out, seg{text: text[prev:], st: st})
	}
	return out
}

func (m *Model) linkLabel(sp links.Span) string {
	if !m.Settings.ShortenLinks {
		return spanSource(sp)
	}
	if sp.Label != "" {
		return sp.Label
	}
	if t, ok := m.board().LinkTitle(sp.URL); ok && t != "" {
		return t
	}
	return sp.URL
}

// spanSource returns the original text of a span.
func spanSource(sp links.Span) string {
	if sp.Label != "" {
		return "[" + sp.Label + "](" + sp.URL + ")"
	}
	return sp.URL
}

func (m *Model) headerTabs() (string, []int) {
	var b strings.Builder
	title := m.st.title.Render(" ✓ todotil ")
	b.WriteString(title)
	b.WriteString(" ")
	x := lipgloss.Width(title) + 1
	edges := []int{}
	for t := range numTabs {
		label := t.label()
		if t != tabHistory {
			label = fmt.Sprintf("%s %d", label, countItems(m.rows[t]))
		}
		label = fmt.Sprintf("%d %s", int(t)+1, label)
		st := m.st.tabIdle
		if t == m.tab && (m.mode == modeList || m.mode == modeDetail) {
			st = m.st.tabActive
		}
		s := st.Render(label)
		b.WriteString(s)
		edges = append(edges, x)
		x += lipgloss.Width(s)
	}
	edges = append(edges, x)
	return b.String(), edges
}

// tabAt returns the tab at column x of the header, or -1.
func (m *Model) tabAt(x int) tab {
	_, edges := m.headerTabs()
	for i := range int(numTabs) {
		if x >= edges[i] && x < edges[i+1] {
			return tab(i)
		}
	}
	return -1
}

func countItems(rows []todo.Row) int {
	n := 0
	for _, r := range rows {
		if r.Kind == todo.RowItem {
			n++
		}
	}
	return n
}

func (m *Model) headerView() string {
	left, _ := m.headerTabs()
	var right string
	switch {
	case m.selectMode:
		right = m.st.warn.Render("SELECT MODE ")
	case m.tab == tabHistory && m.mode == modeList:
		dir := "newest first"
		if !m.Settings.History.Descending {
			dir = "oldest first"
		}
		right = m.st.muted.Render(fmt.Sprintf("by %s, %s ", m.Settings.History.Sort, dir))
	default:
		right = m.st.muted.Render(m.Keys.First(config.Help) + " help  " + m.Keys.First(config.OpenSettings) + " settings ")
	}
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		return ansi.Truncate(left, m.width, "")
	}
	return left + strings.Repeat(" ", gap) + right
}

const (
	gutterWidth = 2
	markerWidth = 2
	ctxMaxLen   = 28
	ctxSep      = " › "
)

// contextSpan returns the columns covered by a row's parent context
// prefix, used for mouse hit testing.
func (m *Model) contextSpan(r todo.Row) (int, int, bool) {
	if r.Context == "" {
		return 0, 0, false
	}
	x0 := gutterWidth + 2*r.Depth + markerWidth
	return x0, x0 + ansi.StringWidth(m.contextText(r.Context)+ctxSep), true
}

func (m *Model) contextText(s string) string {
	return ansi.Truncate(clean(s), ctxMaxLen, "…")
}

func (m *Model) marker(it *todo.Item, depth int, history bool) seg {
	switch {
	case it.Done():
		return seg{text: "✓ ", st: m.st.done}
	case it.Rejected():
		return seg{text: "× ", st: m.st.errorS}
	case it.State == todo.Journal:
		return seg{text: "✎ ", st: lipgloss.NewStyle().Foreground(m.st.stateColor(todo.Journal))}
	case history:
		return seg{text: "● ", st: lipgloss.NewStyle().Foreground(m.st.stateColor(it.State))}
	case depth > 0:
		return seg{text: "• ", st: m.st.muted}
	}
	return seg{text: "○ ", st: lipgloss.NewStyle().Foreground(m.st.stateColor(it.State))}
}

func (m *Model) itemLine(r todo.Row, selected, history bool) string {
	it := r.Item
	var bg color.Color
	gutter := seg{text: "  ", st: m.st.text}
	if selected {
		bg = m.st.selBg
		gutter = seg{text: "▌ ", st: m.st.accent}
	}
	left := []seg{gutter, {text: strings.Repeat("  ", r.Depth), st: m.st.text}, m.marker(it, r.Depth, history)}
	if r.Context != "" {
		left = append(left, seg{text: m.contextText(r.Context) + ctxSep, st: m.st.muted})
	}
	titleSt := m.st.text
	if it.Done() || it.Rejected() {
		titleSt = m.st.muted
	} else if r.Depth == 0 && !history {
		titleSt = titleSt.Bold(true)
	}
	if it.CreatedBy != "" {
		left = append(left, seg{text: agentMark, st: m.st.agent})
	}
	left = append(left, m.textSegs(it.Title, titleSt)...)
	if it.Body != "" || len(it.Notes) > 0 {
		left = append(left, seg{text: " ≡", st: m.st.subtle})
	}
	var right []seg
	if c := it.ActiveClaim(); c != nil {
		right = append(right, m.claimBadge(c), seg{text: "  ", st: m.st.text})
	}
	if history {
		right = append(right, m.historyMeta(it)...)
	} else {
		right = append(right, seg{text: age(m.Now().Sub(it.Created)) + " ", st: m.st.subtle})
	}
	return renderRow(left, right, m.width, bg)
}

// agentMark flags items an agent created.
const agentMark = "✦ "

// claimBadge shows who holds an item and for how long, e.g. "@claude-1 2h".
func (m *Model) claimBadge(c *todo.Claim) seg {
	return seg{text: "@" + c.Assignee + " " + age(m.Now().Sub(c.At)), st: m.st.agent}
}

func (m *Model) historyMeta(it *todo.Item) []seg {
	var out []seg
	if it.Open() {
		out = append(out, seg{text: strings.ToLower(it.State.Label()) + "  ", st: lipgloss.NewStyle().Foreground(m.st.stateColor(it.State))})
	}
	byOutcome := m.Settings.History.Sort == string(todo.SortCompleted)
	created := "created " + it.Created.Local().Format("15:04")
	if byOutcome {
		created = "created " + it.Created.Local().Format("2 Jan 2006 15:04")
	}
	out = append(out, seg{text: created, st: m.st.muted})
	if o, ok := it.Outcome(); ok {
		text := string(o.Kind) + " "
		if o.By != "" {
			text += "by " + o.By + " "
		}
		// Grouped by outcome day, the date is in the header already.
		if byOutcome {
			text += o.At.Local().Format("15:04")
		} else {
			text += o.At.Local().Format("2 Jan 2006 15:04")
		}
		st := m.st.done
		if o.Kind == todo.OutcomeRejected {
			st = m.st.errorS
		}
		out = append(out, seg{text: " · ", st: m.st.subtle}, seg{text: text, st: st})
	}
	return append(out, seg{text: " ", st: m.st.muted})
}

// age formats a duration compactly: 5m, 3h, 2d, 3w, 4mo, 1y.
func age(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "<1m" // not "now", which reads as the state
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 14*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	case d < 60*24*time.Hour:
		return fmt.Sprintf("%dw", int(d.Hours()/24/7))
	case d < 365*24*time.Hour:
		return fmt.Sprintf("%dmo", int(d.Hours()/24/30))
	}
	return fmt.Sprintf("%dy", int(d.Hours()/24/365))
}

// dayLabel names a History day group. The zero day holds items with no
// outcome date: open items and journal notes, or only journal notes when
// that filter is on.
func dayLabel(day, now time.Time, filter todo.HistoryFilter) string {
	if day.IsZero() {
		if filter == todo.HistoryJournal {
			return "Journal notes"
		}
		return "Open and journal notes"
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	switch {
	case day.Equal(today):
		return "Today · " + day.Format("Monday 2 January 2006")
	case day.Equal(today.AddDate(0, 0, -1)):
		return "Yesterday · " + day.Format("Monday 2 January 2006")
	}
	return day.Format("Monday 2 January 2006")
}

func (m *Model) listView() string {
	t := m.tab
	rows, lines := m.rows[t], m.lines[t]
	if len(rows) == 0 {
		msg := fmt.Sprintf("Nothing in %s yet. Press %s to add an entry.", t.label(), m.Keys.First(config.Add))
		return lipgloss.Place(m.width, m.contentHeight(), lipgloss.Center, lipgloss.Center, m.st.muted.Render(msg))
	}
	history := t == tabHistory
	h := m.contentHeight()
	out := make([]string, 0, h)
	for i := m.offset[t]; i < len(lines) && len(out) < h; i++ {
		l := lines[i]
		if l.row < 0 {
			if history {
				out = append(out, "")
			} else {
				out = append(out, m.st.rule.Render("  "+strings.Repeat("─", max(0, m.width-4))))
			}
			continue
		}
		r := rows[l.row]
		if r.Kind == todo.RowDay {
			label := dayLabel(r.Day, m.Now(), m.historyFilter)
			rule := strings.Repeat("─", max(0, m.width-ansi.StringWidth(label)-5))
			out = append(out, " "+m.st.dayHeader.Render(label)+" "+m.st.rule.Render(rule))
			continue
		}
		out = append(out, m.itemLine(r, l.row == m.cursor[t], history))
	}
	return strings.Join(out, "\n")
}

func (m *Model) statusView() string {
	w := m.width
	f := m.currentFilter()
	switch {
	case f != nil && (f.editing || (m.status == "" && f.active())):
		return m.filterView(f)
	case m.confirm != nil:
		return ansi.Truncate(m.st.warn.Render(" "+m.confirm.prompt+" ")+m.st.key.Render("y")+m.st.muted.Render("/")+m.st.key.Render("n"), w, "…")
	case m.pendingMove:
		parts := []string{m.st.accent.Render(" Move to:")}
		for _, a := range []struct {
			act config.Action
			st  todo.State
		}{{config.MoveNow, todo.Now}, {config.MoveNext, todo.Next}, {config.MoveLater, todo.Later}, {config.MoveJournal, todo.Journal}} {
			parts = append(parts, m.st.key.Render(m.Keys.First(a.act))+" "+lipgloss.NewStyle().Foreground(m.st.stateColor(a.st)).Render(a.st.Label()))
		}
		parts = append(parts, m.st.muted.Render("esc cancel"))
		return ansi.Truncate(strings.Join(parts, "  "), w, "…")
	case m.status != "":
		st := m.st.text
		switch m.statusKind {
		case statusWarn:
			st = m.st.warn
		case statusError:
			st = m.st.errorS
		}
		return ansi.Truncate(st.Render(" "+m.status), w, "…")
	}
	return ""
}

func (m *Model) hint(a config.Action, desc string) string {
	return m.st.key.Render(m.Keys.First(a)) + " " + m.st.muted.Render(desc)
}

// overlayEscHint says what esc does in settings or help: clear the filter
// first, then close.
func (m *Model) overlayEscHint() string {
	if f := m.currentFilter(); f != nil && f.active() {
		return m.st.key.Render("esc") + m.st.muted.Render(" clear filter")
	}
	return m.st.key.Render("esc") + m.st.muted.Render(" close")
}

func (m *Model) hintsView() string {
	var hs []string
	switch {
	case m.editor != nil:
		return ""
	case m.currentFilter() != nil && m.currentFilter().editing:
		move := " select"
		if m.mode == modeHelp {
			move = " scroll"
		}
		hs = []string{m.st.key.Render("↑↓") + m.st.muted.Render(move), m.st.key.Render("enter") + m.st.muted.Render(" keep filter"),
			m.st.key.Render("esc") + m.st.muted.Render(" clear")}
	case m.mode == modeSettings:
		hs = []string{m.st.key.Render("↑↓") + m.st.muted.Render(" select"), m.st.key.Render("enter/←→") + m.st.muted.Render(" change"),
			m.st.key.Render("backspace") + m.st.muted.Render(" reset key"), m.hint(config.Filter, "filter"), m.overlayEscHint()}
	case m.mode == modeHelp:
		hs = []string{m.st.key.Render("↑↓") + m.st.muted.Render(" scroll"), m.hint(config.Filter, "filter"), m.overlayEscHint()}
	case m.mode == modeDetail:
		hs = []string{m.st.key.Render("esc") + m.st.muted.Render(" back"), m.hint(config.Edit, "edit"), m.hint(config.Done, "done")}
		if it := m.board().Get(m.detail.id); it != nil && it.Open() {
			hs = append(hs, m.hint(config.Reject, "reject"))
		}
		hs = append(hs, m.hint(config.Move, "move"), m.hint(config.JumpParent, "parent"), m.hint(config.JumpChild, "child"),
			m.hint(config.JumpBack, "back"), m.hint(config.Copy, "copy"), m.hint(config.CopyID, "copy id"),
			m.hint(config.SelectMode, "select"))
	default:
		hs = []string{m.hint(config.Add, "add"), m.hint(config.Edit, "edit"), m.hint(config.Open, "open"),
			m.hint(config.Done, "done")}
		if it := m.selected(); it != nil && it.Open() {
			hs = append(hs, m.hint(config.Reject, "reject"))
		}
		hs = append(hs, m.hint(config.Move, "move"), m.hint(config.Indent, "indent"), m.hint(config.Undo, "undo"))
		if m.tab == tabHistory {
			hs = append(hs, m.hint(config.HistoryFilter, "filter: "+m.historyFilter.Label()),
				m.hint(config.SortKey, "sort"), m.hint(config.SortDir, "reverse"))
		} else {
			hs = append(hs, m.st.key.Render(m.Keys.First(config.ItemUp)+"/"+m.Keys.First(config.ItemDown))+" "+m.st.muted.Render("reorder"))
		}
		hs = append(hs, m.hint(config.Help, "more"))
	}
	var b strings.Builder
	b.WriteString(" ")
	for i, h := range hs {
		part := h
		if i > 0 {
			part = m.st.subtle.Render(" · ") + h
		}
		if lipgloss.Width(b.String())+lipgloss.Width(part) > m.width {
			break
		}
		b.WriteString(part)
	}
	return b.String()
}
