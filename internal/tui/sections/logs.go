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

// Logs is the streaming-log modal. It floats over the main view on a dim
// backdrop and is built for an SRE in a stressful situation: every piece of
// state (query, match count, follow, scroll) is on screen at all times, and
// the keys are predictable across the rest of the app.
//
// Filter semantics: smart-case (lowercase query → case-insensitive; any
// uppercase → case-sensitive, ripgrep convention). Substring match. Matched
// substrings are highlighted in yellow inline.
type Logs struct {
	pod       string
	namespace string

	lines    []string // raw lines, unfiltered
	filtered []int    // indices into lines that match current filter
	rendered string   // cached, filter-aware render fed to the viewport

	vp     viewport.Model
	follow bool
	filter components.Filter

	theme *theme.Theme

	// modal outer dimensions, recomputed on resize and filter toggle
	modalW int
	modalH int
}

// NewLogs constructs the log modal. ctx is captured for theme + spinner
// access during async log appends.
func NewLogs(c *tuictx.Ctx) Logs {
	vp := viewport.New()
	vp.MouseWheelEnabled = true
	return Logs{
		vp:     vp,
		follow: true,
		filter: components.NewFilter(c.Theme),
		theme:  c.Theme,
	}
}

// Reset clears the log buffer and prepares the modal for a new pod stream.
// Called every time the user presses Enter on a pod.
func (l *Logs) Reset(pod, namespace string) {
	l.pod = pod
	l.namespace = namespace
	l.lines = l.lines[:0]
	l.filtered = l.filtered[:0]
	l.rendered = ""
	l.follow = true
	l.filter.Clear()
	l.filter.Close()
	l.vp.SetContent("")
	l.vp.GotoTop()
}

func (l *Logs) Pod() string     { return l.pod }
func (l *Logs) Lines() []string { return l.lines }

// FilterActive lets the root tell whether `y` should yank the visible buffer
// or fall through to the filter input as a literal character.
func (l *Logs) FilterActive() bool { return l.filter.Active() }

// YankPayload returns the currently visible (filtered) log lines joined with
// newlines. When no filter is active this is the entire buffer. Yanking the
// filtered slice is intentional: the filter is the operator saying "this is
// what I care about" — copying the full buffer would defeat the point of
// having narrowed it down.
func (l *Logs) YankPayload() string {
	if len(l.filtered) == 0 {
		return ""
	}
	out := make([]string, 0, len(l.filtered))
	for _, idx := range l.filtered {
		if idx >= 0 && idx < len(l.lines) {
			out = append(out, l.lines[idx])
		}
	}
	return strings.Join(out, "\n")
}

// SetSize is invoked on every WindowSizeMsg. It computes the modal's outer
// dimensions, sizes the viewport to match, and resizes the filter input so
// the query field tracks the modal width.
func (l *Logs) SetSize(termW, termH int) {
	l.modalW, l.modalH = components.ModalFitSize(termW, termH)
	l.recomputeViewportSize()
}

// SetPaneSize sizes the section for inline rendering inside the workspace
// body area of inner dimensions (innerW, innerH). The workspace pane owns
// the outer border; inside the body the section reserves 2 rows for status
// + keys. modalW/modalH are back-computed so the existing PanelContentWidth
// and ModalChrome math still resolves to (innerW, innerH-2) for the viewport.
func (l *Logs) SetPaneSize(innerW, innerH int) {
	if innerW < 14 {
		innerW = 14
	}
	if innerH < 4 {
		innerH = 4
	}
	l.modalW = innerW + 2
	l.modalH = innerH + 2
	l.recomputeViewportSize()
}

// Body returns the section's content (status row + viewport + keys row) as
// an unframed stack — for embedding inside the workspace pane, which owns
// the panel border / focus ring.
func (l *Logs) Body(c *tuictx.Ctx) string {
	return lipgloss.JoinVertical(lipgloss.Left,
		l.renderStatusRow(c),
		l.viewportBlock(c),
		l.renderKeysRow(),
	)
}

