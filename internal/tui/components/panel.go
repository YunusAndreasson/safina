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

// Panel renders a rounded-border box with the title in the top edge. The
// content is expected to fill exactly PanelContentWidth(w) cells per line.
//
//	╭─ Title ──────────────╮
//	│ content              │
//	╰──────────────────────╯
//
// w and h are the *outer* dimensions including borders.
func Panel(t *theme.Theme, title string, w, h int, content string, focused bool) string {
	return PanelWithAccent(t, title, w, h, content, focused, t.P.Blue, t.P.Cyan)
}

// PanelWithAccent renders a panel using custom focus accent colors. When
// focused, the border uses borderAccent + a heavy ThickBorder for a clear
// visual-weight pop; when blurred, it falls back to RoundedBorder in dim. The
// title uses titleAccent (bold) on focus, dim otherwise.
//
// Use this to signal mode changes (e.g. switch to pink when a filter is
// active) without losing the rest of the panel chrome.
func PanelWithAccent(t *theme.Theme, title string, w, h int, content string, focused bool, borderAccent, titleAccent color.Color) string {
	if w < 4 {
		w = 4
	}
	if h < 2 {
		h = 2
	}

	// Border weight does the heavy lifting for focus signalling: thick rule
	// for the active panel, soft rounded rule for the rest. Color comes on top.
	b := lipgloss.RoundedBorder()
	col := t.P.Dim
	if focused {
		b = lipgloss.ThickBorder()
		col = borderAccent
	}
	border := lipgloss.NewStyle().Foreground(col)

	titleStyle := lipgloss.NewStyle().Foreground(col)
	if focused {
		titleStyle = titleStyle.Bold(true).Foreground(titleAccent)
	}
	titleSeg := titleStyle.Render(" " + strings.TrimSpace(title) + " ")
	titleW := lipgloss.Width(titleSeg)

	leadDashes := 1
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
	for i := 0; i < innerH; i++ {
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
