package sections

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/yunus/safina/internal/tui/components"
	"github.com/yunus/safina/internal/tui/theme"
	"github.com/yunus/safina/internal/tui/tuictx"
)

// Describe is the YAML-dump modal — kubectl get -o yaml in a navigable view.
// Smart-case filter narrows AND highlights matches inline so an SRE can
// scan a 300-line manifest for "image:" or "annotations" without grepping.
type Describe struct {
	kind      string
	name      string
	namespace string

	yaml     string
	lines    []string
	filtered []int

	vp     viewport.Model
	filter components.Filter

	loading  bool
	err      error
	revealed bool // true when this is a Secret reveal — adds a "decoded" cue

	theme *theme.Theme

	modalW int
	modalH int
}

// NewDescribe constructs the describe modal.
func NewDescribe(c *tuictx.Ctx) Describe {
	vp := viewport.New()
	vp.MouseWheelEnabled = true
	return Describe{
		vp:     vp,
		filter: components.NewFilter(c.Theme),
		theme:  c.Theme,
	}
}

// Reset prepares for a new resource. Caller follows with LoadDescribe + SetYAML.
func (s *Describe) Reset(kind, name, namespace string) {
	s.kind = kind
	s.name = name
	s.namespace = namespace
	s.yaml = ""
	s.lines = nil
	s.filtered = s.filtered[:0]
	s.filter.Clear()
	s.filter.Close()
	s.loading = true
	s.err = nil
	s.revealed = false
	s.vp.SetContent("")
	s.vp.GotoTop()
}

// MarkRevealed flips the modal into "decoded" mode — the title leads with
// "decoded" and a yellow banner above the YAML body warns that values are
// shown in plaintext. Call this after Reset when opening a Secret reveal.
func (s *Describe) MarkRevealed() {
	s.revealed = true
	s.recomputeViewportSize()
}

// FilterActive lets the root tell whether `y` should yank the YAML or fall
// through to the filter input as a literal character.
func (s *Describe) FilterActive() bool { return s.filter.Active() }

// YankPayload returns the currently visible (filtered) lines of the manifest
// joined with newlines. With no filter that's the full YAML; with a filter
// it's just the matching lines, on the same "the filter is the operator's
// scope" reasoning as logs and events.
func (s *Describe) YankPayload() string {
	if len(s.filtered) == 0 {
		return s.yaml
	}
	out := make([]string, 0, len(s.filtered))
	for _, idx := range s.filtered {
		if idx >= 0 && idx < len(s.lines) {
			out = append(out, s.lines[idx])
		}
	}
	return strings.Join(out, "\n")
}

// SetYAML hands the loaded manifest (or error) to the modal.
func (s *Describe) SetYAML(yaml string, err error) {
	s.yaml = yaml
	s.lines = strings.Split(strings.TrimRight(yaml, "\n"), "\n")
	s.loading = false
	s.err = err
	s.recomputeFilter()
	s.refreshContent()
}

func (s *Describe) Title() string {
	verb := "describe"
	if s.revealed {
		verb = "decoded"
	}
	return fmt.Sprintf("%s · %s · %s", verb, strings.ToLower(s.kind), s.name)
}

func (s *Describe) SetSize(termW, termH int) {
	s.modalW, s.modalH = components.ModalFitSize(termW, termH)
	s.recomputeViewportSize()
}

// SetPaneSize sizes the section for inline rendering inside the workspace
// body area (innerW × innerH). modalW/H are back-computed so ModalChrome
// math resolves the viewport to (innerW, innerH-2). See logs.SetPaneSize.
func (s *Describe) SetPaneSize(innerW, innerH int) {
	if innerW < 14 {
		innerW = 14
	}
	if innerH < 4 {
		innerH = 4
	}
	s.modalW = innerW + 2
	s.modalH = innerH + 2
	s.recomputeViewportSize()
}

// Body returns unframed content for workspace tab embedding. When the
// describe is a Secret reveal, a yellow banner prefixes the body to make
// "this is decoded plaintext, not base64" loud — same cue the legacy
// modal carried.
func (s *Describe) Body(c *tuictx.Ctx) string {
	body := s.viewportBlock(c)
	if s.revealed {
		banner := lipgloss.NewStyle().
			Foreground(c.Theme.P.Yellow).
			Bold(true).
			Render("⚠ decoded — values are plaintext")
		body = lipgloss.JoinVertical(lipgloss.Left, banner, body)
	}
	return lipgloss.JoinVertical(lipgloss.Left,
		s.renderStatusRow(c),
		body,
		s.renderKeysRow(),
	)
}

// PaneTitle returns the workspace tab title.
func (s *Describe) PaneTitle() string { return s.Title() }

func (s *Describe) recomputeViewportSize() {
	inner := components.PanelContentWidth(s.modalW)
	s.vp.SetWidth(inner)
	chrome := components.ModalChrome
	if s.revealed {
		// One extra row for the "decoded" banner above the body.
		chrome++
	}
	h := s.modalH - chrome
	if h < 3 {
		h = 3
	}
	s.vp.SetHeight(h)
	s.filter.SetWidth(components.ModalFilterWidth(s.modalW))
}