// PaneTitle returns the workspace tab title for this section.
func (l *Logs) PaneTitle() string {
	if l.namespace != "" {
		return fmt.Sprintf("logs · %s · %s", l.pod, l.namespace)
	}
	return fmt.Sprintf("logs · %s", l.pod)
}

func (l *Logs) recomputeViewportSize() {
	inner := components.PanelContentWidth(l.modalW)
	l.vp.SetWidth(inner)
	h := l.modalH - components.ModalChrome
	if h < 3 {
		h = 3
	}
	l.vp.SetHeight(h)
	l.filter.SetWidth(components.ModalFilterWidth(l.modalW))
}

// Append ingests a chunk from the log stream and re-applies the current
// filter so new lines appear (or don't) in the viewport without breaking
// follow.
func (l *Logs) Append(s string) {
	wasAtBottom := l.vp.AtBottom()
	l.lines = append(l.lines, strings.Split(s, "\n")...)
	l.recomputeFilter()
	l.refreshContent()
	if l.follow && wasAtBottom {
		l.vp.GotoBottom()
	}
}

// recomputeFilter rebuilds the filtered index list from scratch. Cheap enough
// for the log volumes we're dealing with; correct in the face of streaming
// arrivals.
func (l *Logs) recomputeFilter() {
	l.filtered = l.filtered[:0]
	q := l.filter.Value()
	if q == "" {
		for i := range l.lines {
			l.filtered = append(l.filtered, i)
		}
		return
	}
	cs := hasUpper(q)
	needle := q
	if !cs {
		needle = strings.ToLower(q)
	}
	for i, line := range l.lines {
		hay := line
		if !cs {
			hay = strings.ToLower(line)
		}
		if strings.Contains(hay, needle) {
			l.filtered = append(l.filtered, i)
		}
	}
}

// refreshContent re-renders the viewport's content string with match
// highlighting applied.
func (l *Logs) refreshContent() {
	if l.theme == nil {
		return
	}
	hl := lipgloss.NewStyle().
		Background(l.theme.P.Yellow).
		Foreground(l.theme.P.BG).
		Bold(true)
	q := l.filter.Value()
	cs := hasUpper(q)
	var b strings.Builder
	for n, idx := range l.filtered {
		if n > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(highlight(l.lines[idx], q, cs, hl))
	}
	l.rendered = b.String()
	l.vp.SetContent(l.rendered)
}

// highlight wraps each occurrence of query inside line in hl. Smart-case via
// the cs flag; the original case of line is preserved in output.
func highlight(line, query string, cs bool, hl lipgloss.Style) string {
	if query == "" {
		return line
	}
	var hay string
	needle := query
	if cs {
		hay = line
	} else {
		hay = strings.ToLower(line)
		needle = strings.ToLower(query)
	}
	var b strings.Builder
	i := 0
	for {
		idx := strings.Index(hay[i:], needle)
		if idx < 0 {
			b.WriteString(line[i:])
			break
		}
		b.WriteString(line[i : i+idx])
		b.WriteString(hl.Render(line[i+idx : i+idx+len(needle)]))
		i += idx + len(needle)
	}
	return b.String()
}

func hasUpper(s string) bool {
	for _, r := range s {
		if r >= 'A' && r <= 'Z' {
			return true
		}
	}
	return false
}

