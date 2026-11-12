package components

import (
	"strings"
	"sync"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/YunusAndreasson/safina/internal/tui/textw"
	"github.com/YunusAndreasson/safina/internal/tui/theme"
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
			if textw.Width(strings.TrimSpace(l)) <= 2 {
				style = style.AlignHorizontal(lipgloss.Center)
			} else {
				style = style.AlignHorizontal(lipgloss.Right)
			}
		}
		cells[i] = style.Render(l)
	}
	// Width(n).Render("") paints n cells of the style's background — same
	// result as strings.Repeat(" ", n) wrapped in Render, but declarative.
	gap := t.ColHeader.Width(gapW).Render("")
	pad := t.ColHeader.Width(edgePad).Render("")
	return pad + strings.Join(cells, gap) + pad
}

// Row renders a table row with per-column alignment, edge padding, and the
// chosen background (alternating stripe or selected). The entire returned
// string spans (sumWidths + gaps + 2*edgePad) cells, with bg painted edge to
// edge.
func Row(t *theme.Theme, vals []string, widths []int, aligns []Align, selected bool, idx int) string {
	if row, ok := fastRow(t, vals, widths, aligns, selected, idx); ok {
		return row
	}
	return styledRow(t, vals, widths, aligns, selected, idx)
}

func styledRow(t *theme.Theme, vals []string, widths []int, aligns []Align, selected bool, idx int) string {
	bg := t.RowBgFor(idx, selected)
	cellStyle := lipgloss.NewStyle().Inherit(bg)

	cells := make([]string, len(vals))
	for i, v := range vals {
		s := cellStyle.Width(widths[i])
		if i < len(aligns) && aligns[i] == AlignRight {
			s = s.AlignHorizontal(lipgloss.Right)
		}
		cells[i] = s.Render(v)
	}
	gap := bg.Width(gapW).Render("")
	pad := bg.Width(edgePad).Render("")
	body := pad + strings.Join(cells, gap) + pad
	return bg.Width(RowOuterWidth(widths)).Render(body)
}

// rowPaint is what lipgloss writes around a row's text: the style's SGR
// before text, and the background-only SGR before padding. Each Render
// call of styledRow costs a few microseconds, and a row makes one per cell
// plus three, so a table frame spent most of its time there.
type rowPaint struct {
	text, space string
	ok          bool
}

type rowPaintKey struct {
	t        *theme.Theme
	selected bool
	odd      bool
}

var rowPaints = struct {
	sync.Mutex
	m map[rowPaintKey]rowPaint
}{m: map[rowPaintKey]rowPaint{}}

func paintFor(t *theme.Theme, selected bool, idx int) rowPaint {
	k := rowPaintKey{t, selected, !selected && idx%2 == 1}
	rowPaints.Lock()
	defer rowPaints.Unlock()
	if p, ok := rowPaints.m[k]; ok {
		return p
	}
	p := derivePaint(lipgloss.NewStyle().Inherit(t.RowBgFor(idx, selected)))
	rowPaints.m[k] = p
	return p
}

// derivePaint reads the two prefixes off lipgloss's own output, so the fast
// path writes exactly what lipgloss would. Any other shape disables it.
func derivePaint(st lipgloss.Style) rowPaint {
	const reset = "\x1b[m"
	text, ok := strings.CutSuffix(st.Render("X"), "X"+reset)
	if !ok || strings.Contains(text, "X") {
		return rowPaint{}
	}
	rest, ok := strings.CutPrefix(st.Width(1).Render(""), text+reset)
	if !ok {
		return rowPaint{}
	}
	space, ok := strings.CutSuffix(rest, " "+reset)
	if !ok {
		return rowPaint{}
	}
	return rowPaint{text: text, space: space, ok: true}
}

