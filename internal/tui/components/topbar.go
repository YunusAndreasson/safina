package components

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/yunus/safina/internal/tui/theme"
)

// Topbar renders the one-row title bar that frames the whole app.
//
//	🚢 safina  │  ⎈ prod-east-1 ▸ ◇ my-app          ↻ 3s   14:32:07
//
// Left: ship glyph + app name (the logo). Middle: the breadcrumb of the
// user's current location in the tree — that's the "where am I" answer
// users instinctively scan the top of the screen for. Right: a refresh-age
// indicator (when in a cluster) plus a wall-clock the root ticks once per
// second.
//
// The breadcrumb gets whatever horizontal budget is left after the fixed
// elements are placed; BreadcrumbStyled handles truncation when the path
// overflows. refreshAge is empty when there's no live data ("↻" hidden).
func Topbar(t *theme.Theme, w int, crumbs []Crumb, refreshAge, clock string) string {
	bar := lipgloss.NewStyle().
		Background(t.P.BG2).
		Foreground(t.P.FG).
		Width(w).
		Padding(0, 1)

	// Logo: ship + name read together. Cyan-bold matches the existing title
	// accent so the topbar feels like part of the same design system.
	// Safina means "ship" in Arabic — the emoji nods at the project's name.
	logo := lipgloss.NewStyle().Foreground(t.P.Cyan).Bold(true).
		Background(t.P.BG2).
		Render("🚢 safina")

	// A vertical bar between the logo and the breadcrumb signals "this side
	// is the app, that side is your location" — different visual register
	// than the breadcrumb's own ▸ separator.
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

	// Width math: bar.Padding(0,1) eats two cells. Compute the breadcrumb's
	// budget as whatever's left after the logo, separator, clock, refresh
	// pill, and a minimum gap so things never touch.
	innerW := w - 2
	if innerW < 1 {
		innerW = 1
	}
	const minGap = 4
	rightW := lipgloss.Width(clockCell)
	if refreshCell != "" {
		rightW += lipgloss.Width(refreshCell) + 3 // 3-cell gap between pill and clock
	}
	usedW := lipgloss.Width(logo) + lipgloss.Width(logoSep) + rightW + minGap
	crumbBudget := innerW - usedW
	if crumbBudget < 4 {
		crumbBudget = 4
	}

	// Breadcrumb has its own hierarchy: trail = faded (where I came from),
	// separators = muted (visual glue, not content), current = FG-bold (the
	// loudest answer to "where am I right now"). This makes the topbar do
	// real informational work instead of just labelling.
	crumbCell := ""
	if len(crumbs) > 0 {
		trailStyle := lipgloss.NewStyle().Foreground(t.P.FGFade).Background(t.P.BG2)
		currentStyle := lipgloss.NewStyle().Foreground(t.P.FG).Background(t.P.BG2).Bold(true)
		sepStyle := lipgloss.NewStyle().Foreground(t.P.Dim).Background(t.P.BG2)
		crumbCell = BreadcrumbStyled(crumbs, crumbBudget, trailStyle, currentStyle, sepStyle)
	}

	right := clockCell
	if refreshCell != "" {
		spacer := lipgloss.NewStyle().Background(t.P.BG2).Render("   ")
		right = refreshCell + spacer + clockCell
	}

	consumed := lipgloss.Width(logo) + lipgloss.Width(logoSep) +
		lipgloss.Width(crumbCell) + lipgloss.Width(right)
	pad := innerW - consumed
	if pad < 1 {
		pad = 1
	}
	gap := lipgloss.NewStyle().Background(t.P.BG2).Render(strings.Repeat(" ", pad))

	return bar.Render(logo + logoSep + crumbCell + gap + right)
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
