package components

import (
	"fmt"
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/yunus/safina/internal/tui/theme"
)

// ClusterHealth is the topbar's tiny dashboard module — the on-caller's
// "is anything on fire right now?" glance, rendered as a tight set of
// pills between the breadcrumb and the right-side chrome. Show==false
// (the zero value) suppresses the module entirely; safe to pass at the
// context picker where there is no cluster yet.
type ClusterHealth struct {
	Show          bool
	NodesReady    int
	NodesTotal    int
	PodsRunning   int
	PodsCrashing  int
	PodsPending   int
	PodsTotal     int
	CPUFrac       float64 // [0,1] cluster-wide used / allocatable
	MemFrac       float64
	PressureCount int
}

// Topbar renders the one-row title bar that frames the whole app.
//
//	▌ safina │ ⎈ prod-east-1 ▸ ◇ my-app    ⬢ 5/5 ◉ 142/150  ↯ 2 fwd   ↻ 3s   14:32:07
//
// Left: half-block glyph + app name (the brand). Middle: the breadcrumb of
// the user's current location in the tree, rendered with a brightness ramp
// so the eye is led to the leaf. Right: optional cluster-health pills,
// optional forwards pill, optional refresh-age indicator, and a wall-clock.
//
// The breadcrumb gets whatever horizontal budget is left after the fixed
// elements are placed; BreadcrumbRamped handles truncation when the path
// overflows. refreshAge is empty when there's no live data ("↻" hidden).
// forwardCount == 0 hides the forwards pill entirely.
func Topbar(t *theme.Theme, w int, crumbs []Crumb, refreshAge, clock string, forwardCount int, health ClusterHealth) string {
	bar := lipgloss.NewStyle().
		Background(t.P.BG2).
		Foreground(t.P.FG).
		Width(w).
		Padding(0, 1)

	// Logo: a single-cell half-block bar followed by the name. ▌ is
	// single-cell BMP, monospace-safe across every terminal. Cyan-bold
	// matches the title accent the focused-pane border anchors on.
	logo := lipgloss.NewStyle().Foreground(t.P.Cyan).Bold(true).
		Background(t.P.BG2).
		Render("▌ safina")

	// Vertical rule between the brand and the operator's location — a
	// different visual register from the breadcrumb's own ▸ separator.
	logoSep := lipgloss.NewStyle().Foreground(t.P.Dim).Background(t.P.BG2).
		Render("  │  ")

	clockCell := lipgloss.NewStyle().
		Foreground(t.P.FGFade).
		Background(t.P.BG2).
		Render(clock)

	// Refresh-age pill, only when we have live data. Color shifts with age
	// so the user feels staleness ramp up: dim under 6s (in step with the
	// 5s auto-refresh), yellow when something looks sluggish, red when it
	// looks stuck. Zero-value refreshAge hides the cell entirely.
	refreshCell := ""
	if refreshAge != "" {
		refreshCell = lipgloss.NewStyle().
			Foreground(refreshColor(t, refreshAge)).
			Background(t.P.BG2).
			Render("↻ " + refreshAge)
	}

	// Active port-forwards pill. Hidden when zero; pink/bold when present
	// so the operator's eye picks it up immediately (the same pink used
	// for the modal-cursor accent).
	forwardCell := ""
	if forwardCount > 0 {
		forwardCell = lipgloss.NewStyle().
			Foreground(t.P.Pink).Bold(true).
			Background(t.P.BG2).
			Render(fmt.Sprintf("↯ %d fwd", forwardCount))
	}

	// Cluster health pill — the at-a-glance answer to "is anything on
	// fire right now". Rendered between the breadcrumb and the right-side
	// chrome so the operator's eye lands on it without scanning the rest
	// of the bar. Suppressed entirely when there is no cluster selected
	// or the first snapshot hasn't landed yet.
	healthCell := ""
	if health.Show {
		healthCell = renderClusterHealth(t, health)
	}

	// Width math: bar.Padding(0,1) eats two cells. Compute the breadcrumb's
	// budget as whatever's left after the logo, separator, clock, the two
	// optional pills, and a minimum gap so things never touch.
	innerW := w - 2
	if innerW < 1 {
		innerW = 1
	}
	const minGap = 4
	rightW := lipgloss.Width(clockCell)
	if refreshCell != "" {
		rightW += lipgloss.Width(refreshCell) + 3 // 3-cell gap between pill and clock
	}
	if forwardCell != "" {
		rightW += lipgloss.Width(forwardCell) + 3 // 3-cell gap between forward pill and the rest
	}
	if healthCell != "" {
		rightW += lipgloss.Width(healthCell) + 3 // 3-cell gap before the health pill
	}
	usedW := lipgloss.Width(logo) + lipgloss.Width(logoSep) + rightW + minGap
	crumbBudget := innerW - usedW
	if crumbBudget < 4 {
		crumbBudget = 4
	}

	// Breadcrumb runs a four-tier brightness ramp from oldest crumb to leaf
	// (Dim → FGFade → FG → Bold). The eye is led along the gradient to the
	// most recent segment — the answer to "where am I right now". Separators
	// stay muted so they read as glue, not content.
	crumbCell := ""
	if len(crumbs) > 0 {
		ramp := []lipgloss.Style{
			lipgloss.NewStyle().Foreground(t.P.Dim).Background(t.P.BG2),
			lipgloss.NewStyle().Foreground(t.P.FGFade).Background(t.P.BG2),
			lipgloss.NewStyle().Foreground(t.P.FG).Background(t.P.BG2),
			lipgloss.NewStyle().Foreground(t.P.FG).Background(t.P.BG2).Bold(true),
		}
		sepStyle := lipgloss.NewStyle().Foreground(t.P.Dim).Background(t.P.BG2)
		crumbCell = BreadcrumbRamped(crumbs, crumbBudget, ramp, sepStyle)
	}

	spacer := lipgloss.NewStyle().Background(t.P.BG2).Render("   ")
	right := clockCell
	if refreshCell != "" {
		right = refreshCell + spacer + clockCell
	}
	if forwardCell != "" {
		right = forwardCell + spacer + right
	}
	if healthCell != "" {
		right = healthCell + spacer + right
	}

	consumed := lipgloss.Width(logo) + lipgloss.Width(logoSep) +
		lipgloss.Width(crumbCell) + lipgloss.Width(right)
	pad := innerW - consumed
	if pad < 1 {
		pad = 1
	}
	gap := lipgloss.NewStyle().Background(t.P.BG2).Width(pad).Render("")

	return bar.Render(logo + logoSep + crumbCell + gap + right)
}

