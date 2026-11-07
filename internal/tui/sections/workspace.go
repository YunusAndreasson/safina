package sections

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/yunus/safina/internal/tui/components"
	"github.com/yunus/safina/internal/tui/theme"
	"github.com/yunus/safina/internal/tui/tuictx"
)

// WorkspaceTab enumerates the right-pane tabs. Inspector is the default
// (the selection summary); the rest are activated by their verb keys and
// stay open until the operator switches away — the tree pane remains
// usable the entire time, so a tail-logs-while-scrolling-tree flow just
// works. Chat is also a tab so closing it is the same as closing any
// other tab: switch to Inspector (Esc, or 1).
type WorkspaceTab int

const (
	WSTabInspector WorkspaceTab = iota
	WSTabLogs
	WSTabEvents
	WSTabYaml
	WSTabChat
	WSTabHot
	WSTabForwards
	WSTabDrain
)

// Label uses a separate key and sentence-case name for every tab.
func (t WorkspaceTab) Label() string {
	switch t {
	case WSTabInspector:
		return "1 Inspector"
	case WSTabLogs:
		return "l Logs"
	case WSTabEvents:
		return "e Events"
	case WSTabYaml:
		return "d Describe"
	case WSTabChat:
		return "i Chat"
	case WSTabHot:
		return "h Hot pods"
	case WSTabForwards:
		return "⌃f Forwards"
	case WSTabDrain:
		return "D Drain"
	}
	return ""
}

// WSTabAvailability describes which tabs the workspace should expose for
// the current selection / app state. Standard tabs (Inspector / Logs /
// Events / Yaml / Chat) are mostly always-on (the LLM is part of the
// operating surface, not a separate mode); pinned tabs (Hot / Forwards /
// Drain) appear only while their underlying op is live.
type WSTabAvailability struct {
	Logs     bool
	Events   bool
	Yaml     bool
	Chat     bool
	Hot      bool
	Forwards bool
	Drain    bool
}

// AvailableTabs returns the ordered slice of tabs to render given the
// availability flags. Inspector is always first; Chat sits to the right
// of the standard data tabs so it reads as "ask about this thing" after
// the inspect/logs/events/yaml verbs. Pinned tabs (Hot / Forwards /
// Drain) come last and only appear while their op is active.
func AvailableTabs(a WSTabAvailability) []WorkspaceTab {
	tabs := []WorkspaceTab{WSTabInspector}
	if a.Logs {
		tabs = append(tabs, WSTabLogs)
	}
	if a.Events {
		tabs = append(tabs, WSTabEvents)
	}
	if a.Yaml {
		tabs = append(tabs, WSTabYaml)
	}
	if a.Chat {
		tabs = append(tabs, WSTabChat)
	}
	if a.Hot {
		tabs = append(tabs, WSTabHot)
	}
	if a.Forwards {
		tabs = append(tabs, WSTabForwards)
	}
	if a.Drain {
		tabs = append(tabs, WSTabDrain)
	}
	return tabs
}

// TabHitbox describes the on-strip x-range one tab occupies after rendering.
// Used by the mouse-click router to map a column to a tab so left-clicking a
// label switches to it (the same affordance the (l)/(e)/(y) hotkeys already
// expose, surfaced for operators who reach for the mouse).
//
// X0 is inclusive, X1 is exclusive — half-open interval matches the way
// MouseClickMsg.X is reported (zero-based column). Tab is the WorkspaceTab
// value to activate when X0 ≤ click_x < X1.
type TabHitbox struct {
	Tab WorkspaceTab
	X0  int
	X1  int
}

