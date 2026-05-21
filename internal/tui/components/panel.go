package components

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/yunus/safina/internal/tui/theme"
)

// PanelContentWidth returns the usable width inside the panel (excluding the
// border) — i.e. the width Rows must occupy.
func PanelContentWidth(w int) int {
	cw := w - 2
	if cw < 1 {
		cw = 1
	}
	return cw
}

// PanelWithAccent renders a rounded/thick-bordered box with the title in the
// top edge. The content is expected to fill exactly PanelContentWidth(w)
// cells per line. w and h are the *outer* dimensions including borders.
//
//	╭─ Title ──────────────╮
//	│ content              │
//	╰──────────────────────╯
//
// When focused, the border uses ThickBorder painted with the theme's focus
// gradient anchored on borderAccent (Blue → borderAccent → Pink, or flat for
// warning anchors like Yellow/Red — see Theme.FocusGradient). When blurred,
// it falls back to RoundedBorder in dim. The title uses titleAccent (bold)
// on focus, dim otherwise.
//
// The gradient is recomputed per render but each cell pays a single
// lipgloss.Style allocation; for the panel sizes we render (≤ 200 cells of
// perimeter on a typical terminal) this is in the noise.
func PanelWithAccent(t *theme.Theme, title string, w, h int, content string, focused bool, borderAccent, titleAccent color.Color) string {
	if w < 4 {
		w = 4
	}
	if h < 2 {
		h = 2
	}

	if !focused {
		return renderFlatPanel(title, w, h, content, t.P.Dim, t.P.Dim)
	}

	// Same glyph set as the blurred path (renderFlatPanel uses
	// RoundedBorder). The focus signal is carried entirely by the per-cell
	// gradient color — the box shape is invariant. Earlier this path used
	// ThickBorder, which meant every focus swap snapped the border
	// characters between two sets (─│╭ ↔ ━┃┏) at the same moment the
	// color changed, reading as a flicker on Tab / state transitions
	// (e.g. tree → workspace when opening Logs). Unifying the glyphs
	// removes the snap; only the color register shifts.
	b := lipgloss.RoundedBorder()
	topGrad := t.FocusGradient(borderAccent, w)
	vGrad := t.FocusGradient(borderAccent, h)

	// Empty title means "draw a clean top border with no inserted gap". The
	// Inspector tab uses this path because the topbar's breadcrumb leaf
	// already names the current selection — repeating it on the panel rule
	// is duplication, not chrome.
	trimmed := strings.TrimSpace(title)
	titleStyle := lipgloss.NewStyle().Foreground(titleAccent).Bold(true)
	titleSeg := ""
	titleW := 0
	leadDashes := 1
	if trimmed != "" {
		titleSeg = titleStyle.Render(" " + trimmed + " ")
		titleW = lipgloss.Width(titleSeg)
	} else {
		leadDashes = 0
	}
	dashesAfterTitle := w - 2 - leadDashes - titleW
	if dashesAfterTitle < 1 {
		dashesAfterTitle = 1
		// Title would have overflowed; recompute width to actually fit.
		titleW = w - 2 - leadDashes - dashesAfterTitle
		if titleW < 0 {
			titleW = 0
		}
	}

	// Top edge: each border cell painted with its own gradient color so the
	// hue glides across the rule. The title spans columns
	// [leadDashes+1, leadDashes+titleW] and keeps its semantic color.
	var topB strings.Builder
	topB.WriteString(colorCell(topGrad[0], b.TopLeft))
	for i := range leadDashes {
		topB.WriteString(colorCell(topGrad[1+i], b.Top))
	}
	topB.WriteString(titleSeg)
	startAfter := 1 + leadDashes + titleW
	for i := range dashesAfterTitle {
		idx := startAfter + i
		if idx >= len(topGrad) {
			idx = len(topGrad) - 1
		}
		topB.WriteString(colorCell(topGrad[idx], b.Top))
	}
	topB.WriteString(colorCell(topGrad[len(topGrad)-1], b.TopRight))
	top := topB.String()

	// Bottom edge: same horizontal gradient, no title interruption.
	var botB strings.Builder
	botB.WriteString(colorCell(topGrad[0], b.BottomLeft))
	for i := 1; i < w-1; i++ {
		botB.WriteString(colorCell(topGrad[i], b.Bottom))
	}
	botB.WriteString(colorCell(topGrad[len(topGrad)-1], b.BottomRight))
	bot := botB.String()

	// Left/right edges paint the vertical gradient — connects the top corner
	// hue to the bottom corner hue along the side, so the gradient feels
	// continuous around the perimeter rather than two unrelated bands.
	innerH := h - 2
	contentW := PanelContentWidth(w)
	lines := strings.Split(content, "\n")
	out := make([]string, 0, h)
	out = append(out, top)
	for i := range innerH {
		var ln string
		if i < len(lines) {
			ln = lines[i]
		}
		lw := lipgloss.Width(ln)
		fill := contentW - lw
		if fill < 0 {
			ln = lipgloss.NewStyle().MaxWidth(contentW).Render(ln)
			fill = 0
		}
		// Side colors come from the vertical gradient at row i+1 (row 0 is
		// the top edge already drawn; row h-1 is the bottom edge).
		idx := i + 1
		if idx >= len(vGrad) {
			idx = len(vGrad) - 1
		}
		left := colorCell(vGrad[idx], b.Left)
		right := colorCell(vGrad[idx], b.Right)
		out = append(out, left+ln+strings.Repeat(" ", fill)+right)
	}
	out = append(out, bot)
	return strings.Join(out, "\n")
}

