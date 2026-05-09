package theme

import (
	"image/color"

	"charm.land/lipgloss/v2"
)

// Palette is the flat color set the theme is derived from.
type Palette struct {
	BG     color.Color // primary background
	BG2    color.Color // surface (status bar, modal)
	BG3    color.Color // hover / focused-panel inner highlight
	BGRow  color.Color // alternating row stripe
	FG     color.Color // body text — primary
	FGFade color.Color // body text — secondary
	Dim    color.Color // labels, borders, metadata

	Blue     color.Color // primary accent (focused border, key hints)
	BlueDark color.Color // selection background — saturated, low-glare
	Cyan     color.Color // titles / active context
	Pink     color.Color // filter / prompt
	Green    color.Color // ok / running / healthy
	Yellow   color.Color // warning / pending / partial
	Red      color.Color // error / failed / down
	Mauve    color.Color // numeric ratios
}

// TokyoNight — calibrated for legibility and a soft, even contrast.
func TokyoNight() Palette {
	return Palette{
		BG:       lipgloss.Color("#1a1b26"),
		BG2:      lipgloss.Color("#24283b"),
		BG3:      lipgloss.Color("#2a2f44"),
		BGRow:    lipgloss.Color("#1e2030"),
		FG:       lipgloss.Color("#c0caf5"),
		FGFade:   lipgloss.Color("#9aa5ce"),
		Dim:      lipgloss.Color("#565f89"),
		Blue:     lipgloss.Color("#7aa2f7"),
		BlueDark: lipgloss.Color("#3b4261"),
		Cyan:     lipgloss.Color("#7dcfff"),
		Pink:     lipgloss.Color("#bb9af7"),
		Green:    lipgloss.Color("#9ece6a"),
		Yellow:   lipgloss.Color("#e0af68"),
		Red:      lipgloss.Color("#f7768e"),
		Mauve:    lipgloss.Color("#c0a6e8"),
	}
}