// workspaceTabLayout is shared by rendering and hit testing. When space is
// limited, keep a contiguous window containing the active tab visible.
func workspaceTabLayout(tabs []WorkspaceTab, active WorkspaceTab, w int) (hits []TabHitbox, left, right bool) {
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
			n += lipgloss.Width(tabs[i].Label())
			if i > start {
				n += 3
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
	for _, tab := range tabs[start:end] {
		next := pos + lipgloss.Width(tab.Label())
		hits = append(hits, TabHitbox{Tab: tab, X0: pos, X1: next})
		pos = next + 3
	}
	return hits, start > 0, end < len(tabs)
}

// WorkspaceTabHitboxes returns precisely the labels visible in the strip.
func WorkspaceTabHitboxes(tabs []WorkspaceTab, active WorkspaceTab, w int) []TabHitbox {
	hits, _, _ := workspaceTabLayout(tabs, active, w)
	return hits
}

// RenderWorkspaceTabStrip keeps the active view visible even in narrow panes.
func RenderWorkspaceTabStrip(t *theme.Theme, tabs []WorkspaceTab, active WorkspaceTab, w int) string {
	hits, left, right := workspaceTabLayout(tabs, active, w)
	activeStyle := lipgloss.NewStyle().Foreground(t.P.Cyan).Bold(true)
	idleStyle := lipgloss.NewStyle().Foreground(t.P.FGFade)
	sepStyle := lipgloss.NewStyle().Foreground(t.P.Dim)
	parts := make([]string, 0, len(hits))
	for _, hit := range hits {
		style := idleStyle
		if hit.Tab == active {
			style = activeStyle
		}
		parts = append(parts, style.Render(components.TruncPad(hit.Tab.Label(), hit.X1-hit.X0)))
	}
	strip := strings.Join(parts, sepStyle.Render("   "))
	if left {
		strip = sepStyle.Render("… ") + strip
	}
	if right {
		strip += sepStyle.Render(" …")
	}
	return strip
}

// RenderWorkspaceBodyHeader returns a thin underline row separating the
// tab strip from the body content. Same visual register as a Notion-style
// tabbed editor — the strip floats over a hairline.
func RenderWorkspaceBodyHeader(t *theme.Theme, w int) string {
	return lipgloss.NewStyle().Foreground(t.P.Dim).Render(strings.Repeat("─", w))
}

// RenderWorkspacePane wraps the tab strip + body content in the workspace's
// single outer panel. The panel's title is derived from the active tab —
// when on Inspector, it's the selection breadcrumb leaf; when on Logs, the
// section's PaneTitle ("logs · podname"). This way the title row doubles
// as a contextual "what am I looking at" line, while the tab strip on the
// first content row stays consistent across all tabs.
func RenderWorkspacePane(t *theme.Theme, w, h int, title, tabStrip, body string, focused bool) string {
	inner := components.PanelContentWidth(w)
	header := RenderWorkspaceBodyHeader(t, inner)
	content := lipgloss.JoinVertical(lipgloss.Left, tabStrip, header, body)
	return components.PanelWithAccent(t, title, w, h, content, focused, t.P.Cyan, t.P.Cyan)
}

// WorkspaceBodyHeight returns the inner height available for tab body
// content given a workspace pane of outer height h. Reserves the panel
// borders (2) + tab strip (1) + hairline (1).
func WorkspaceBodyHeight(h int) int {
	bh := h - 4
	if bh < 1 {
		bh = 1
	}
	return bh
}

// WorkspaceBodyWidth returns the inner width for tab body content.
func WorkspaceBodyWidth(w int) int {
	return components.PanelContentWidth(w)
}

// IdlePlaceholder is the body fallback when a tab has no content yet
// (e.g. Logs tab is selected but no pod has been targeted, or a load
// hasn't landed). Centralised so all idle states share one register.
func IdlePlaceholder(t *theme.Theme, w, h int, message string) string {
	box := lipgloss.NewStyle().Width(w).Height(h).Align(lipgloss.Center, lipgloss.Center)
	return box.Render(t.Idle.Render(message))
}

// RenderInspectorTab stacks Inspector + Activity + Connected sub-panels
// inside the workspace body. Uses a φ-flavored split: facts (top 38%)
// then activity flow (62% of remainder) then connected (the rest), so
// the eye lands on identity first and scans downward to "what's
// happening" and "what's wired in".
func RenderInspectorTab(c *tuictx.Ctx, w, h int, inspector *Inspector, activity *Activity, connected *Connected) string {
	if h < 6 {
		return inspector.View(c, w, h)
	}
	// Top: Inspector — content-bounded, capped so a tall workspace doesn't
	// pad a metadata snapshot with empty space. The cap is sized to fit
	// title + status + LIVE USAGE (4 rows) + one typical metadata section,
	// trusting Activity to absorb the rest of the height.
	const inspMax = 17
	inspH := h * 38 / 100
	if inspH > inspMax {
		inspH = inspMax
	}
	if inspH < 6 {
		inspH = 6
	}
	rem := h - inspH
	// Activity gets ~62% of the remainder — the flow-pulse the operator
	// actively scans. Connected gets the rest (counts of related resources).
	actH := rem * 62 / 100
	if actH < 5 {
		actH = 5
	}
	connH := rem - actH
	if connH < 4 {
		connH = 4
		actH = rem - connH
	}
	return lipgloss.JoinVertical(lipgloss.Left,
		inspector.View(c, w, inspH),
		activity.View(c, w, actH),
		connected.View(c, w, connH),
	)
}
