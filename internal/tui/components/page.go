package components

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/YunusAndreasson/safina/internal/tui/textw"
	"github.com/YunusAndreasson/safina/internal/tui/theme"
)

// Single-pane safina draws no boxes. One surface owns the keys, so a border
// marking focus has nothing left to say; a full-screen box would only cost
// two columns and two rows. Content is PanelContentWidth(w) wide and h-1
// tall, right under the title rule; nothing is held back at the bottom:
//
//	── Title ─────────────────────
//	 content
//	 content
//	~    ~~      ~   ~~~     ~     ← the waterline, in the last row only
//	                                  when the rows above it are empty

// waterline is a calm, irregular swell: drawn once under a short list, it
// marks where the rows end the way a hull's waterline does. Static, dim,
// and never under an illustration.
const waterline = "~    ~~      ~   ~~~     ~      ~~    ~        ~~   ~       ~~~    ~      ~   ~~     ~~       ~    ~~~  ~         ~~     "

// minWaterlineGap is how many empty rows a list must leave before the
// waterline appears; less than that and it would crowd the last row.
const minWaterlineGap = 6

// PageFrame renders content under a title rule with one-cell gutters.
// accent colours the title (dim for an ordinary list, pink while a filter
// is being typed). waterline draws the swell in the bottom row when the
// content leaves enough empty rows above it; otherwise that row is content.
func PageFrame(t *theme.Theme, title string, w, h int, content string, accent color.Color, waterline bool) string {
	if w < 4 {
		w = 4
	}
	if h < 2 {
		h = 2
	}
	innerH := h - 1
	lines := strings.Split(content, "\n")
	var b strings.Builder
	// An untitled section sits directly under a heading that already rules
	// it off (the tab strip), so it starts with a blank row, not a line.
	if title == "" {
		b.WriteString(strings.Repeat(" ", w))
	} else {
		// The rule keeps the page's one-cell gutters, like the tab rule.
		b.WriteString(" " + TitleRule(t, title, w-2, accent) + " ")
	}
	b.WriteByte('\n')
	b.WriteString(PageBody(t, w, innerH, lines, waterline))
	return b.String()
}

// PageBody renders content lines into w × h cells with one-cell gutters
// and no title, for a page whose heading is drawn above it. waterline
// draws the swell in the bottom row when enough rows above it are empty.
func PageBody(t *theme.Theme, w, h int, lines []string, waterline bool) string {
	contentW := PanelContentWidth(w)
	used := 0
	for i := range min(h, len(lines)) {
		if strings.TrimSpace(ansi.Strip(lines[i])) != "" {
			used = i + 1
		}
	}
	swell := waterline && h-used > minWaterlineGap
	var b strings.Builder
	for i := range h {
		if i > 0 {
			b.WriteByte('\n')
		}
		if swell && i == h-1 {
			b.WriteString(Waterline(t, w))
			break
		}
		var ln string
		if i < len(lines) {
			ln = lines[i]
		}
		b.WriteByte(' ')
		b.WriteString(fitLine(ln, contentW))
		b.WriteByte(' ')
	}
	return b.String()
}

// TitleRule is the ASCII-era heading: `── Title ─────`. An empty title draws
// the plain rule.
func TitleRule(t *theme.Theme, title string, w int, accent color.Color) string {
	rule := lipgloss.NewStyle().Foreground(t.P.Dim)
	if title == "" {
		return rule.Render(strings.Repeat("─", w))
	}
	title = ansi.Truncate(title, max(1, w-6), "…")
	head := rule.Render("── ") + lipgloss.NewStyle().Foreground(accent).Render(title) + " "
	rest := w - textw.Width(head)
	if rest < 0 {
		return ansi.Truncate(head, w, "")
	}
	return head + rule.Render(strings.Repeat("─", rest))
}

// Waterline renders the swell across w cells in the faintest ink.
func Waterline(t *theme.Theme, w int) string {
	var line strings.Builder
	for line.Len() < w {
		line.WriteString(waterline)
	}
	s := line.String()[:w]
	return lipgloss.NewStyle().Foreground(t.P.Dim).Faint(true).Render(s)
}

// FitLine pads or cuts a line to exactly w cells.
func FitLine(ln string, w int) string { return fitLine(ln, w) }

func fitLine(ln string, w int) string {
	n := textw.Width(ln)
	if n > w {
		return lipgloss.NewStyle().MaxWidth(w).Render(ln)
	}
	return ln + strings.Repeat(" ", w-n)
}
