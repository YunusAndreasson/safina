package sections

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/YunusAndreasson/safina/internal/tui/components"
	"github.com/YunusAndreasson/safina/internal/tui/textw"
	"github.com/YunusAndreasson/safina/internal/tui/theme"
	"github.com/YunusAndreasson/safina/internal/tui/tuictx"
)

// WorkspaceTab enumerates the views of a resource page. Children (an app's
// pods, a pod's containers) and Details are the page's own surfaces; Logs,
// Events, YAML and Metrics are reading views of the same resource. Chat,
// Hot pods, Forwards and Drain are tool pages: they keep a WorkspaceTab so
// the model can name them, but they never appear in a page's tab strip.
type WorkspaceTab int

const (
	WSTabDetails WorkspaceTab = iota
	WSTabChildren
	WSTabMetrics
	WSTabLogs
	WSTabEvents
	WSTabYaml
	WSTabChat
	WSTabHot
	WSTabForwards
	WSTabDrain
)

// Title is the tab's sentence-case name. The children tab is named by what
// the resource holds, so it needs the page's kind.
func (t WorkspaceTab) Title(focusKind string) string {
	switch t {
	case WSTabChildren:
		return ChildrenLabel(focusKind)
	case WSTabDetails:
		return "Details"
	case WSTabMetrics:
		return "Metrics"
	case WSTabLogs:
		return "Logs"
	case WSTabEvents:
		return "Events"
	case WSTabYaml:
		return "YAML"
	case WSTabChat:
		return "Chat"
	case WSTabHot:
		return "Hot pods"
	case WSTabForwards:
		return "Forwards"
	case WSTabDrain:
		return "Drain"
	}
	return ""
}

// IsReading reports whether the tab is one of the reading views a state
// drives (and that must be reopened rather than merely shown).
func (t WorkspaceTab) IsReading() bool {
	return t == WSTabMetrics || t == WSTabLogs || t == WSTabEvents || t == WSTabYaml
}

// WSTabAvailability describes which tabs a resource page offers.
type WSTabAvailability struct {
	Children bool
	Metrics  bool
	Logs     bool
	Events   bool
	Yaml     bool
}

// AvailableTabs returns the page's tabs in strip order: what the resource
// holds first, then what it is, then how it behaves.
func AvailableTabs(a WSTabAvailability) []WorkspaceTab {
	var tabs []WorkspaceTab
	if a.Children {
		tabs = append(tabs, WSTabChildren)
	}
	tabs = append(tabs, WSTabDetails)
	if a.Logs {
		tabs = append(tabs, WSTabLogs)
	}
	if a.Events {
		tabs = append(tabs, WSTabEvents)
	}
	if a.Yaml {
		tabs = append(tabs, WSTabYaml)
	}
	if a.Metrics {
		tabs = append(tabs, WSTabMetrics)
	}
	return tabs
}

// TabLabels numbers the tabs by position: the number is the key that opens
// it, so the strip teaches its own shortcuts.
func TabLabels(tabs []WorkspaceTab, focusKind string) []string {
	out := make([]string, len(tabs))
	for i, tab := range tabs {
		out[i] = fmt.Sprintf("%d %s", i+1, tab.Title(focusKind))
	}
	return out
}

// TabHitbox describes the on-strip x-range one tab occupies after rendering,
// so a click on a label opens that tab. X0 is inclusive, X1 exclusive,
// matching the zero-based MouseClickMsg.X.
type TabHitbox struct {
	Tab WorkspaceTab
	X0  int
	X1  int
}

// The tab rule is one row: the labels sit on the rule itself, in the same
// family as a list's title rule, and the open tab's own state closes it on
// the right:
//
//	━ 1 Pods ━── 2 Details ── 3 Logs ── 4 Events ─────────── 1 204 lines ─
//
// The active label is bold between two heavy cells in the beacon cyan; the
// mark reads without colour.
const (
	// tabGap is the space, two rule cells and space between two labels.
	tabGap = 4
	// tabLead is the rule cell and space before the first label.
	tabLead = 2
	// tabTail is the rule kept after the last label or the state text.
	tabTail = 2
)

