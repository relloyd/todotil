package ui

import (
	"regexp"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/relloyd/todotil/internal/config"
	"github.com/relloyd/todotil/internal/todo"
)

// detailState is the full view of one item.
type detailState struct {
	id      string
	lines   []string
	targets map[int]string // line → item to jump to when clicked
	offset  int
}

const dateLayout = "Mon 2 Jan 2006 15:04"

func (m *Model) openDetail(id string) {
	m.detail = detailState{id: id}
	m.mode = modeDetail
	m.buildDetail()
}

var (
	bodyCheckRe  = regexp.MustCompile(`^(\s*)[-*+]\s+\[([ xX])\]\s+(.*)$`)
	bodyBulletRe = regexp.MustCompile(`^(\s*)[-*+]\s+(.*)$`)
	bodyHeadRe   = regexp.MustCompile(`^#{1,6}\s+(.*)$`)
)

func (m *Model) buildDetail() {
	b := m.board()
	it := b.Get(m.detail.id)
	if it == nil {
		m.mode = modeList
		return
	}
	w := max(10, m.width-4)
	d := &m.detail
	d.lines, d.targets = nil, map[int]string{}
	add := func(s string, target string) {
		for _, l := range strings.Split(ansi.Wrap(s, w, ""), "\n") {
			if target != "" {
				d.targets[len(d.lines)] = target
			}
			d.lines = append(d.lines, l)
		}
	}

	add(renderSegs(m.textSegs(it.Title, m.st.text.Bold(true)), nil), "")
	meta := []string{}
	switch {
	case it.Done():
		meta = append(meta, m.st.badgeBase.Background(lipgloss.Color(m.Palette.Done)).Render("Done"))
	default:
		meta = append(meta, m.st.badge(it.State))
	}
	created := "created " + it.Created.Local().Format(dateLayout)
	if it.CreatedBy != "" {
		created += " by " + it.CreatedBy
	}
	meta = append(meta, m.st.muted.Render(created))
	if it.Completed != nil {
		done := "completed " + it.Completed.Local().Format(dateLayout)
		if it.CompletedBy != "" {
			done += " by " + it.CompletedBy
		}
		meta = append(meta, m.st.done.Render(done))
	}
	add(strings.Join(meta, m.st.subtle.Render(" · ")), "")
	if c := it.ActiveClaim(); c != nil {
		add(m.st.agent.Render("● claimed by "+c.Assignee+" · "+age(m.Now().Sub(c.At))+" ago · "+c.ID)+
			m.st.subtle.Render("  ("+m.Keys.First(config.Unassign)+" to unassign)"), "")
	}
	if p := b.Get(it.Parent); p != nil {
		add(m.st.muted.Render("↑ parent: ")+m.st.accent.Render(clean(p.Title))+m.st.subtle.Render("  ("+m.Keys.First(config.JumpParent)+")"), p.ID)
	}
	add("", "")
	if it.Body == "" {
		add(m.st.subtle.Render("No body. Press "+m.Keys.First(config.Edit)+" to add one."), "")
	}
	for _, line := range strings.Split(it.Body, "\n") {
		add(m.bodyLine(line), "")
	}
	if kids := b.Descendants(it.ID); len(kids) > 0 {
		add("", "")
		add(m.st.dayHeader.Render("Children")+" "+m.st.rule.Render(strings.Repeat("─", max(0, w-9))), "")
		depth := map[string]int{it.ID: -1}
		for _, k := range kids {
			depth[k.ID] = depth[k.Parent] + 1
			box, st := "☐ ", m.st.text
			switch {
			case k.Done():
				box, st = "☑ ", m.st.muted
			case k.State == todo.Journal:
				box = "✎ "
			}
			where := k.State.Label()
			if k.Done() {
				where = "done"
			}
			line := strings.Repeat("  ", depth[k.ID]) + m.st.done.Render(box) +
				renderSegs(m.textSegs(k.Title, st), nil) + "  " +
				lipgloss.NewStyle().Foreground(m.st.stateColor(k.State)).Render(strings.ToLower(where))
			if c := k.ActiveClaim(); c != nil {
				line += "  " + m.st.agent.Render("@"+c.Assignee)
			}
			add(line, k.ID)
		}
	}
	if len(it.Notes) > 0 {
		add("", "")
		add(m.st.dayHeader.Render("Notes")+" "+m.st.rule.Render(strings.Repeat("─", max(0, w-6))), "")
		for _, n := range it.Notes {
			who := n.By
			if who == "" {
				who = "you"
			}
			add(m.st.muted.Render(n.At.Local().Format("2 Jan 15:04")+"  ")+m.st.agent.Render(who)+"  "+
				renderSegs(m.textSegs(n.Text, m.st.text), nil), "")
		}
	}
	if len(it.Claims) > 0 {
		add("", "")
		add(m.st.dayHeader.Render("Claims")+" "+m.st.rule.Render(strings.Repeat("─", max(0, w-7))), "")
		for _, c := range it.Claims {
			line := m.st.agent.Render(c.Assignee) + m.st.muted.Render("  "+c.At.Local().Format("2 Jan 15:04"))
			if c.Ended != nil {
				end := string(c.End)
				if c.Reason != "" {
					end += ": " + c.Reason
				}
				line += m.st.muted.Render(" → " + end + " " + c.Ended.Local().Format("2 Jan 15:04"))
			} else {
				line += m.st.agent.Render(" → active")
			}
			add(line+m.st.subtle.Render("  "+c.ID), "")
		}
	}
	d.offset = min(d.offset, max(0, len(d.lines)-m.contentHeight()))
}

