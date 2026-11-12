package components

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/YunusAndreasson/safina/internal/tui/textw"
	"github.com/YunusAndreasson/safina/internal/tui/theme"
)

// frameRuleMin is the shortest gap FrameEdge draws as a rule; a narrower
// gap keeps its spaces.
const frameRuleMin = 6

// FrameEdge turns a padded one-row line (a cell of air at each end, w
// cells in all) into the top or bottom edge of the page frame:
//
//	╭─ minikube ▸ experiment ▸ Workloads ────────────── ✓ restarted api ─╮
//	╰─ esc ‹ minikube ────────────────────────── ctrl+p search · ? help ─╯
//
// The line's widest run of blank cells becomes the rule, so the text stays
// where its renderer put it, set into the frame like a title on a border.
func FrameEdge(t *theme.Theme, line string, w int, top bool) string {
	left, right := "╰", "╯"
	if top {
		left, right = "╭", "╮"
	}
	rule := lipgloss.NewStyle().Foreground(t.P.Dim)
	dash := func(n int) string { return rule.Render(strings.Repeat("─", max(0, n))) }
	if w < 8 {
		return rule.Render(left + strings.Repeat("─", max(0, w-2)) + right)
	}
	if lw := textw.Width(line); lw < w {
		line += strings.Repeat(" ", w-lw)
	}
	content := ansi.Cut(line, 1, w-1) // w-2 cells, without the air
	a, b := blankRun(ansi.Strip(content))
	n := b - a
	switch {
	case n < frameRuleMin:
		// No room for a rule: the text, cut to fit between the corners.
		inner := ansi.Truncate(content, w-6, "…")
		return rule.Render(left+"─") + " " + inner + strings.Repeat(" ", max(0, w-6-textw.Width(inner))) + " " + rule.Render("─"+right)
	case a == 0 && b >= w-2:
		return rule.Render(left + strings.Repeat("─", w-2) + right)
	case a == 0:
		// Nothing on the left: the rule leads up to the right-hand text.
		return rule.Render(left+"─") + dash(n-4) + " " + ansi.Cut(content, b, w-2) + " " + rule.Render("─"+right)
	case b >= w-2:
		// The blank run is the line's tail: the rule runs into the corner.
		return rule.Render(left+"─") + " " + ansi.Cut(content, 0, a) + " " + dash(n-4) + rule.Render("─"+right)
	default:
		return rule.Render(left+"─") + " " + ansi.Cut(content, 0, a) + " " + dash(n-6) + " " + ansi.Cut(content, b, w-2) + " " + rule.Render("─"+right)
	}
}

// FrameSides sets each row of a page w-2 cells wide between the frame's
// sides.
func FrameSides(t *theme.Theme, page string) string {
	side := lipgloss.NewStyle().Foreground(t.P.Dim).Render("│")
	rows := strings.Split(page, "\n")
	for i, r := range rows {
		rows[i] = side + r + side
	}
	return strings.Join(rows, "\n")
}

// blankRun finds the widest run of spaces in plain text, in cells, as the
// half-open range [a, b).
func blankRun(plain string) (int, int) {
	bestA, bestB := 0, 0
	cell, runA := 0, -1
	for _, r := range plain {
		cw := max(1, textw.Width(string(r)))
		if r == ' ' {
			if runA < 0 {
				runA = cell
			}
		} else {
			if runA >= 0 && cell-runA > bestB-bestA {
				bestA, bestB = runA, cell
			}
			runA = -1
		}
		cell += cw
	}
	if runA >= 0 && cell-runA > bestB-bestA {
		bestA, bestB = runA, cell
	}
	return bestA, bestB
}