// workspaceTabLayout is shared by rendering and hit testing. When space is
// limited, keep a contiguous window containing the active tab visible.
func workspaceTabLayout(tabs []WorkspaceTab, labels []string, active WorkspaceTab, w int) (hits []TabHitbox, left, right bool) {
	if len(tabs) == 0 || w <= 0 {
		return nil, false, false
	}
	selected := 0
	for i, tab := range tabs {
		if tab == active {
			selected = i
			break
		}
	}
	start, end := 0, len(tabs)
	width := func() int {
		n := 0
		if start > 0 {
			n += 2
		}
		if end < len(tabs) {
			n += 2
		}
		for i := start; i < end; i++ {
			n += textw.Width(labels[i])
			if i > start {
				n += tabGap
			}
		}
		return n
	}
	for width() > w && end-start > 1 {
		if end-1 > selected {
			end--
		} else {
			start++
		}
	}
	if width() > w {
		return []TabHitbox{{Tab: tabs[selected], X0: 0, X1: w}}, false, false
	}
	pos := 0
	if start > 0 {
		pos = 2
	}
	for i := start; i < end; i++ {
		next := pos + textw.Width(labels[i])
		hits = append(hits, TabHitbox{Tab: tabs[i], X0: pos, X1: next})
		pos = next + tabGap
	}
	return hits, start > 0, end < len(tabs)
}

// tabRuleLayout places the labels on a w-cell rule, leaving room for the
// state text when every tab still fits beside it; otherwise the tabs win
// and the state is dropped. Hitboxes are in rule coordinates.
func tabRuleLayout(tabs []WorkspaceTab, labels []string, active WorkspaceTab, stateW, w int) (hits []TabHitbox, left, right, showState bool) {
	avail := func(state bool) int {
		n := w - tabLead - tabTail
		if state {
			n -= stateW + 3
		}
		return max(0, n)
	}
	if avail(false) < 1 {
		// No room for the rule itself: the active label takes the row.
		for _, tab := range tabs {
			if tab == active {
				return []TabHitbox{{Tab: tab, X0: 0, X1: w}}, false, false, false
			}
		}
		return nil, false, false, false
	}
	showState = stateW > 0
	hits, left, right = workspaceTabLayout(tabs, labels, active, avail(showState))
	if showState && (left || right || len(hits) < len(tabs)) {
		showState = false
		hits, left, right = workspaceTabLayout(tabs, labels, active, avail(false))
	}
	for i := range hits {
		hits[i].X0 += tabLead
		hits[i].X1 += tabLead
	}
	return hits, left, right, showState
}

// WorkspaceTabHitboxes returns precisely the labels visible on the rule.
func WorkspaceTabHitboxes(tabs []WorkspaceTab, focusKind string, active WorkspaceTab, state string, w int) []TabHitbox {
	hits, _, _, _ := tabRuleLayout(tabs, TabLabels(tabs, focusKind), active, textw.Width(state), w)
	return hits
}

// RenderTabRule draws the tabs on their rule, with the open tab's state
// (pre-styled) at the right end when there is room for it.
func RenderTabRule(t *theme.Theme, tabs []WorkspaceTab, focusKind string, active WorkspaceTab, state string, w int) string {
	if w <= 0 {
		return ""
	}
	labels := TabLabels(tabs, focusKind)
	hits, left, right, showState := tabRuleLayout(tabs, labels, active, textw.Width(state), w)
	rule := lipgloss.NewStyle().Foreground(t.P.Dim)
	heavy := lipgloss.NewStyle().Foreground(t.P.Cyan)
	key := t.HelpKey
	activeStyle := lipgloss.NewStyle().Foreground(t.P.FG).Bold(true)
	idleStyle := lipgloss.NewStyle().Foreground(t.P.FGFade)

	end := w
	if showState {
		end = w - tabTail - textw.Width(state) - 1
	}
	// One style per cell, so the overlays (labels, marks, ellipses) stay
	// simple; runs of one style render together below.
	type cell struct {
		r  string
		st *lipgloss.Style
	}
	cells := make([]cell, max(0, end))
	for i := range cells {
		cells[i] = cell{"─", &rule}
	}
	put := func(x int, r string, st *lipgloss.Style) {
		if x >= 0 && x < len(cells) {
			cells[x] = cell{r, st}
		}
	}
	byTab := make(map[WorkspaceTab]string, len(tabs))
	for i, tab := range tabs {
		byTab[tab] = labels[i]
	}
	space := lipgloss.NewStyle()
	for _, hit := range hits {
		put(hit.X0-1, " ", &space)
		put(hit.X1, " ", &space)
		style := &idleStyle
		if hit.Tab == active {
			style = &activeStyle
			put(hit.X0-2, "━", &heavy)
			put(hit.X1+1, "━", &heavy)
		}
		shortcut, _, _ := strings.Cut(byTab[hit.Tab], " ")
		label := []rune(ansi.Truncate(byTab[hit.Tab], hit.X1-hit.X0, "…"))
		for i, r := range label {
			st := style
			if i < len(shortcut) {
				st = &key
			}
			put(hit.X0+i, string(r), st)
		}
	}
	if left {
		put(0, "…", &rule)
	}
	if right && len(hits) > 0 {
		put(hits[len(hits)-1].X1+2, "…", &rule)
	}
	var out strings.Builder
	for i := 0; i < len(cells); {
		j := i
		var run strings.Builder
		for j < len(cells) && cells[j].st == cells[i].st {
			run.WriteString(cells[j].r)
			j++
		}
		out.WriteString(cells[i].st.Render(run.String()))
		i = j
	}
	if showState {
		out.WriteString(" " + state + " " + rule.Render(strings.Repeat("─", tabTail-1)))
	}
	return out.String()
}

