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

// TopbarInput is everything the one-row title bar can show. The bar has two
// zones: on the left, where the operator is and how the thing on screen is
// doing; on the right, how the world is (what just happened, what waits,
// how fresh the data is, how the cluster is).
type TopbarInput struct {
	Crumbs []Crumb
	// Status follows the leaf crumb on a resource page: kind · state ·
	// replicas, pre-styled. StatusShort is its narrow form (kind · state).
	Status, StatusShort string
	// Notice is transient feedback, pre-styled: a ✓ confirmation or the
	// operations in flight. It never drops; it truncates.
	Notice    string
	Forwards  int
	Proposals int
	// Freshness is "paused", "↻ 12s" or "cached"; empty while data is live.
	Freshness string
	Health    ClusterHealth
	// Brandless drops the wordmark, for the location row under the full
	// header, which carries the mark itself.
	Brandless bool
}

// topbarKey is the comparable part of TopbarInput, for the memo.
type topbarKey struct {
	t                           *theme.Theme
	w                           int
	status, statusShort, notice string
	forwards, proposals         int
	freshness                   string
	health                      ClusterHealth
	brandless                   bool
}

// Topbar renders the one-row title bar that frames the whole app.
//
//	▌safina   prod ▸ shop ▸ Workloads ▸ api  Deployment · Healthy · 2/2   ✓ restarted  2 fwd  Nodes 5/5 Pods 142/150
//
// The breadcrumb runs a brightness ramp so the eye is led to the leaf, and
// the page's status reads on from the leaf it describes. When width runs
// short, the right zone gives way first (cluster health, then freshness),
// then the status shrinks, then the ancestor crumbs truncate. The brand, the
// leaf and the notice stay.
func Topbar(t *theme.Theme, w int, in TopbarInput) string {
	key := topbarKey{t, w, in.Status, in.StatusShort, in.Notice, in.Forwards, in.Proposals, in.Freshness, in.Health, in.Brandless}
	lastTopbar.Lock()
	defer lastTopbar.Unlock()
	if lastTopbar.ok && lastTopbar.key == key && slices.Equal(lastTopbar.crumbs, in.Crumbs) {
		return lastTopbar.out
	}
	out := topbar(t, w, in)
	lastTopbar.key, lastTopbar.crumbs, lastTopbar.out, lastTopbar.ok = key, slices.Clone(in.Crumbs), out, true
	return out
}

// lastTopbar is the bar drawn last: it changes on navigation and status,
// not on the cursor moves and ticks that redraw every frame.
var lastTopbar struct {
	sync.Mutex
	key    topbarKey
	crumbs []Crumb
	out    string
	ok     bool
}

func topbar(t *theme.Theme, w int, in TopbarInput) string {
	bg := lipgloss.NewStyle().Background(t.P.BG)
	bar := bg.Foreground(t.P.FG).Width(w).Padding(0, 1)

	// The wordmark is a quiet landmark behind the site's mark: a half block
	// in the beacon cyan, the one cell of colour the brand spends.
	logo := bg.Foreground(t.P.Cyan).Render("▌") +
		bg.Foreground(t.P.FGFade).Bold(true).Render("safina") + bg.Render("   ")
	if in.Brandless {
		// Under the full header the location is the page frame's title;
		// the frame anchors it, and the header carries the mark.
		logo = ""
	}

	innerW := max(1, w-2)
	const minGap = 2
	spacer := bg.Render("   ")

	notice := in.Notice
	pill := func(n int, unit string) string {
		if n <= 0 {
			return ""
		}
		return bg.Foreground(t.P.Pink).Bold(true).Render(fmt.Sprintf("%d %s", n, unit))
	}
	proposals := pill(in.Proposals, "proposals")
	if in.Proposals == 1 {
		proposals = pill(1, "proposal")
	}
	forwards := pill(in.Forwards, "fwd")
	freshness := ""
	if in.Freshness != "" {
		freshness = bg.Foreground(t.P.Yellow).Render(in.Freshness)
	}
	health := ""
	if in.Health.Show {
		health = renderClusterHealth(t, in.Health)
	}
	status := in.Status

	right := func() string {
		cells := []string{}
		for _, cell := range []string{notice, proposals, forwards, freshness, health} {
			if cell != "" {
				cells = append(cells, cell)
			}
		}
		return strings.Join(cells, spacer)
	}
	statusW := func() int {
		if status == "" {
			return 0
		}
		return textw.Width(status) + 2
	}
	locationMin := min(24, innerW-textw.Width(logo)-minGap)
	crumbBudget := func() int { return innerW - textw.Width(logo+right()) - statusW() - minGap }
	// Give way in order: the cluster's health, the data's age, then the long
	// status, then the status altogether. The crumbs never go below their
	// minimum while something on the right can still yield.
	for _, yield := range []func(){
		func() { health = "" },
		func() { freshness = "" },
		func() { status = in.StatusShort },
		func() { status = "" },
	} {
		if crumbBudget() >= locationMin {
			break
		}
		yield()
	}
	if crumbBudget() < locationMin && notice != "" {
		notice = ansi.Truncate(notice, max(8, textw.Width(notice)-(locationMin-crumbBudget())), "…")
	}
	budget := max(1, crumbBudget())

	crumbCell := ""
	if len(in.Crumbs) > 0 {
		ramp := []lipgloss.Style{
			bg.Foreground(t.P.Dim),
			bg.Foreground(t.P.FGFade),
			bg.Foreground(t.P.FG),
			bg.Foreground(t.P.FG).Bold(true),
		}
		sepStyle := bg.Foreground(t.P.Dim)
		crumbCell = BreadcrumbRamped(in.Crumbs, budget, ramp, sepStyle)
		for i, crumb := range in.Crumbs {
			if !crumb.Pinned {
				continue
			}
			cluster := ansi.Truncate(crumb.Label, budget, "…")
			crumbCell = ramp[len(ramp)-1].Render(cluster)
			remaining := budget - textw.Width(cluster) - 3
			if i+1 < len(in.Crumbs) && remaining > 3 {
				crumbCell += sepStyle.Render(" ▸ ") + BreadcrumbRamped(in.Crumbs[i+1:], remaining, ramp, sepStyle)
			}
			break
		}
	}
	if status != "" {
		crumbCell += bg.Render("  ") + status
	}

	r := right()
	pad := max(1, innerW-textw.Width(logo)-textw.Width(crumbCell)-textw.Width(r))
	return bar.Render(logo + crumbCell + bg.Width(pad).Render("") + r)
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
