package sections

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/YunusAndreasson/safina/internal/tui/components"
	"github.com/YunusAndreasson/safina/internal/tui/textw"
	"github.com/YunusAndreasson/safina/internal/tui/theme"
	"github.com/YunusAndreasson/safina/internal/tui/tuictx"
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
	WSTabMetrics
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
	case WSTabMetrics:
		return "m Metrics"
	case WSTabInspector:
		return "1 Inspector"
	case WSTabLogs:
		return "l Logs"
	case WSTabEvents:
		return "e Events"
	case WSTabYaml:
		return "d YAML"
	case WSTabChat:
		return "i Chat"
	case WSTabHot:
		return "h Hot pods"
	case WSTabForwards:
		return "⌃f Forwards"
	case WSTabDrain:
		return "Drain"
	}
	return ""
}

// WSTabAvailability describes which tabs the workspace should expose for
// the current selection / app state. Standard tabs (Inspector / Logs /
// Events / Yaml / Chat) are mostly always-on (the LLM is part of the
// operating surface, not a separate mode); pinned tabs (Hot / Forwards /
// Drain) appear only while their underlying op is live.
type WSTabAvailability struct {
	Metrics  bool
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
	if a.Metrics {
		tabs = append(tabs, WSTabMetrics)
	}
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
			n += textw.Width(tabs[i].Label())
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
		next := pos + textw.Width(tab.Label())
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
	activeStyle := lipgloss.NewStyle().Foreground(t.P.FG).Bold(true)
	idleStyle := lipgloss.NewStyle().Foreground(t.P.FGFade)
	sepStyle := lipgloss.NewStyle().Foreground(t.P.Dim)
	parts := make([]string, 0, len(hits))
	for _, hit := range hits {
		style := idleStyle
		if hit.Tab == active {
			style = activeStyle
		}
		label := hit.Tab.Label()
		if shortcut, title, ok := strings.Cut(label, " "); ok {
			label = t.HelpKey.Render(shortcut) + " " + style.Render(title)
		} else {
			label = style.Render(label)
		}
		parts = append(parts, components.TruncPad(label, hit.X1-hit.X0))
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
// single outer panel. The border title identifies the resource; the tab
// strip identifies the view, without repeating it in the border.
func RenderWorkspacePane(t *theme.Theme, w, h int, title, tabStrip, body string, focused bool) string {
	inner := components.PanelContentWidth(w)
	header := RenderWorkspaceBodyHeader(t, inner)
	content := textw.JoinVertical(lipgloss.Left, tabStrip, header, body)
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
	return components.IllustratedEmpty(t, w, h, components.BeaconArt, message, "")
}

// RenderInspectorTab gives facts and relationships only the rows they need;
// Activity receives the remaining height for readable event messages.
func RenderInspectorTab(c *tuictx.Ctx, w, h int, inspector *Inspector, activity *Activity, connected *Connected) string {
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
