package theme

import (
	"image/color"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
)

// Theme bundles a Palette with derived lipgloss styles. Components take a
// *Theme so a single switch recolors the whole app.
type Theme struct {
	P Palette

	// Selection / chrome
	Selected     lipgloss.Style
	SelectedDim  lipgloss.Style // selected row in unfocused panel
	ColHeader    lipgloss.Style
	ColHeaderNum lipgloss.Style // right-aligned numeric column header
	StatusBar    lipgloss.Style
	ErrLine      lipgloss.Style
	OkLine       lipgloss.Style // green sibling to ErrLine — post-action confirmations
	Modal        lipgloss.Style
	LineNum      lipgloss.Style
	HelpKey      lipgloss.Style
	HelpDesc     lipgloss.Style
	FilterPrompt lipgloss.Style
	FilterText   lipgloss.Style

	// Typography helpers
	NameStrong lipgloss.Style // primary identifier (deployment / pod name)
	NameDim    lipgloss.Style // identifier in unfocused panel / non-selected row
	Label      lipgloss.Style // "READY", "STATUS" — bold dim
	Meta       lipgloss.Style // namespace / age / counts
	Numeric    lipgloss.Style // unstyled numeric value (right-aligned by table)
	Title      lipgloss.Style // panel title — focused
	TitleDim   lipgloss.Style // panel title — blurred

	// Inline helpers
	DimTxt   lipgloss.Style
	DimBold  lipgloss.Style
	DimPad   lipgloss.Style
	Idle     lipgloss.Style // italic muted — for "loading…", "no matches", "select a cluster"
	GreenTxt lipgloss.Style
	CyanBold lipgloss.Style
	BlueBold lipgloss.Style
}

// Default builds the default theme.
func Default() *Theme { return New(TokyoNight()) }

// New builds a Theme from a Palette.
func New(p Palette) *Theme {
	t := &Theme{P: p}

	// Selection uses a saturated dark navy so the inner status colors
	// (red CrashLoopBackOff, green Running, …) keep their meaning instead of
	// fighting a bright background.
	t.Selected = lipgloss.NewStyle().Background(p.BlueDark).Foreground(p.FG).Bold(true)
	t.SelectedDim = lipgloss.NewStyle().Background(p.BG3).Foreground(p.FG)
	t.ColHeader = lipgloss.NewStyle().Background(p.BG).Foreground(p.Dim).Bold(true)
	t.ColHeaderNum = t.ColHeader.AlignHorizontal(lipgloss.Right)
	t.StatusBar = lipgloss.NewStyle().Background(p.BG2).Foreground(p.FG).Padding(0, 1).Bold(true)
	t.ErrLine = lipgloss.NewStyle().Background(p.BG2).Foreground(p.Red).Bold(true).Padding(0, 1)
	t.OkLine = lipgloss.NewStyle().Background(p.BG2).Foreground(p.Green).Bold(true).Padding(0, 1)
	t.Modal = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(p.Blue).
		Background(p.BG2).
		Padding(1, 2)
	t.LineNum = lipgloss.NewStyle().Foreground(p.Dim).Width(6)
	t.HelpKey = lipgloss.NewStyle().Foreground(p.Blue).Bold(true)
	t.HelpDesc = lipgloss.NewStyle().Foreground(p.FGFade)
	t.FilterPrompt = lipgloss.NewStyle().Foreground(p.Pink).Bold(true)
	t.FilterText = lipgloss.NewStyle().Foreground(p.FG)

	t.NameStrong = lipgloss.NewStyle().Foreground(p.FG).Bold(true)
	t.NameDim = lipgloss.NewStyle().Foreground(p.FGFade)
	t.Label = lipgloss.NewStyle().Foreground(p.Dim).Bold(true)
	t.Meta = lipgloss.NewStyle().Foreground(p.Dim)
	t.Numeric = lipgloss.NewStyle().Foreground(p.FG)
	t.Title = lipgloss.NewStyle().Foreground(p.Cyan).Bold(true)
	t.TitleDim = lipgloss.NewStyle().Foreground(p.Dim)

	t.DimTxt = lipgloss.NewStyle().Foreground(p.Dim)
	t.DimBold = lipgloss.NewStyle().Foreground(p.Dim).Bold(true)
	t.DimPad = lipgloss.NewStyle().Foreground(p.Dim).Padding(0, 1)
	// Italic adds a real typographic register for non-data text — empty
	// states, loaders, hints — so they don't compete with names and counts.
	t.Idle = lipgloss.NewStyle().Foreground(p.Dim).Italic(true).Padding(0, 1)
	t.GreenTxt = lipgloss.NewStyle().Foreground(p.Green).Bold(true)
	t.CyanBold = lipgloss.NewStyle().Foreground(p.Cyan).Bold(true)
	t.BlueBold = lipgloss.NewStyle().Foreground(p.Blue).Bold(true)

	return t
}

