package components

import (
	"image/color"
	"reflect"
	"strings"
	"sync"

	"charm.land/lipgloss/v2"

	"github.com/YunusAndreasson/safina/internal/tui/textw"
	"github.com/YunusAndreasson/safina/internal/tui/theme"
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
// The border is drawn once per theme, colors, title and size and reused
// (see panelEdges); only the content lines are laid out per frame.
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
	e := cachedEdges(edgeKey{t, borderAccent, titleAccent, title, w, h, true}, func() panelEdges {
		return focusedEdges(t, title, w, h, borderAccent, titleAccent)
	})
	return e.frame(w, h, content)
}

// panelEdges is a panel's border, drawn once per theme, color, title and
// size: per-cell gradient borders cost a style render per cell, which made
// the borders a large share of every frame.
type panelEdges struct {
	top, bot    string
	left, right []string // one per inner row; a flat border repeats one cell
}

type edgeKey struct {
	t                *theme.Theme
	border, titleCol color.Color
	title            string
	w, h             int
	focused          bool
}

const maxCachedEdges = 64

var edgeCache = struct {
	sync.Mutex
	m map[edgeKey]*panelEdges
}{m: map[edgeKey]*panelEdges{}}

func cachedEdges(k edgeKey, build func() panelEdges) *panelEdges {
	if !comparableColor(k.border) || !comparableColor(k.titleCol) {
		e := build()
		return &e
	}
	edgeCache.Lock()
	defer edgeCache.Unlock()
	if e, ok := edgeCache.m[k]; ok {
		return e
	}
	if len(edgeCache.m) >= maxCachedEdges { // resizes and theme changes leave old sizes behind
		clear(edgeCache.m)
	}
	e := build()
	edgeCache.m[k] = &e
	return &e
}

// comparableColor reports whether c can be a map key; a color type holding
// a slice or map would panic.
func comparableColor(c color.Color) bool {
	return c == nil || reflect.TypeOf(c).Comparable()
}

func (e *panelEdges) frame(w, h int, content string) string {
	innerH := h - 2
	contentW := PanelContentWidth(w)
	lines := strings.Split(content, "\n")
	var b strings.Builder
	b.Grow(len(content) + len(e.top) + len(e.bot) + innerH*(len(e.left[0])+len(e.right[0])+contentW))
	b.WriteString(e.top)
	for i := range innerH {
		var ln string
		if i < len(lines) {
			ln = lines[i]
		}
		fill := contentW - textw.Width(ln)
		if fill < 0 {
			ln = lipgloss.NewStyle().MaxWidth(contentW).Render(ln)
			fill = 0
		}
		b.WriteByte('\n')
		b.WriteString(e.left[min(i, len(e.left)-1)])
		b.WriteString(ln)
		for ; fill > 0; fill-- {
			b.WriteByte(' ')
		}
		b.WriteString(e.right[min(i, len(e.right)-1)])
	}
	b.WriteByte('\n')
	b.WriteString(e.bot)
	return b.String()
}

func focusedEdges(t *theme.Theme, title string, w, h int, borderAccent, titleAccent color.Color) panelEdges {
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
		titleW = textw.Width(titleSeg)
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

	// Bottom edge: same horizontal gradient, no title interruption.
	var botB strings.Builder
	botB.WriteString(colorCell(topGrad[0], b.BottomLeft))
	for i := 1; i < w-1; i++ {
		botB.WriteString(colorCell(topGrad[i], b.Bottom))
	}
	botB.WriteString(colorCell(topGrad[len(topGrad)-1], b.BottomRight))

	// Left/right edges paint the vertical gradient — connects the top corner
	// hue to the bottom corner hue along the side, so the gradient feels
	// continuous around the perimeter rather than two unrelated bands.
	// Side colors come from the vertical gradient at row i+1 (row 0 is the
	// top edge; row h-1 is the bottom edge).
	innerH := max(h-2, 1)
	e := panelEdges{top: topB.String(), bot: botB.String(), left: make([]string, innerH), right: make([]string, innerH)}
	for i := range innerH {
		idx := min(i+1, len(vGrad)-1)
		e.left[i] = colorCell(vGrad[idx], b.Left)
		e.right[i] = colorCell(vGrad[idx], b.Right)
	}
	return e
}

// renderFlatPanel is the blurred-panel fast path: a single foreground color
// for the whole border, no per-cell styling. Same shape as PanelWithAccent
// pre-gradient; kept separate so the focused path can stay readable.
func renderFlatPanel(title string, w, h int, content string, borderColor, titleColor color.Color) string {
	e := cachedEdges(edgeKey{nil, borderColor, titleColor, title, w, 0, false}, func() panelEdges {
		return flatEdges(title, w, borderColor, titleColor)
	})
	return e.frame(w, h, content)
}

func flatEdges(title string, w int, borderColor, titleColor color.Color) panelEdges {
	b := lipgloss.RoundedBorder()
	border := lipgloss.NewStyle().Foreground(borderColor)
	titleStyle := lipgloss.NewStyle().Foreground(titleColor)
	trimmed := strings.TrimSpace(title)
	titleSeg := ""
	titleW := 0
	leadDashes := 1
	if trimmed != "" {
		titleSeg = titleStyle.Render(" " + trimmed + " ")
		titleW = textw.Width(titleSeg)
	} else {
		leadDashes = 0
	}
	dashesAfterTitle := w - 2 - leadDashes - titleW
	if dashesAfterTitle < 1 {
		dashesAfterTitle = 1
	}
	return panelEdges{
		top: border.Render(b.TopLeft+strings.Repeat(b.Top, leadDashes)) +
			titleSeg +
			border.Render(strings.Repeat(b.Top, dashesAfterTitle)+b.TopRight),
		bot:   border.Render(b.BottomLeft + strings.Repeat(b.Bottom, w-2) + b.BottomRight),
		left:  []string{border.Render(b.Left)},
		right: []string{border.Render(b.Right)},
	}
}

// colorCell renders a single border cell (string, may be multi-byte UTF-8)
// with the given foreground color. Centralised so the gradient-rendering
// loops stay readable.
func colorCell(c color.Color, cell string) string {
	return lipgloss.NewStyle().Foreground(c).Render(cell)
}
