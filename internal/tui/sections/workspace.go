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

// Label returns the tab's visible name. Tabs that have a hotkey embed the
// letter in parentheses (vim-discoverable, also matches GNU readline
// labelling) — operators can scan the strip and see exactly which key
// activates each tab without consulting `?`.
func (t WorkspaceTab) Label() string {
	switch t {
	case WSTabInspector:
		return "(1) Inspector"
	case WSTabLogs:
		return "(l)ogs"
	case WSTabEvents:
		return "(e)vents"
	case WSTabYaml:
		return "(d)escribe"
	case WSTabChat:
		return "(i) Chat"
	case WSTabHot:
		return "(H)ot"
	case WSTabForwards:
		return "(⌃f) Forwards"
	case WSTabDrain:
		return "(D)rain"
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

// WorkspaceTabHitboxes recomputes the strip layout (same math as
// RenderWorkspaceTabStrip) and returns the per-tab column ranges within the
// strip's coordinate space (origin = strip's first cell). Caller adds the
// strip's screen-origin x to translate back to terminal coordinates.
//
// Returns nil when the strip falls back to its truncated single-tab form —
// the click target there would be the active tab only and there's no useful
// switching to do, so we don't generate a hitbox.
func WorkspaceTabHitboxes(tabs []WorkspaceTab, w int) []TabHitbox {
	if len(tabs) == 0 {
		return nil
	}
	// Mirror RenderWorkspaceTabStrip's layout: each tab's rendered label is
	// "❯ Label" for the active tab and "Label" for others, joined by a 3-cell
	// separator. For hit-testing we treat the active tab's "❯ " prefix as part
	// of its label so the X0..X1 range catches clicks on the cursor glyph too.
	const sepW = 3
	hits := make([]TabHitbox, 0, len(tabs))
	pos := 0
	for i, tab := range tabs {
		label := tab.Label()
		// Style/render width matches RenderWorkspaceTabStrip: active tab adds
		// "❯ " (2 cells), inactive tabs are plain. We don't actually pick the
		// active styling here — the *width* is identical either way for hit
		// purposes because both render the literal label glyphs; we approximate
		// with the inactive width to avoid a circular dep on `active`. The
		// 2-cell delta on the active tab makes its hit zone 2 wider than
		// strictly accurate, which is the friendlier failure mode (slightly
		// generous click target on the focused tab).
		segW := lipgloss.Width(label)
		hits = append(hits, TabHitbox{Tab: tab, X0: pos, X1: pos + segW})
		pos += segW
		if i < len(tabs)-1 {
			pos += sepW
		}
	}
	// If the strip would have truncated, the hitboxes past the visible region
	// are meaningless — the caller renders "…" instead of trailing labels.
	// Return only the prefix that fits.
	if pos <= w {
		return hits
	}
	cut := hits[:0]
	for _, h := range hits {
		if h.X1 <= w {
			cut = append(cut, h)
		}
	}
	return cut
}

// RenderWorkspaceTabStrip renders the tab strip — a one-row band where
// the active tab is bright cyan-bold prefixed with ❯, inactive tabs are
// dim, and the line as a whole serves as the panel's first content row.
// Width-aware: if tabs would overflow, drops trailing tabs and appends "…".
func RenderWorkspaceTabStrip(t *theme.Theme, tabs []WorkspaceTab, active WorkspaceTab, w int) string {
	if len(tabs) == 0 {
		return ""
	}
	activeStyle := lipgloss.NewStyle().Foreground(t.P.Cyan).Bold(true)
	idleStyle := lipgloss.NewStyle().Foreground(t.P.FGFade)
	arrowStyle := lipgloss.NewStyle().Foreground(t.P.Pink).Bold(true)
	sepStyle := lipgloss.NewStyle().Foreground(t.P.Dim)

	parts := make([]string, 0, len(tabs))
	for _, tab := range tabs {
		label := tab.Label()
		if tab == active {
			parts = append(parts, arrowStyle.Render("❯ ")+activeStyle.Render(label))
		} else {
			parts = append(parts, idleStyle.Render(label))
		}
	}
	sep := sepStyle.Render("   ")
	strip := strings.Join(parts, sep)

	if lipgloss.Width(strip) <= w {
		return strip
	}
	// Try dropping trailing tabs one at a time until it fits, appending "…".
	for keep := len(parts) - 1; keep >= 1; keep-- {
		candidate := strings.Join(parts[:keep], sep) + sepStyle.Render(" …")
		if lipgloss.Width(candidate) <= w {
			return candidate
		}
	}
	// Last resort: even one tab won't fit — render the active label clipped.
	for i, tab := range tabs {
		if tab == active {
			return lipgloss.NewStyle().MaxWidth(w).Render(parts[i])
		}
	}
	return lipgloss.NewStyle().MaxWidth(w).Render(parts[0])
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
	// Top: Inspector — content-bounded, capped at 13 rows so a tall workspace
	// doesn't pad a metadata snapshot with empty space.
	const inspMax = 13
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