// bodyLine lightly renders a Markdown line: checkboxes, bullets, headings
// and links.
func (m *Model) bodyLine(line string) string {
	if mm := bodyCheckRe.FindStringSubmatch(line); mm != nil {
		if mm[2] == " " {
			return mm[1] + m.st.done.Render("☐ ") + renderSegs(m.textSegs(mm[3], m.st.text), nil)
		}
		return mm[1] + m.st.done.Render("☑ ") + renderSegs(m.textSegs(mm[3], m.st.muted), nil)
	}
	if mm := bodyHeadRe.FindStringSubmatch(line); mm != nil {
		return renderSegs(m.textSegs(mm[1], m.st.accent), nil)
	}
	if mm := bodyBulletRe.FindStringSubmatch(line); mm != nil {
		return mm[1] + m.st.muted.Render("• ") + renderSegs(m.textSegs(mm[2], m.st.text), nil)
	}
	return renderSegs(m.textSegs(line, m.st.text), nil)
}

func (m *Model) detailView() string {
	d := m.detail
	h := m.contentHeight()
	end := min(len(d.lines), d.offset+h)
	out := make([]string, 0, h)
	for _, l := range d.lines[d.offset:end] {
		out = append(out, "  "+l)
	}
	return strings.Join(out, "\n")
}

func (m *Model) scrollDetail(n int) {
	d := &m.detail
	d.offset = min(max(0, d.offset+n), max(0, len(d.lines)-m.contentHeight()))
}

func (m *Model) detailKey(msg tea.KeyPressMsg) tea.Cmd {
	k := func(a config.Action) bool { return m.keyMatches(a, msg) }
	it := m.board().Get(m.detail.id)
	h := m.contentHeight()
	switch {
	case msg.String() == "esc" || msg.String() == "backspace" || msg.String() == "left":
		m.mode = modeList
		if i := m.rowIndex(m.tab, m.detail.id); i >= 0 {
			m.cursor[m.tab] = i
			m.ensureVisible(m.tab)
		}
	case k(config.Down):
		m.scrollDetail(1)
	case k(config.Up):
		m.scrollDetail(-1)
	case k(config.HalfDown):
		m.scrollDetail(h / 2)
	case k(config.HalfUp):
		m.scrollDetail(-h / 2)
	case k(config.PageDown):
		m.scrollDetail(h)
	case k(config.PageUp):
		m.scrollDetail(-h)
	case k(config.Top):
		m.detail.offset = 0
	case k(config.Bottom):
		m.scrollDetail(len(m.detail.lines))
	case it == nil:
	case k(config.Edit):
		return m.openEdit(it.ID)
	case k(config.Done):
		return m.toggleDone(it)
	case k(config.Move):
		m.pendingMove = true
	case k(config.Delete):
		return m.askDelete(it)
	case k(config.Copy):
		return m.copyItem(it)
	case k(config.Unassign):
		return m.unassign(it)
	case k(config.AddChild):
		return m.openAdd(it.ID, "")
	}
	return nil
}