// Update returns (cmd, leave). When leave is true the root closes the modal
// and returns to the main view.
func (l *Logs) Update(msg tea.Msg, c *tuictx.Ctx) (tea.Cmd, bool) {
	km, ok := msg.(tea.KeyPressMsg)
	if !ok {
		var cmd tea.Cmd
		l.vp, cmd = l.vp.Update(msg)
		return cmd, false
	}

	if l.filter.Active() {
		return l.handleFilterKey(km)
	}

	switch {
	case key.Matches(km, c.Keys.Back), km.String() == "q":
		// Esc / q outside the filter input: close the modal. The filter
		// query itself is reset via Reset() when a new pod's stream starts,
		// so we don't need to clear it here.
		return nil, true

	case km.String() == "/":
		l.filter.Open()
		l.recomputeViewportSize()
		return nil, false

	case km.String() == " " || km.String() == "f":
		l.follow = !l.follow
		if l.follow {
			l.vp.GotoBottom()
		}
		return nil, false

	case km.String() == "g":
		l.follow = false
		l.vp.GotoTop()
		return nil, false

	case km.String() == "G":
		l.follow = true
		l.vp.GotoBottom()
		return nil, false

	case km.String() == "ctrl+l":
		l.filter.Clear()
		l.recomputeFilter()
		l.refreshContent()
		if l.follow {
			l.vp.GotoBottom()
		}
		return nil, false
	}

	prevAtBottom := l.vp.AtBottom()
	var cmd tea.Cmd
	l.vp, cmd = l.vp.Update(km)
	// Manual scroll up implicitly disengages follow — same convention as `tail
	// -f` clients. `G` re-engages.
	if prevAtBottom && !l.vp.AtBottom() {
		l.follow = false
	}
	return cmd, false
}

func (l *Logs) handleFilterKey(km tea.KeyPressMsg) (tea.Cmd, bool) {
	cmd, action := l.filter.HandleKey(km)
	switch action {
	case components.FilterCancel:
		// Snapshot reverted by HandleKey; rerun match pass on the prior query.
		l.recomputeFilter()
		l.refreshContent()
		l.recomputeViewportSize()
	case components.FilterCommit:
		// Query preserved; only the row layout shifts (filter view is gone).
		l.recomputeViewportSize()
	case components.FilterEdit:
		l.recomputeFilter()
		l.refreshContent()
		if l.follow {
			l.vp.GotoBottom()
		}
	}
	return cmd, false
}

// viewportBlock returns the viewport's view, replacing it with a centred
// "waiting" message + spinner before the first line arrives so the operator
// has positive feedback that a stream is being established.
func (l *Logs) viewportBlock(c *tuictx.Ctx) string {
	st := components.BlockState{}
	switch {
	case len(l.lines) == 0:
		st.Loading = true
		st.Loading_ = "establishing stream"
	case len(l.filtered) == 0 && l.filter.Value() != "":
		st.NoMatch = true
	default:
		st.Loaded = true
	}
	return components.CenteredViewportBlock(c.Theme, c.Spinner.View(), l.vp, st)
}

// renderStatusRow shows query (or hint), match count, follow state, and
// scroll percentage on a single line. Width-aware: left chunk and right
// chunk justify to the panel inner width.
func (l *Logs) renderStatusRow(c *tuictx.Ctx) string {
	t := c.Theme
	indicators := make([]components.Indicator, 0, 3)
	if l.filter.Value() != "" {
		indicators = append(indicators, components.Indicator{
			Text: fmt.Sprintf("%d/%d matches", len(l.filtered), len(l.lines)),
		})
	}
	if l.follow {
		followStyle := lipgloss.NewStyle().Foreground(t.P.Green).Bold(true)
		indicators = append(indicators, components.Indicator{Text: "● follow", Style: &followStyle})
	} else {
		indicators = append(indicators, components.Indicator{Text: "○ paused"})
	}
	indicators = append(indicators, components.Indicator{
		Text: fmt.Sprintf("%3.f%%", l.vp.ScrollPercent()*100),
	})
	return components.RenderModalStatusBar(t, l.modalW, l.filter, indicators)
}

// renderKeysRow lists the modal's key bindings in the same style/separator
// convention as the global help bar (interpunct-separated, dim).
func (l *Logs) renderKeysRow() string {
	return components.RenderModalKeysRow(l.theme, []components.KeyDesc{
		{Key: "/", Desc: "filter"},
		{Key: "space", Desc: "follow"},
		{Key: "j/k", Desc: "scroll"},
		{Key: "g/G", Desc: "top/bot"},
		{Key: "ctrl+l", Desc: "clear"},
		{Key: "esc", Desc: "close"},
	})
}