// IdlePlaceholder is the body fallback when a tab has no content yet
// (e.g. Logs tab is selected but no pod has been targeted, or a load
// hasn't landed). Centralised so all idle states share one register.
func IdlePlaceholder(t *theme.Theme, w, h int, message string) string {
	return components.IllustratedEmpty(t, w, h, components.BeaconArt, message, "")
}

// detailsSplitWidth is where Details has room for two columns: what the
// resource is on the left, what happened to it and what it touches on the
// right.
const detailsSplitWidth = 120

// RenderDetails lays out a resource page's Details tab in w × h cells. Facts
// and relationships get only the rows they need; recent events receive the
// rest, for readable messages. Connected records where it landed so clicks
// can find its rows.
func RenderDetails(c *tuictx.Ctx, w, h int, inspector *Inspector, activity *Activity, connected *Connected) string {
	inspector.HeaderOutside = true
	if w >= detailsSplitWidth && h >= 8 {
		leftW := w * 11 / 20
		rightW := w - leftW
		connH := 3
		if connected.snap != nil {
			body := connected.renderBody(c, components.PanelContentWidth(rightW), h/2)
			connH = min(h/2, max(3, lipgloss.Height(body)+2))
		}
		// Events take the height they need and Connected follows them, so
		// the column reads as one list of sections rather than one pinned
		// to the top and one to the bottom.
		actH := h - connH
		if lines := lipgloss.Height(activity.renderBody(c, components.PanelContentWidth(rightW), actH)); lines+2 < actH {
			actH = lines + 2 // the title, the events, a row of air
			connH = h - actH
		}
		connected.PanelTop, connected.PanelLeft = actH, leftW
		right := textw.JoinVertical(lipgloss.Left, activity.View(c, rightW, actH), connected.View(c, rightW, connH))
		return textw.JoinHorizontal(lipgloss.Top, inspector.View(c, leftW, h), right)
	}
	connected.PanelLeft = 0
	if h < 6 {
		return inspector.View(c, w, h)
	}
	innerW := components.PanelContentWidth(w)
	if h < 9 {
		return textw.JoinVertical(lipgloss.Left, inspector.View(c, w, 3), activity.View(c, w, h-3))
	}
	connH := 3
	if connected.snap != nil {
		body := connected.renderBody(c, innerW, 3)
		connH = min(5, max(3, lipgloss.Height(body)+2))
	}
	activityMin := 3
	if h >= 11 {
		activityMin = 5
	}
	connH = min(connH, h-6)
	activityMin = min(activityMin, h-connH-3)
	inspH := 3
	if inspector.snap != nil {
		body := inspector.renderBody(c, innerW, 15)
		inspH = min(17, max(3, lipgloss.Height(body)+2))
	}
	inspH = min(inspH, h-connH-activityMin)
	actH := h - inspH - connH
	connected.PanelTop = inspH + actH
	return textw.JoinVertical(lipgloss.Left,
		inspector.View(c, w, inspH),
		activity.View(c, w, actH),
		connected.View(c, w, connH),
	)
}
