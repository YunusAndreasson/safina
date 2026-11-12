package sections

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/YunusAndreasson/safina/internal/tui/components"
	"github.com/YunusAndreasson/safina/internal/tui/textw"
	"github.com/YunusAndreasson/safina/internal/tui/theme"
	"github.com/YunusAndreasson/safina/internal/tui/tuictx"
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
	pod         string
	container   string
	previous    bool
	streamEnded bool
	streamErr   error
	namespace   string

	logBytes          int
	discardedLogLines int64
	discardedLogBytes int64
	lines             []string            // raw lines, unfiltered
	filtered          []int               // indices into lines that match current filter
	rendered          string              // cached, filter-aware render fed to the viewport
	renderTail        logRenderCheckpoint // state before the unfinished final raw line

	vp     viewport.Model
	follow bool
	filter components.Filter
	jq     logJQ
	// wrap drives the viewport's soft-wrap mode. Default false (long
	// lines truncate at the panel's right edge, matching every log
	// viewer the on-caller has seen); `w` toggles it for the times a
	// wide panic / large JSON record needs to read in one glance.
	wrap             bool
	fullTimestamps   bool
	compactTimeShown bool

	theme *theme.Theme

	// modal outer dimensions, recomputed on resize and filter toggle
	modalW int
	modalH int
}

// A stream can only change its last raw line. Keep the rendering state before
// that line so the next chunk reuses all completed lines, including date headers.
type logRenderCheckpoint struct {
	bytes   int
	matches int
	lastDay string
	compact bool
}

// NewLogs constructs the log modal. ctx is captured for theme + spinner
// access during async log appends.
func NewLogs(c *tuictx.Ctx) Logs {
	vp := viewport.New()
	vp.MouseWheelEnabled = true
	vp.SoftWrap = false
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
	l.container = ""
	l.previous = false
	l.streamEnded = false
	l.streamErr = nil
	l.namespace = namespace
	l.lines = nil
	l.filtered = nil
	l.logBytes = 0
	l.discardedLogLines = 0
	l.discardedLogBytes = 0
	l.rendered = ""
	l.renderTail = logRenderCheckpoint{}
	l.follow = true
	l.filter.Clear()
	l.jq = logJQ{}
	l.filter.Close()
	l.vp.SetContent("")
	l.vp.GotoTop()
}

// EndStream leaves the output readable while reporting that no more is arriving.
func (l *Logs) EndStream(err error) { l.streamEnded = true; l.streamErr = err }

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

// SetPaneSize sizes the section for inline rendering inside a resource
// page's tab body of inner dimensions (innerW, innerH). The page owns the
// header and tabs; inside the body the section reserves 2 rows for status
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

// Body returns the section's content for a resource page's tab: the
// viewport, under the filter row while a filter is in use. Its state goes
// on the tab rule (Indicators) and its keys in the footer (Hints).
func (l *Logs) Body(c *tuictx.Ctx) string {
	if row, ok := components.FilterRow(c.Theme, l.filter); ok {
		return textw.JoinVertical(lipgloss.Left, row, l.viewportBlock(c))
	}
	return l.viewportBlock(c)
}

// filterRows is the height of the filter row: one while it is in use.
func (l *Logs) filterRows() int {
	if l.filter.Active() || l.filter.Value() != "" {
		return 1
	}
	return 0
}

// SetTarget records the container and stream generation.
func (l *Logs) SetTarget(container string, previous bool) {
	l.container = container
	l.previous = previous
}

func (l *Logs) PaneTitle() string { return l.pod }

func (l *Logs) recomputeViewportSize() {
	inner := components.PanelContentWidth(l.modalW)
	l.vp.SetWidth(inner)
	h := l.modalH - 2 - l.filterRows()
	if h < 3 {
		h = 3
	}
	// A following stream stays pinned to its newest line when the view
	// grows or shrinks (a filter row coming and going, a resize).
	pinned := l.follow && l.vp.AtBottom()
	l.vp.SetHeight(h)
	if pinned {
		l.vp.GotoBottom()
	}
	l.filter.SetWidth(components.ModalFilterWidth(l.modalW))
}