func (s *Describe) recomputeFilter() {
	s.filtered = s.filtered[:0]
	q := s.filter.Value()
	if q == "" {
		for i := range s.lines {
			s.filtered = append(s.filtered, i)
		}
		return
	}
	cs := hasUpper(q)
	needle := q
	if !cs {
		needle = strings.ToLower(q)
	}
	for i, ln := range s.lines {
		hay := ln
		if !cs {
			hay = strings.ToLower(ln)
		}
		if strings.Contains(hay, needle) {
			s.filtered = append(s.filtered, i)
		}
	}
}

func (s *Describe) refreshContent() {
	if s.theme == nil {
		return
	}
	var b strings.Builder
	for n, idx := range s.filtered {
		if n > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(colorizeYAMLLine(s.lines[idx], s.theme))
	}
	s.vp.SetContent(b.String())
}

// colorizeYAMLLine paints a single YAML line with light syntax highlighting
// — keys cyan-bold, structural punctuation dim, comments dim italic, scalars
// styled by inferred type (strings green, numbers pink, bool/null yellow).
// Quick visual structure for an SRE scanning a 300-line manifest.
func colorizeYAMLLine(line string, t *theme.Theme) string {
	if strings.TrimSpace(line) == "" {
		return line
	}

	// Preserve leading indentation literally.
	i := 0
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	indent := line[:i]
	rest := line[i:]

	dim := lipgloss.NewStyle().Foreground(t.P.Dim)
	dimItalic := t.Hint
	keyStyle := lipgloss.NewStyle().Foreground(t.P.Cyan).Bold(true)

	// Whole-line comment.
	if strings.HasPrefix(rest, "#") {
		return indent + dimItalic.Render(rest)
	}

	// Document separator / end markers ---/...
	if rest == "---" || rest == "..." {
		return indent + dim.Render(rest)
	}

	// List marker `- ` (or bare `-`).
	listMarker := ""
	switch {
	case strings.HasPrefix(rest, "- "):
		listMarker = dim.Render("-") + " "
		rest = rest[2:]
	case rest == "-":
		return indent + dim.Render("-")
	}

	// Strip trailing comment for separate styling.
	rest, comment := splitTrailingYAMLComment(rest)
	commentRendered := ""
	if comment != "" {
		commentRendered = " " + dimItalic.Render(strings.TrimLeft(comment, " "))
	}

	// Look for a key:value separator. The colon must be followed by space,
	// tab, or end-of-line — that disambiguates "url: http://..." (colon after
	// "url") from "image: registry:5000/..." (the inner colon is part of the
	// scalar, not a separator).
	colon := findKeyColon(rest)
	if colon < 0 {
		// No key — pure scalar (continuation line, list value, multiline body).
		return indent + listMarker + colorizeYAMLValue(rest, t) + commentRendered
	}

	key := rest[:colon]
	after := rest[colon+1:]
	keyRendered := keyStyle.Render(key) + dim.Render(":")

	if after == "" {
		return indent + listMarker + keyRendered + commentRendered
	}
	// Preserve inter-segment whitespace exactly, then style the value.
	leadWS := after[:len(after)-len(strings.TrimLeft(after, " \t"))]
	val := after[len(leadWS):]
	if val == "" {
		return indent + listMarker + keyRendered + leadWS + commentRendered
	}
	return indent + listMarker + keyRendered + leadWS + colorizeYAMLValue(val, t) + commentRendered
}

// colorizeYAMLValue picks a colour by inferred scalar type. Quoted strings →
// green, numbers → pink, bool/null → yellow bold, block-scalar markers (|, >)
// dim, plain strings stay default.
func colorizeYAMLValue(v string, t *theme.Theme) string {
	if v == "" {
		return v
	}
	first := v[0]
	if first == '"' || first == '\'' {
		return lipgloss.NewStyle().Foreground(t.P.Green).Render(v)
	}
	switch v {
	case "true", "false", "null", "~":
		return lipgloss.NewStyle().Foreground(t.P.Yellow).Bold(true).Render(v)
	}
	if v == "|" || v == ">" || v == "|-" || v == ">-" || v == "|+" || v == ">+" {
		return lipgloss.NewStyle().Foreground(t.P.Dim).Render(v)
	}
	if isYAMLNumber(v) {
		return lipgloss.NewStyle().Foreground(t.P.Pink).Render(v)
	}
	// Plain scalar — left in default foreground for readability against the
	// coloured keys.
	return v
}

func isYAMLNumber(s string) bool {
	if s == "" {
		return false
	}
	i := 0
	if s[0] == '-' || s[0] == '+' {
		i++
	}
	if i == len(s) {
		return false
	}
	dots := 0
	digits := 0
	for ; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
			digits++
		case c == '.':
			dots++
			if dots > 1 {
				return false
			}
		default:
			return false
		}
	}
	return digits > 0
}

