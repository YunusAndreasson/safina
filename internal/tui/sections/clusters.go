package sections

import (
	"strings"

	"github.com/charmbracelet/x/ansi"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/sahilm/fuzzy"

	"github.com/YunusAndreasson/safina/internal/config"
	"github.com/YunusAndreasson/safina/internal/tui/components"
	"github.com/YunusAndreasson/safina/internal/tui/textw"
	"github.com/YunusAndreasson/safina/internal/tui/theme"
	"github.com/YunusAndreasson/safina/internal/tui/tuictx"
)

// ClusterSwitcher is the Ctrl+K overlay listing kube contexts. Per the
// project rule "no default cluster", this lets the operator jump between
// contexts mid-session without popping to the tree root and re-drilling.
// On select the caller fires the same load path the tree's root drill
// would, so behavior is identical to picking from the cold-start picker.
type ClusterSwitcher struct {
	items  []config.ContextInfo
	input  components.SearchInput
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
	s.input.SetValue("")
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
	s.input.SetValue(q)
	s.cursor = 0
	s.recomputeMatches()
}

// UpdateQuery delegates editing to the shared Bubbles input and reranks only
// when its value changes; cursor movement does not reset result selection.
func (s *ClusterSwitcher) UpdateQuery(msg tea.Msg) tea.Cmd {
	cmd, changed := s.input.Update(msg)
	if changed {
		s.cursor = 0
		s.recomputeMatches()
	}
	return cmd
}

// Query returns the live query string.
func (s *ClusterSwitcher) Query() string { return s.input.Value() }

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
	if s.input.Value() == "" {
		for i := range s.items {
			s.matches = append(s.matches, i)
		}
		return
	}
	corpus := make([]string, len(s.items))
	for i, it := range s.items {
		corpus[i] = it.Name
	}
	// Names that contain what was typed come first. Fuzzy scores alone can
	// rank a long GKE-style name, matched letter by letter across its
	// separators, above the one EKS name that contains "eks".
	found := fuzzy.Find(s.input.Value(), corpus)
	q := strings.ToLower(s.input.Value())
	for _, literal := range []bool{true, false} {
		for _, m := range found {
			if strings.Contains(strings.ToLower(m.Str), q) == literal {
				s.matches = append(s.matches, m.Index)
			}
		}
	}
}

// View renders the centered overlay sized to the terminal. The backdrop is
// the standard modal dot pattern, dimming the main view behind it.
func (s *ClusterSwitcher) View(c *tuictx.Ctx, termW, termH int, activeContext string) string {
	return lipgloss.Place(termW, termH, lipgloss.Center, lipgloss.Center, s.Card(c, termW, termH, activeContext), components.ModalBackdrop(c.Theme)...)
}

// Card renders only the dialog; the application owns its layered backdrop.
func (s *ClusterSwitcher) Card(c *tuictx.Ctx, termW, termH int, activeContext string) string {
	t := c.Theme

	inner := max(1, min(70, termW-8))
	listH := max(1, min(12, termH-10))
	prompt := s.input.View(t, inner)
	stack := textw.JoinVertical(lipgloss.Left,
		t.CyanBold.Render("Switch cluster · ● current"),
		ansi.Truncate(prompt, inner, "…"), "",
		s.renderList(t, inner, listH, activeContext), "",
		components.RenderBoundedHints(t, []components.KeyDesc{{Key: "↑↓", Desc: "select"}, {Key: "↵", Desc: "switch"}, {Key: "esc", Desc: "cancel"}}, inner, components.KeyDesc{Key: "esc", Desc: "cancel"}))
	panel := t.Modal.Padding(1, 2).Width(inner + 6).Render(stack)
	return panel
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
