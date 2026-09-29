package ui

import (
	"image/color"

	"charm.land/lipgloss/v2"

	"github.com/relloyd/todotil/internal/config"
	"github.com/relloyd/todotil/internal/todo"
)

// styles are derived from the configured palette.
type styles struct {
	pal config.Palette

	text, muted, subtle, accent, link, warn, errorS, done      lipgloss.Style
	title, tabActive, tabIdle, dayHeader, rule, key, badgeBase lipgloss.Style
	dialog, dialogTitle                                        lipgloss.Style

	selBg, dialogBg color.Color
}

func newStyles(p config.Palette) styles {
	c := lipgloss.Color
	s := styles{pal: p, selBg: c(p.Selection), dialogBg: c(p.Dialog)}
	s.text = lipgloss.NewStyle().Foreground(c(p.Text))
	s.muted = lipgloss.NewStyle().Foreground(c(p.Muted))
	s.subtle = lipgloss.NewStyle().Foreground(c(p.Subtle))
	s.accent = lipgloss.NewStyle().Foreground(c(p.Accent)).Bold(true)
	s.link = lipgloss.NewStyle().Foreground(c(p.Link)).Underline(true)
	s.warn = lipgloss.NewStyle().Foreground(c(p.Warn))
	s.errorS = lipgloss.NewStyle().Foreground(c(p.Error)).Bold(true)
	s.done = lipgloss.NewStyle().Foreground(c(p.Done))
	s.title = lipgloss.NewStyle().Foreground(c(p.AccentAlt)).Bold(true)
	s.tabActive = lipgloss.NewStyle().Foreground(c(p.Dialog)).Background(c(p.Accent)).Bold(true).Padding(0, 1)
	s.tabIdle = lipgloss.NewStyle().Foreground(c(p.Muted)).Padding(0, 1)
	s.dayHeader = lipgloss.NewStyle().Foreground(c(p.Accent)).Bold(true)
	s.rule = lipgloss.NewStyle().Foreground(c(p.Subtle))
	s.key = lipgloss.NewStyle().Foreground(c(p.AccentAlt)).Bold(true)
	s.badgeBase = lipgloss.NewStyle().Foreground(c(p.Dialog)).Bold(true).Padding(0, 1)
	s.dialog = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(c(p.Accent)).
		Background(c(p.Dialog)).
		Padding(0, 1)
	s.dialogTitle = lipgloss.NewStyle().Foreground(c(p.Accent)).Background(c(p.Dialog)).Bold(true)
	return s
}

// stateColor returns the colour associated with a state.
func (s styles) stateColor(st todo.State) color.Color {
	switch st {
	case todo.Now:
		return lipgloss.Color(s.pal.Now)
	case todo.Next:
		return lipgloss.Color(s.pal.Next)
	case todo.Later:
		return lipgloss.Color(s.pal.Later)
	}
	return lipgloss.Color(s.pal.Journal)
}

// badge renders a state label on a coloured background.
func (s styles) badge(st todo.State) string {
	return s.badgeBase.Background(s.stateColor(st)).Render(st.Label())
}
