package components

import (
	"fmt"
	"slices"
	"strings"
	"sync"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/YunusAndreasson/safina/internal/tui/textw"
	"github.com/YunusAndreasson/safina/internal/tui/theme"
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
//	safina │ prod-east-1 ▸ my-app    Nodes 5/5 Pods 142/150  2 fwd
//
// Left: app name. Middle: the breadcrumb of
// the user's current location in the tree, rendered with a brightness ramp
// so the eye is led to the leaf. Right: optional cluster-health pills,
// and optional forwards pill.
//
// The breadcrumb gets whatever horizontal budget is left after the fixed
// elements are placed; BreadcrumbRamped handles truncation when the path
// overflows.
// forwardCount == 0 hides the forwards pill entirely.
func Topbar(t *theme.Theme, w int, crumbs []Crumb, forwardCount int, health ClusterHealth, cached bool) string {
	in := topbarInput{t, w, forwardCount, health, cached}
	lastTopbar.Lock()
	defer lastTopbar.Unlock()
	if lastTopbar.ok && lastTopbar.in == in && slices.Equal(lastTopbar.crumbs, crumbs) {
		return lastTopbar.out
	}
	out := topbar(t, w, crumbs, forwardCount, health, cached)
	lastTopbar.in, lastTopbar.crumbs, lastTopbar.out, lastTopbar.ok = in, slices.Clone(crumbs), out, true
	return out
}

type topbarInput struct {
	t            *theme.Theme
	w            int
	forwardCount int
	health       ClusterHealth
	cached       bool
}

// lastTopbar is the bar drawn last: it changes on navigation, not on the
// cursor moves and ticks that redraw every frame.
var lastTopbar struct {
	sync.Mutex
	in     topbarInput
	crumbs []Crumb
	out    string
	ok     bool
}

func topbar(t *theme.Theme, w int, crumbs []Crumb, forwardCount int, health ClusterHealth, cached bool) string {
	bar := lipgloss.NewStyle().
		Background(t.P.BG).
		Foreground(t.P.FG).
		Width(w).
		Padding(0, 1)

	// The wordmark is a quiet landmark; focus color belongs to the active pane.
	logo := lipgloss.NewStyle().Foreground(t.P.FGFade).Bold(true).
		Background(t.P.BG).
		Render("safina")

	// Vertical rule between the brand and the operator's location — a
	// different visual register from the breadcrumb's own ▸ separator.
	logoSep := lipgloss.NewStyle().Foreground(t.P.Dim).Background(t.P.BG).
		Render("  │  ")

	// Active port-forwards pill. Hidden when zero; pink/bold when present
	// so the operator's eye picks it up immediately (the same pink used
	// for the modal-cursor accent).
	forwardCell := ""
	if forwardCount > 0 {
		forwardCell = lipgloss.NewStyle().
			Foreground(t.P.Pink).Bold(true).
			Background(t.P.BG).
			Render(fmt.Sprintf("%d fwd", forwardCount))
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

	cachedCell := ""
	if cached {
		cachedCell = lipgloss.NewStyle().Foreground(t.P.Yellow).Background(t.P.BG).Render("cached")
	}

	// Width math: bar.Padding(0,1) eats two cells. Compute the breadcrumb's
	// budget as whatever's left after the logo, separator, the two
	// optional pills, and a minimum gap so things never touch.
	innerW := w - 2
	if innerW < 1 {
		innerW = 1
	}
	const minGap = 2
	spacer := lipgloss.NewStyle().Background(t.P.BG).Render("   ")
	renderRight := func() string {
		cells := []string{}
		for _, cell := range []string{healthCell, forwardCell, cachedCell} {
			if cell != "" {
				cells = append(cells, cell)
			}
		}
		return strings.Join(cells, spacer)
	}
	right := renderRight()
	locationMin := min(32, innerW-textw.Width(logo+logoSep)-minGap)
	budget := func() int { return innerW - textw.Width(logo+logoSep+right) - minGap }
	if budget() < locationMin {
		healthCell = ""
		right = renderRight()
	}
	crumbBudget := max(1, budget())

	// Breadcrumb runs a four-tier brightness ramp from oldest crumb to leaf
	// (Dim → FGFade → FG → Bold). The eye is led along the gradient to the
	// most recent segment — the answer to "where am I right now". Separators
	// stay muted so they read as glue, not content.
	crumbCell := ""
	if len(crumbs) > 0 {
		ramp := []lipgloss.Style{
			lipgloss.NewStyle().Foreground(t.P.Dim).Background(t.P.BG),
			lipgloss.NewStyle().Foreground(t.P.FGFade).Background(t.P.BG),
			lipgloss.NewStyle().Foreground(t.P.FG).Background(t.P.BG),
			lipgloss.NewStyle().Foreground(t.P.FG).Background(t.P.BG).Bold(true),
		}
		sepStyle := lipgloss.NewStyle().Foreground(t.P.Dim).Background(t.P.BG)
		crumbCell = BreadcrumbRamped(crumbs, crumbBudget, ramp, sepStyle)
		for i, crumb := range crumbs {
			if !crumb.Pinned {
				continue
			}
			cluster := ansi.Truncate(crumb.Label, crumbBudget, "…")
			crumbCell = ramp[len(ramp)-1].Render(cluster)
			remaining := crumbBudget - textw.Width(cluster) - 3
			if i+1 < len(crumbs) && remaining > 3 {
				crumbCell += sepStyle.Render(" ▸ ") + BreadcrumbRamped(crumbs[i+1:], remaining, ramp, sepStyle)
			}
			break
		}
	}

	consumed := textw.Width(logo) + textw.Width(logoSep) +
		textw.Width(crumbCell) + textw.Width(right)
	pad := innerW - consumed
	if pad < 1 {
		pad = 1
	}
	gap := lipgloss.NewStyle().Background(t.P.BG).Width(pad).Render("")

	return bar.Render(logo + logoSep + crumbCell + gap + right)
}

// renderClusterHealth composes the topbar's at-a-glance cluster summary:
//
//	Nodes 5/5  Pods 142/150  CPU 34%  MEM 67%  ⚠2
//
// Each cell is colored independently so the operator sees the offending
// dimension first — yellow nodes if any are NotReady, red pods if any are
// CrashLoopBackOff, green CPU/MEM until they push past 50/80 percent. The
// pressure cell is hidden entirely when zero; surfacing it only when it's
// non-zero keeps the pill quiet on healthy clusters.
func renderClusterHealth(t *theme.Theme, h ClusterHealth) string {
	bg := lipgloss.NewStyle().Background(t.P.BG)

	// Nodes pill: green when all ready, yellow when any not ready.
	nodesColor := t.P.Green
	if h.NodesReady < h.NodesTotal {
		nodesColor = t.P.Yellow
	}
	nodes := bg.Foreground(nodesColor).Bold(true).
		Render(fmt.Sprintf("Nodes %d/%d", h.NodesReady, h.NodesTotal))

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
		Render(fmt.Sprintf("Pods %d/%d", h.PodsRunning, h.PodsTotal))

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
