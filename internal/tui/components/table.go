package components

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/yunus/safina/internal/tui/theme"
)

// Align is a per-column horizontal alignment hint.
type Align int

const (
	AlignLeft Align = iota
	AlignRight
)

// gapW is the visible space between adjacent columns.
// edgePad is the breathing room painted on each side of the row, in the row's
// own background, so that selection/striping fills the panel edge-to-edge.
const (
	gapW    = 1
	edgePad = 1
)

// Header renders a column header row with alignment per column. The header
// includes the same edge padding as Row so it aligns with the data rows.
//
// One width-aware tweak: a header label of ≤ 2 cells (single glyphs like
// `↻`, `▾`, status icons) reads as an *icon* not a text label, so we
// override AlignRight with center alignment — a single right-aligned
// glyph floats lonely against the column edge and breaks the visual
// rhythm of the row above the data. Same idea gh-dash uses.
func Header(t *theme.Theme, labels []string, widths []int, aligns []Align) string {
	cells := make([]string, len(labels))
	for i, l := range labels {
		style := t.ColHeader.Width(widths[i])
		if i < len(aligns) && aligns[i] == AlignRight {
			if lipgloss.Width(strings.TrimSpace(l)) <= 2 {
				style = style.AlignHorizontal(lipgloss.Center)
			} else {
				style = style.AlignHorizontal(lipgloss.Right)
			}
		}
		cells[i] = style.Render(l)
	}
	gap := t.ColHeader.Render(strings.Repeat(" ", gapW))
	pad := t.ColHeader.Render(strings.Repeat(" ", edgePad))
	return pad + strings.Join(cells, gap) + pad
}

// Row renders a table row with per-column alignment, edge padding, and the
// chosen background (alternating stripe or selected). The entire returned
// string spans (sumWidths + gaps + 2*edgePad) cells, with bg painted edge to
// edge.
func Row(t *theme.Theme, vals []string, widths []int, aligns []Align, selected bool, idx int) string {
	bg := t.RowBg(idx)
	if selected {
		bg = t.Selected
	}
	cellStyle := lipgloss.NewStyle().Inherit(bg)

	cells := make([]string, len(vals))
	for i, v := range vals {
		s := cellStyle.Width(widths[i])
		if i < len(aligns) && aligns[i] == AlignRight {
			s = s.AlignHorizontal(lipgloss.Right)
		}
		cells[i] = s.Render(v)
	}
	gap := bg.Render(strings.Repeat(" ", gapW))
	pad := bg.Render(strings.Repeat(" ", edgePad))
	body := pad + strings.Join(cells, gap) + pad
	return bg.Width(RowOuterWidth(widths)).Render(body)
}

// RowOuterWidth returns the total visible width of a row given its column
// widths — including inter-column gaps and edge padding.
func RowOuterWidth(widths []int) int {
	return sumWidths(widths) + gapW*max(0, len(widths)-1) + 2*edgePad
}

// GapTotal is the total width consumed by inter-column gaps for n columns.
// Callers should subtract this from the panel content width when sizing
// columns. (Edge padding is reserved separately via EdgeTotal.)
func GapTotal(numCols int) int {
	if numCols < 2 {
		return 0
	}
	return gapW * (numCols - 1)
}

// EdgeTotal is the total width reserved for row edge padding (both sides).
func EdgeTotal() int { return 2 * edgePad }

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// VisibleRange returns [start, end) indices given a cursor and a fixed window.
// Keeps the cursor centered when possible.
func VisibleRange(cursor, total, maxRows int) (int, int) {
	if maxRows < 1 {
		maxRows = 1
	}
	if total <= maxRows {
		return 0, total
	}
	s := cursor - maxRows/2
	if s < 0 {
		s = 0
	}
	e := s + maxRows
	if e > total {
		e = total
		s = e - maxRows
		if s < 0 {
			s = 0
		}
	}
	return s, e
}

// TruncPad pads s to width w with spaces, or truncates with an ellipsis.
func TruncPad(s string, w int) string {
	if w < 1 {
		w = 1
	}
	rs := []rune(s)
	if len(rs) > w {
		return string(rs[:w-1]) + "…"
	}
	pad := w - lipgloss.Width(s)
	if pad < 0 {
		return s
	}
	return s + strings.Repeat(" ", pad)
}

func sumWidths(w []int) int {
	s := 0
	for _, x := range w {
		s += x
	}
	return s
}
