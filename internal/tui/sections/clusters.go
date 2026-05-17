package sections

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/sahilm/fuzzy"

	"github.com/yunus/safina/internal/config"
	"github.com/yunus/safina/internal/tui/components"
	"github.com/yunus/safina/internal/tui/theme"
	"github.com/yunus/safina/internal/tui/tuictx"
)

// ClusterSwitcher is the Ctrl+K overlay listing kube contexts. Per the
// project rule "no default cluster", this lets the operator jump between
// contexts mid-session without popping to the tree root and re-drilling.
// On select the caller fires the same load path the tree's root drill
// would, so behavior is identical to picking from the cold-start picker.
type ClusterSwitcher struct {
	items  []config.ContextInfo
	query  string
	cursor int

	// matches caches the filtered index slice between recomputeMatches and
	// View — we don't need full fuzzy.Matches here because the corpus is
	// small (kube contexts rarely number in the hundreds).
	matches []int
}

// NewClusterSwitcher builds an empty switcher; populate via Open().
func NewClusterSwitcher() ClusterSwitcher {
	return ClusterSwitcher{}
}

// Open seeds the switcher with the available context list and resets the
// query / cursor. Called on Ctrl+K.
func (s *ClusterSwitcher) Open(items []config.ContextInfo, activeContext string) {
	s.items = items
	s.query = ""
	s.cursor = 0
	// Position cursor on the active context so the operator immediately sees
	// "you are here". This is the one place the app shows a current-context
	// dot — the memory rule says no last-used-default at startup, but mid-
	// session we do want a "current cluster" indicator.
	for i, it := range items {
		if it.Name == activeContext {
			s.cursor = i
			break
		}
	}
	s.recomputeMatches()
}

// SetQuery updates the fuzzy filter; cursor resets to first match.
func (s *ClusterSwitcher) SetQuery(q string) {
	s.query = q
	s.cursor = 0
	s.recomputeMatches()
}

// Query returns the live query string.
func (s *ClusterSwitcher) Query() string { return s.query }

// CursorUp / CursorDown move within the filtered list, clamped.
func (s *ClusterSwitcher) CursorUp() {
	if s.cursor > 0 {
		s.cursor--
	}
}

func (s *ClusterSwitcher) CursorDown() {
	if s.cursor < len(s.matches)-1 {
		s.cursor++
	}
}

// Selected returns the currently-highlighted context, ok=false if list is
// empty. Caller fires a drill into this context on Enter.
func (s *ClusterSwitcher) Selected() (config.ContextInfo, bool) {
	if s.cursor < 0 || s.cursor >= len(s.matches) {
		return config.ContextInfo{}, false
	}
	idx := s.matches[s.cursor]
	if idx < 0 || idx >= len(s.items) {
		return config.ContextInfo{}, false
	}
	return s.items[idx], true
}

func (s *ClusterSwitcher) recomputeMatches() {
	s.matches = s.matches[:0]
	if s.query == "" {
		for i := range s.items {
			s.matches = append(s.matches, i)
		}
		return
	}
	corpus := make([]string, len(s.items))
	for i, it := range s.items {
		corpus[i] = it.Name
	}
	for _, m := range fuzzy.Find(s.query, corpus) {
		s.matches = append(s.matches, m.Index)
	}
}

// View renders the centered overlay sized to the terminal. The backdrop is
// the standard modal dot pattern, dimming the main view behind it.
func (s *ClusterSwitcher) View(c *tuictx.Ctx, termW, termH int, activeContext string) string {
	t := c.Theme

	// φ-sized overlay: ~62% of width, capped at 80 cols. Height grows with
	// the list up to ~62% of available rows.
	w := termW * 62 / 100
	if w > 80 {
		w = 80
	}
	if w < 40 {
		w = 40
	}
	rowsAvail := len(s.matches)
	if rowsAvail < 3 {
		rowsAvail = 3
	}
	h := rowsAvail + 6 // prompt + 2 dividers + spacer + 2 borders
	maxH := termH * 62 / 100
	if h > maxH {
		h = maxH
	}
	if h < 10 {
		h = 10
	}

	inner := components.PanelContentWidth(w)
	title := t.CyanBold.Render("switch cluster")
	hint := t.DimTxt.Italic(true).Render("type to filter · ↑↓ select · ↵ switch · esc cancel")

	prompt := t.FilterPrompt.Render("⎈ ") + t.FilterText.Render(s.query) + t.FilterPrompt.Render("▌")
	divider := lipgloss.NewStyle().Foreground(t.P.Dim).Render(strings.Repeat("─", inner))

	listH := h - 6 // prompt + 2 dividers + 2 borders + hint
	if listH < 3 {
		listH = 3
	}
	list := s.renderList(t, inner, listH, activeContext)

	stack := lipgloss.JoinVertical(lipgloss.Left,
		title, "",
		prompt, divider,
		list, divider,
		hint,
	)
	panel := components.PanelWithAccent(t, "", w, h, stack, true, t.P.Pink, t.P.Cyan)
	return lipgloss.Place(termW, termH, lipgloss.Center, lipgloss.Center, panel,
		components.ModalBackdrop(t)...)
}

func (s *ClusterSwitcher) renderList(t *theme.Theme, w, rows int, activeContext string) string {
	if len(s.matches) == 0 {
		box := lipgloss.NewStyle().Width(w).Height(rows).Align(lipgloss.Center, lipgloss.Center)
		return box.Render(t.Idle.Render("no matching cluster"))
	}
	start, end := components.VisibleRange(s.cursor, len(s.matches), rows)
	out := make([]string, 0, end-start)
	for i := start; i < end; i++ {
		idx := s.matches[i]
		if idx < 0 || idx >= len(s.items) {
			continue
		}
		it := s.items[idx]
		selected := i == s.cursor
		dot := "  "
		if it.Name == activeContext {
			dot = t.GreenTxt.Render("● ")
		}
		bg := t.RowBgFor(i, selected)
		name := bg.Foreground(t.P.FG).Bold(selected).Render(components.TruncPad(it.Name, w-4))
		out = append(out, bg.Render(dot)+name)
	}
	for len(out) < rows {
		out = append(out, lipgloss.NewStyle().Width(w).Render(""))
	}
	return strings.Join(out, "\n")
}