// fastRow builds the row styledRow would when every value is one line that
// fits its column; anything else (wrapping, tabs, overflow) goes to lipgloss.
func fastRow(t *theme.Theme, vals []string, widths []int, aligns []Align, selected bool, idx int) (string, bool) {
	if len(vals) != len(widths) {
		return "", false
	}
	p := paintFor(t, selected, idx)
	if !p.ok {
		return "", false
	}
	const reset = "\x1b[m"
	var b strings.Builder
	b.Grow(RowOuterWidth(widths) + len(vals)*(len(p.text)+len(p.space)+16) + 64)
	b.WriteString(p.text)
	writeSpace := func(n int) {
		b.WriteString(p.space)
		for ; n > 0; n-- {
			b.WriteByte(' ')
		}
		b.WriteString(reset)
	}
	blank := func(n int) { // Width(n).Render(""): an empty text run, then n spaces
		b.WriteString(p.text)
		b.WriteString(reset)
		writeSpace(n)
	}
	blank(edgePad)
	for i, v := range vals {
		w := widths[i]
		if w < 1 || strings.ContainsAny(v, "\n\t\r") {
			return "", false
		}
		vw := textw.StringWidth(v)
		if vw > w {
			return "", false
		}
		if i > 0 {
			blank(gapW)
		}
		right := i < len(aligns) && aligns[i] == AlignRight
		switch {
		case vw == w:
			b.WriteString(p.text)
			b.WriteString(v)
			b.WriteString(reset)
		case right: // an empty value still writes its (empty) text run, after the padding
			writeSpace(w - vw)
			b.WriteString(p.text)
			b.WriteString(v)
			b.WriteString(reset)
		default:
			b.WriteString(p.text)
			b.WriteString(v)
			b.WriteString(reset)
			writeSpace(w - vw)
		}
	}
	blank(edgePad)
	b.WriteString(reset)
	return b.String(), true
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
	s = ansi.Truncate(s, w, "…")
	return s + strings.Repeat(" ", max(0, w-textw.Width(s)))
}

func sumWidths(w []int) int {
	s := 0
	for _, x := range w {
		s += x
	}
	return s
}

// TruncMiddlePad keeps a pod's distinguishing suffix visible when its full
// generated name does not fit. Both halves still refer to the original name.
func TruncMiddlePad(s string, w int) string {
	if textw.Width(s) <= w || w < 7 {
		return TruncPad(s, w)
	}
	tail := min(5, (w-1)/2)
	head := w - tail - 1
	return ansi.Cut(s, 0, head) + "…" + ansi.Cut(s, textw.Width(s)-tail, textw.Width(s))
}

// Spread lets a table use a wide screen the way terminal tables do: spare
// cells go between the columns, evenly, rather than to one column or to the
// row's end. Each gap grows by up to maxGrow cells; what is left over stays
// with the caller (a filler column at the row's end).
type Spread struct {
	after []bool // after[i]: a spacer column follows column i
	grow  int
}

// SpreadColumns widens the gaps between the n columns of widths by sharing
// out slack cells. It returns the spread, the widths, aligns and labels with
// the spacer columns in place, and the cells it did not use.
func SpreadColumns(widths []int, aligns []Align, labels []string, slack, maxGrow int) (Spread, []int, []Align, []string, int) {
	n := len(widths) - 1
	if n < 1 || slack < 2*n {
		return Spread{}, widths, aligns, labels, slack
	}
	grow := min(maxGrow, slack/n)
	sp := Spread{after: make([]bool, len(widths)), grow: grow}
	var w []int
	var a []Align
	var l []string
	for i := range widths {
		w = append(w, widths[i])
		a = append(a, aligns[i])
		l = append(l, labels[i])
		if i < n {
			sp.after[i] = true
			// A spacer is grow-1 cells plus its own one-cell gap.
			w = append(w, grow-gapW)
			a = append(a, AlignLeft)
			l = append(l, "")
		}
	}
	return sp, w, a, l, slack - n*grow
}

// Vals puts the empty spacer cells into a row's values.
func (s Spread) Vals(vals []string) []string {
	if s.grow == 0 {
		return vals
	}
	out := make([]string, 0, 2*len(vals))
	for i, v := range vals {
		out = append(out, v)
		if i < len(s.after) && s.after[i] {
			out = append(out, "")
		}
	}
	return out
}