// Append ingests a chunk from the log stream and re-applies the current
// filter so new lines appear (or don't) in the viewport without breaking
// follow.
func (l *Logs) Append(s string) {
	if s == "" {
		return
	}
	wasAtBottom := l.follow && l.vp.AtBottom()
	previousOffset := l.vp.YOffset()
	l.logBytes += len(s)
	// HTTP reads are arbitrary chunks, not records. Continue the final
	// fragment so split reads neither break lines nor insert blank rows.
	start := max(0, len(l.lines)-1)
	parts := strings.Split(s, "\n")
	if len(l.lines) == 0 {
		l.lines = parts
	} else {
		l.lines[len(l.lines)-1] += parts[0]
		l.lines = append(l.lines, parts[1:]...)
	}
	dropped, trimmed := l.trimLogStorage(start)
	if trimmed {
		removedMatches := 0
		for _, idx := range l.filtered {
			if idx < dropped {
				removedMatches++
			}
		}
		l.recomputeFilter()
		l.refreshContent()
		if l.follow && wasAtBottom {
			l.vp.GotoBottom()
		} else {
			l.vp.SetYOffset(max(0, previousOffset-removedMatches))
		}
		return
	}
	// Recheck the old final fragment: appending can both introduce and remove
	// a match (for example, completing a split UTF-8 character).
	for len(l.filtered) > 0 && l.filtered[len(l.filtered)-1] >= start {
		l.filtered = l.filtered[:len(l.filtered)-1]
	}
	l.appendFiltered(start)
	if l.jq.err != nil {
		l.refreshContent()
		return
	}
	// If the reusable prefix already contains every match, incoming records
	// changed only raw storage. Keep the viewport (and its scroll position)
	// intact instead of rescanning the unchanged displayed backlog.
	if l.renderTail.bytes == len(l.rendered) && l.renderTail.matches == len(l.filtered) {
		return
	}
	l.renderContent(l.renderTail)
	if l.follow && wasAtBottom {
		l.vp.GotoBottom()
	}
}

// recomputeFilter rebuilds matches after a query change.
func (l *Logs) recomputeFilter() {
	l.filtered = l.filtered[:0]
	l.jq = logJQ{}
	l.appendFiltered(0)
}

func (l *Logs) appendFiltered(start int) {
	q := l.filter.Value()
	if strings.HasPrefix(q, "jq:") {
		l.appendJQ(start, q)
		return
	}
	if q == "" {
		for i := start; i < len(l.lines); i++ {
			l.filtered = append(l.filtered, i)
		}
		return
	}
	cs := hasUpper(q)
	needle := q
	if !cs {
		needle = strings.ToLower(q)
	}
	for i := start; i < len(l.lines); i++ {
		hay := l.lines[i]
		if !cs {
			hay = strings.ToLower(hay)
		}
		if strings.Contains(hay, needle) {
			l.filtered = append(l.filtered, i)
		}
	}
}

// refreshContent formats the display separately from raw storage. Compact
// UTC times and date boundaries leave room for the message. Searches that
// match omitted timestamp precision show the original line so the match is
// visible. Copy always uses raw lines.
func (l *Logs) refreshContent() {
	l.renderContent(logRenderCheckpoint{})
}