// StatusColor maps a Kubernetes status phase to a palette color.
func (t *Theme) StatusColor(s string) color.Color {
	switch s {
	case "Running", "Ready":
		return t.P.Green
	case "Pending", "ContainerCreating", "Init":
		return t.P.Yellow
	case "Failed", "CrashLoopBackOff", "ImagePullBackOff", "ErrImagePull", "Error", "Terminating", "NotReady":
		return t.P.Red
	default:
		return t.P.FGFade
	}
}

// StatusGlyph renders the status indicator sized to fit at most maxW
// columns over the given row background style. We bake bg into every styled
// segment because helpers that emit only a foreground end with a `\x1b[0m`
// reset, and that reset would punch a hole in the row's selection / stripe
// background mid-cell — visible as a "the status never blends with the bar"
// glitch on selected rows.
//
// "Running" is the steady state for ~95% of pods — printing the word next
// to a green dot 50× in a list is pure noise. The dot already says "healthy";
// we suppress the label so the eye lands on outliers (Pending /
// CrashLoopBackOff / …) instantly. Same idea for "Ready" on nodes.
//
// Pass an empty (zero-value) lipgloss.Style for chrome that isn't on a row.
func (t *Theme) StatusGlyph(s string, maxW int, bg lipgloss.Style) string {
	col := t.StatusColor(s)
	fg := bg.Foreground(col)
	dot := fg.Render("●")
	if s == "Running" || s == "Ready" {
		return dot
	}
	if maxW < 3 {
		return dot
	}
	labelW := maxW - 2 // "● " takes 2 cells
	label := s
	if lipgloss.Width(label) > labelW {
		if labelW < 2 {
			return dot
		}
		rs := []rune(label)
		label = string(rs[:labelW-1]) + "…"
	}
	return dot + bg.Render(" ") + fg.Render(label)
}

// RowBg returns the alternating-row background style.
func (t *Theme) RowBg(idx int) lipgloss.Style {
	base := lipgloss.NewStyle().Padding(0, 0)
	if idx%2 == 1 {
		return base.Background(t.P.BGRow)
	}
	return base.Background(t.P.BG)
}

// RowBgFor returns the background style a row will paint for (idx, selected).
// Mirrors components.Row's internal computation so cell builders (status,
// ratio, etc.) can paint the same bg into their styled output.
func (t *Theme) RowBgFor(idx int, selected bool) lipgloss.Style {
	if selected {
		return t.Selected
	}
	return t.RowBg(idx)
}

// Ratio renders a "ready/total"-style string colored by health, painted on
// the given row background. See StatusGlyph for why bg is required.
//
//	ready == total  → green (healthy)
//	ready == 0      → red (down)
//	otherwise       → yellow (partial)
//	unparseable     → dim
func (t *Theme) Ratio(s string, bg lipgloss.Style) string {
	ready, total, ok := parseRatio(s)
	if !ok {
		return bg.Foreground(t.P.Dim).Render(s)
	}
	var col color.Color
	switch {
	case total == 0:
		col = t.P.Dim
	case ready >= total:
		col = t.P.Green
	case ready == 0:
		col = t.P.Red
	default:
		col = t.P.Yellow
	}
	return bg.Foreground(col).Bold(true).Render(s)
}

// RestartCount renders an integer restart counter colored by severity over
// the given row background. Returns an empty (bg-filled) cell for n == 0:
// "is anything restarting?" is a binary scan question, and dim zeros across
// dozens of rows still pull the eye away from the real signal.
func (t *Theme) RestartCount(n int, bg lipgloss.Style) string {
	if n == 0 {
		return ""
	}
	s := strconv.Itoa(n)
	switch {
	case n < 3:
		return bg.Foreground(t.P.FG).Render(s)
	case n < 10:
		return bg.Foreground(t.P.Yellow).Bold(true).Render(s)
	default:
		return bg.Foreground(t.P.Red).Bold(true).Render(s)
	}
}

// Age renders an age string in a uniform muted style on the given row bg.
func (t *Theme) Age(s string, bg lipgloss.Style) string {
	return bg.Foreground(t.P.Dim).Render(s)
}

// Meta renders a generic muted cell on the given row bg — the bg-aware
// counterpart to t.Meta.Render(). Use for namespace, age, image, etc.
func (t *Theme) MetaOn(s string, bg lipgloss.Style) string {
	return bg.Foreground(t.P.Dim).Render(s)
}

// NameStrongOn renders a primary identifier (deployment / pod / node name)
// over the given row bg.
func (t *Theme) NameStrongOn(s string, bg lipgloss.Style) string {
	return bg.Foreground(t.P.FG).Bold(true).Render(s)
}

// ActiveDot returns the green ● used to mark the currently active resource
// (e.g. the active kube context). Centralised so every "active" marker in the
// app shares one shape and one color.
func (t *Theme) ActiveDot() string {
	return lipgloss.NewStyle().Foreground(t.P.Green).Render("●")
}

func parseRatio(s string) (ready, total int, ok bool) {
	parts := strings.SplitN(strings.TrimSpace(s), "/", 2)
	if len(parts) != 2 {
		return 0, 0, false
	}
	r, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
	tot, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return r, tot, true
}