// renderClusterHealth composes the topbar's at-a-glance cluster summary:
//
//	⬢ 5/5  ◉ 142/150  CPU 34%  MEM 67%  ⚠2
//
// Each cell is colored independently so the operator sees the offending
// dimension first — yellow nodes if any are NotReady, red pods if any are
// CrashLoopBackOff, green CPU/MEM until they push past 50/80 percent. The
// pressure cell is hidden entirely when zero; surfacing it only when it's
// non-zero keeps the pill quiet on healthy clusters.
func renderClusterHealth(t *theme.Theme, h ClusterHealth) string {
	bg := lipgloss.NewStyle().Background(t.P.BG2)

	// Nodes pill: green when all ready, yellow when any not ready.
	nodesColor := t.P.Green
	if h.NodesReady < h.NodesTotal {
		nodesColor = t.P.Yellow
	}
	nodes := bg.Foreground(nodesColor).Bold(true).
		Render(fmt.Sprintf("⬢ %d/%d", h.NodesReady, h.NodesTotal))

	// Pods pill: red when anything is crashing, yellow when anything's
	// pending, green otherwise. CrashLoopBackOff outranks Pending — a
	// crash is a real-now problem, pending is "kubernetes is working on it".
	podsColor := t.P.Green
	switch {
	case h.PodsCrashing > 0:
		podsColor = t.P.Red
	case h.PodsPending > 0:
		podsColor = t.P.Yellow
	}
	pods := bg.Foreground(podsColor).Bold(true).
		Render(fmt.Sprintf("◉ %d/%d", h.PodsRunning, h.PodsTotal))

	// CPU / Mem pills: use the same green/yellow/red gradient as the row
	// sparklines so the topbar reads as a higher-level summary of the same
	// utilization band the operator sees in the body.
	cpu := bg.Foreground(t.UtilColor(h.CPUFrac)).Bold(true).
		Render(fmt.Sprintf("CPU %d%%", int(h.CPUFrac*100+0.5)))
	mem := bg.Foreground(t.UtilColor(h.MemFrac)).Bold(true).
		Render(fmt.Sprintf("MEM %d%%", int(h.MemFrac*100+0.5)))

	gap := bg.Render("  ")
	parts := []string{nodes, gap, pods, gap, cpu, gap, mem}
	if h.PressureCount > 0 {
		press := bg.Foreground(t.P.Red).Bold(true).
			Render(fmt.Sprintf("⚠%d", h.PressureCount))
		parts = append(parts, gap, press)
	}
	return strings.Join(parts, "")
}

// refreshColor maps a refresh-age string to a foreground color. Conservative
// thresholds: dim under 6s (auto-refresh interval is 5s, so a fresh load
// reads as a sub-second value most of the time), yellow when it stretches,
// red when something looks stuck. We parse the leading number out of the
// "Ns" / "Nm" age string instead of taking a duration so the topbar's
// caller can format the string however it wants.
func refreshColor(t *theme.Theme, age string) color.Color {
	// Shape: "<int><unit>" where unit ∈ {"s","m","h"}. Bail on anything else.
	if len(age) < 2 {
		return t.P.Dim
	}
	unit := age[len(age)-1]
	nStr := age[:len(age)-1]
	n := 0
	for _, r := range nStr {
		if r < '0' || r > '9' {
			return t.P.Dim
		}
		n = n*10 + int(r-'0')
	}
	switch unit {
	case 's':
		if n < 6 {
			return t.P.FGFade
		}
		if n < 15 {
			return t.P.Yellow
		}
		return t.P.Red
	case 'm', 'h':
		return t.P.Red
	}
	return t.P.Dim
}