func (l *Logs) renderContent(prefix logRenderCheckpoint) {
	if l.theme == nil {
		return
	}
	q := l.filter.Value()
	if strings.HasPrefix(q, "jq:") {
		q = ""
	}
	var b strings.Builder
	b.Grow(len(l.rendered))
	b.WriteString(l.rendered[:prefix.bytes])
	hl := lipgloss.NewStyle().Background(l.theme.P.Yellow).Foreground(l.theme.P.BG).Bold(true)
	lastDay := prefix.lastDay
	l.compactTimeShown = prefix.compact
	lowerQuery, caseSensitive := strings.ToLower(q), hasUpper(q)
	for n := prefix.matches; n < len(l.filtered); n++ {
		idx := l.filtered[n]
		if idx == len(l.lines)-1 {
			l.renderTail = logRenderCheckpoint{b.Len(), n, lastDay, l.compactTimeShown}
		}
		if n > 0 {
			b.WriteByte('\n')
		}
		line := l.lines[idx]
		if !l.fullTimestamps {
			compact, day := compactLogTimestamp(line)
			// A search for timestamp precision must still show its match.
			if q == "" || strings.Contains(strings.ToLower(compact), lowerQuery) {
				line = compact
				if day != "" {
					l.compactTimeShown = true
				}
				if day != "" && day != lastDay {
					b.WriteString(l.theme.Hint.Render("── " + day + " · UTC ──"))
					b.WriteByte('\n')
					lastDay = day
				}
			}
		}
		out := colorizeLogLine(line, l.theme)
		if q != "" {
			out = highlight(line, q, caseSensitive, hl)
		}
		// The viewport cuts a line at the pane edge counting a tab as no
		// width, then lipgloss draws the tab as four spaces. The overflow
		// wrapped onto a row the viewport never counted, so follow stopped
		// half a screen short of the newest record.
		b.WriteString(strings.ReplaceAll(out, "\t", "    "))
	}
	// If the unfinished line did not match, the whole render is reusable.
	if len(l.filtered) == 0 || l.filtered[len(l.filtered)-1] != len(l.lines)-1 {
		l.renderTail = logRenderCheckpoint{b.Len(), len(l.filtered), lastDay, l.compactTimeShown}
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
	if paste, ok := msg.(tea.PasteMsg); ok && l.filter.Active() {
		cmd := l.filter.Update(paste)
		l.recomputeFilter()
		l.refreshContent()
		if l.follow {
			l.vp.GotoBottom()
		}
		return cmd, false
	}
	km, ok := msg.(tea.KeyPressMsg)
	if !ok {
		var cmd tea.Cmd
		l.vp, cmd = l.vp.Update(msg)
		if _, wheel := msg.(tea.MouseWheelMsg); wheel && !l.vp.AtBottom() {
			l.follow = false
		}
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

	case km.String() == "t":
		l.fullTimestamps = !l.fullTimestamps
		l.refreshContent()
		if l.follow {
			l.vp.GotoBottom()
		}
		return nil, false

	case km.String() == "/":
		l.filter.Open()
		l.recomputeViewportSize()
		return nil, false

	case km.String() == " " || km.String() == "space" || km.String() == "f":
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

	case key.Matches(km, c.Keys.Wrap):
		// Toggle soft wrap. The viewport handles wrap internally —
		// we only flip its SoftWrap flag; the next render re-flows
		// the existing buffer. Follow stays sticky: if we were at
		// the bottom before, we should remain visually at the bottom
		// after re-wrap (otherwise the line count changes and the
		// last line drifts out of view).
		wasAtBottom := l.vp.AtBottom()
		l.wrap = !l.wrap
		l.vp.SoftWrap = l.wrap
		if l.follow && wasAtBottom {
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
	case l.jq.err != nil:
		st.Err = l.jq.err
	case len(l.lines) == 0 && l.streamErr != nil && !errors.Is(l.streamErr, context.Canceled):
		// Why the stream never started (no pods left, access denied) is the
		// answer here; "no log output" only sent the reader to retry.
		st.Err = l.streamErr
	case len(l.lines) == 0 && l.streamEnded:
		st.Empty = true
		st.Empty_ = "No log output. Press l to reopen logs."
	case len(l.lines) == 0:
		st.Loading = true
		st.Loading_ = "establishing stream"
	case len(l.filtered) == 0 && l.filter.Value() != "":
		st.NoMatch = true
	default:
		st.Loaded = true
	}
	return components.CenteredViewportBlock(c.Theme, c.SpinnerView(), l.vp, st)
}

// renderStatusRow shows query (or hint), match count, follow state, and
// scroll percentage on a single line. Width-aware: left chunk and right
// chunk justify to the panel inner width.
// Indicators is the stream's state for the right end of the tab rule.
func (l *Logs) Indicators(c *tuictx.Ctx) []components.Indicator {
	t := c.Theme
	indicators := make([]components.Indicator, 0, 7)
	if l.discardedLogBytes > 0 {
		indicators = append(indicators, components.Indicator{Text: fmt.Sprintf("buffer capped · %d lines / %d KiB discarded", l.discardedLogLines, l.discardedLogBytes/1024)})
	}
	source := strings.Trim(strings.Join([]string{l.namespace, l.container}, "/"), "/")
	if source != "" {
		indicators = append(indicators, components.Indicator{Text: source})
	}
	if l.previous {
		indicators = append(indicators, components.Indicator{Text: "previous"})
	}
	if l.filter.Value() != "" {
		indicators = append(indicators, components.Indicator{
			Text: fmt.Sprintf("%d/%d matches", len(l.filtered), len(l.lines)),
		})
	}
	switch {
	case l.streamEnded:
		label := "stream ended · l reopen"
		if l.streamErr != nil {
			label = "disconnected · l retry"
		}
		indicators = append(indicators, components.Indicator{Text: label})
	case l.follow:
		followStyle := lipgloss.NewStyle().Foreground(t.P.Green).Bold(true)
		indicators = append(indicators, components.Indicator{Text: "● follow", Style: &followStyle})
	default:
		indicators = append(indicators, components.Indicator{Text: "○ scroll paused"})
	}
	if l.compactTimeShown {
		indicators = append(indicators, components.Indicator{Text: "UTC"})
	}
	return indicators
}

// Hints lists the stream's keys for the footer, in the same style/separator
// convention as the global help bar (interpunct-separated, dim). The
// wrap key carries its current state in the description ("wrap on" /
// "wrap") so the operator sees the toggle's effect without a separate
// indicator.
func (l *Logs) Hints() []components.KeyDesc {
	wrapDesc := "wrap"
	if l.wrap {
		wrapDesc = "wrap on"
	}
	followDesc := "pause"
	if !l.follow {
		followDesc = "resume"
	}
	timeDesc := "full time"
	if l.fullTimestamps {
		timeDesc = "short time"
	}
	return []components.KeyDesc{
		{Key: "/", Desc: "filter"},
		{Key: "space", Desc: followDesc},
		{Key: "o", Desc: "targets"},
		{Key: "p", Desc: "previous"},
		{Key: "w", Desc: wrapDesc},
		{Key: "t", Desc: timeDesc},
		{Key: "g/G", Desc: "top/bot"},
		{Key: "ctrl+l", Desc: "clear"},
	}
}