// findKeyColon returns the index of the first ':' that acts as a YAML mapping
// separator (followed by space/tab/EOL), respecting single/double quotes. -1
// when the line carries no key.
//
// Quoting nuance: inside double quotes a backslash escapes the next byte, so
// `"a\":b": value` is one quoted key terminated at index 6, not at index 2.
// Single-quoted YAML strings don't use backslash escapes (they use doubled
// `”` to embed a quote, which our toggle naturally handles by flipping off
// then back on).
func findKeyColon(s string) int {
	dq := false
	sq := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if dq && c == '\\' && i+1 < len(s) {
			i++
			continue
		}
		switch {
		case c == '"' && !sq:
			dq = !dq
		case c == '\'' && !dq:
			sq = !sq
		case c == ':' && !dq && !sq:
			if i+1 == len(s) || s[i+1] == ' ' || s[i+1] == '\t' {
				return i
			}
		}
	}
	return -1
}

// splitTrailingYAMLComment finds an unquoted '#' preceded by whitespace and
// returns (before, comment-including-hash). Returns (s, "") if no comment.
// Mirrors findKeyColon's quote handling, including the `\"` escape inside
// double quotes.
func splitTrailingYAMLComment(s string) (string, string) {
	dq := false
	sq := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if dq && c == '\\' && i+1 < len(s) {
			i++
			continue
		}
		switch {
		case c == '"' && !sq:
			dq = !dq
		case c == '\'' && !dq:
			sq = !sq
		case c == '#' && !dq && !sq:
			if i == 0 || s[i-1] == ' ' || s[i-1] == '\t' {
				return strings.TrimRight(s[:i], " \t"), s[i:]
			}
		}
	}
	return s, ""
}

// Update returns (cmd, leave). leave=true closes the modal.
func (s *Describe) Update(msg tea.Msg, c *tuictx.Ctx) (tea.Cmd, bool) {
	km, ok := msg.(tea.KeyPressMsg)
	if !ok {
		var cmd tea.Cmd
		s.vp, cmd = s.vp.Update(msg)
		return cmd, false
	}

	if s.filter.Active() {
		return s.handleFilterKey(km)
	}

	switch {
	case key.Matches(km, c.Keys.Back), km.String() == "q":
		return nil, true

	case km.String() == "/":
		s.filter.Open()
		s.recomputeViewportSize()
		return nil, false

	case key.Matches(km, c.Keys.Refresh):
		s.loading = true
		return LoadDescribe(c.Background, c.Manager, c.ActiveContext, s.namespace, s.kind, s.name), false

	case km.String() == "ctrl+l":
		s.filter.Clear()
		s.recomputeFilter()
		s.refreshContent()
		return nil, false
	}

	var cmd tea.Cmd
	s.vp, cmd = s.vp.Update(km)
	return cmd, false
}

func (s *Describe) handleFilterKey(km tea.KeyPressMsg) (tea.Cmd, bool) {
	cmd, action := s.filter.HandleKey(km)
	switch action {
	case components.FilterCancel:
		s.recomputeFilter()
		s.refreshContent()
		s.recomputeViewportSize()
	case components.FilterCommit:
		s.recomputeViewportSize()
	case components.FilterEdit:
		s.recomputeFilter()
		s.refreshContent()
	}
	return cmd, false
}

func (s *Describe) viewportBlock(c *tuictx.Ctx) string {
	st := components.BlockState{}
	switch {
	case s.loading:
		st.Loading = true
		st.Loading_ = "loading manifest"
	case s.err != nil:
		st.Err = s.err
	case len(s.lines) == 0:
		st.Empty = true
		st.Empty_ = "empty manifest"
	case len(s.filtered) == 0 && s.filter.Value() != "":
		st.NoMatch = true
	default:
		st.Loaded = true
	}
	return components.CenteredViewportBlock(c.Theme, c.Spinner.View(), s.vp, st)
}

func (s *Describe) renderStatusRow(c *tuictx.Ctx) string {
	indicators := make([]components.Indicator, 0, 2)
	if s.filter.Value() != "" {
		indicators = append(indicators, components.Indicator{
			Text: fmt.Sprintf("%d/%d lines", len(s.filtered), len(s.lines)),
		})
	} else if len(s.lines) > 0 {
		indicators = append(indicators, components.Indicator{
			Text: fmt.Sprintf("%d lines", len(s.lines)),
		})
	}
	indicators = append(indicators, components.Indicator{
		Text: fmt.Sprintf("%3.f%%", s.vp.ScrollPercent()*100),
	})
	return components.RenderModalStatusBar(c.Theme, s.modalW, s.filter, indicators)
}

func (s *Describe) renderKeysRow() string {
	return components.RenderModalKeysRow(s.theme, []components.KeyDesc{
		{Key: "/", Desc: "filter"},
		{Key: "r", Desc: "refresh"},
		{Key: "j/k", Desc: "scroll"},
		{Key: "g/G", Desc: "top/bot"},
		{Key: "ctrl+l", Desc: "clear"},
		{Key: "esc", Desc: "close"},
	})
}