// renderFlatPanel is the blurred-panel fast path: a single foreground color
// for the whole border, no per-cell styling. Same shape as PanelWithAccent
// pre-gradient; kept separate so the focused path can stay readable.
func renderFlatPanel(title string, w, h int, content string, borderColor, titleColor color.Color) string {
	b := lipgloss.RoundedBorder()
	border := lipgloss.NewStyle().Foreground(borderColor)
	titleStyle := lipgloss.NewStyle().Foreground(titleColor)
	trimmed := strings.TrimSpace(title)
	titleSeg := ""
	titleW := 0
	leadDashes := 1
	if trimmed != "" {
		titleSeg = titleStyle.Render(" " + trimmed + " ")
		titleW = lipgloss.Width(titleSeg)
	} else {
		leadDashes = 0
	}
	dashesAfterTitle := w - 2 - leadDashes - titleW
	if dashesAfterTitle < 1 {
		dashesAfterTitle = 1
	}
	top := border.Render(b.TopLeft+strings.Repeat(b.Top, leadDashes)) +
		titleSeg +
		border.Render(strings.Repeat(b.Top, dashesAfterTitle)+b.TopRight)

	bot := border.Render(b.BottomLeft + strings.Repeat(b.Bottom, w-2) + b.BottomRight)
	innerH := h - 2
	contentW := PanelContentWidth(w)
	left := border.Render(b.Left)
	right := border.Render(b.Right)

	lines := strings.Split(content, "\n")
	out := make([]string, 0, h)
	out = append(out, top)
	for i := range innerH {
		var ln string
		if i < len(lines) {
			ln = lines[i]
		}
		lw := lipgloss.Width(ln)
		fill := contentW - lw
		if fill < 0 {
			ln = lipgloss.NewStyle().MaxWidth(contentW).Render(ln)
			fill = 0
		}
		out = append(out, left+ln+strings.Repeat(" ", fill)+right)
	}
	out = append(out, bot)
	return strings.Join(out, "\n")
}

// colorCell renders a single border cell (string, may be multi-byte UTF-8)
// with the given foreground color. Centralised so the gradient-rendering
// loops stay readable.
func colorCell(c color.Color, cell string) string {
	return lipgloss.NewStyle().Foreground(c).Render(cell)
}
