package components

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// Crumb is one segment in a breadcrumb path. The optional KindGlyph and
// HealthGlyph render to the left of the label.
type Crumb struct {
	KindGlyph   string // single-rune kind indicator, e.g. "◇" "◆" "▦"
	HealthGlyph string // ✓ ⚠ ✗ ⏵ … — empty to omit
	Label       string
}

func composeCrumb(c Crumb) string {
	parts := make([]string, 0, 3)
	if c.KindGlyph != "" {
		parts = append(parts, c.KindGlyph)
	}
	if c.HealthGlyph != "" {
		parts = append(parts, c.HealthGlyph)
	}
	parts = append(parts, c.Label)
	return strings.Join(parts, " ")
}

// BreadcrumbRamped renders a breadcrumb with a brightness ramp across the
// trail: the further left, the dimmer. The last segment (the operator's
// current location) is the brightest — the eye is led to it along the gradient.
//
// ramp must have at least one style; if there are more crumbs than ramp tiers
// the leading segments share the dimmest tier. The current crumb always uses
// the last ramp tier regardless of how many trail tiers exist before it.
// Truncation follows the same rule as BreadcrumbStyled (drop leading, then
// ellipsise the leaf).
func BreadcrumbRamped(crumbs []Crumb, w int, ramp []lipgloss.Style, sepStyle lipgloss.Style) string {
	if len(crumbs) == 0 || w <= 0 || len(ramp) == 0 {
		return ""
	}
	rawParts := make([]string, len(crumbs))
	styledParts := make([]string, len(crumbs))
	for i, c := range crumbs {
		raw := composeCrumb(c)
		rawParts[i] = raw
		// Last crumb → top of ramp (brightest). Trail crumbs slide back
		// through the ramp; if there's more trail than tiers, the oldest
		// segments all share ramp[0] (the dimmest).
		var style lipgloss.Style
		if i == len(crumbs)-1 {
			style = ramp[len(ramp)-1]
		} else {
			tier := i - (len(crumbs) - 1 - (len(ramp) - 1))
			if tier < 0 {
				tier = 0
			}
			if tier >= len(ramp)-1 {
				tier = len(ramp) - 2
				if tier < 0 {
					tier = 0
				}
			}
			style = ramp[tier]
		}
		styledParts[i] = style.Render(raw)
	}
	const sepText = " ▸ "
	sep := sepStyle.Render(sepText)

	full := strings.Join(styledParts, sep)
	if lipgloss.Width(full) <= w {
		return full
	}
	dim := ramp[0]
	ellipsis := dim.Render("…")
	for keep := len(styledParts) - 1; keep >= 1; keep-- {
		candidate := ellipsis + sep + strings.Join(styledParts[len(styledParts)-keep:], sep)
		if lipgloss.Width(candidate) <= w {
			return candidate
		}
	}
	last := rawParts[len(rawParts)-1]
	current := ramp[len(ramp)-1]
	if lipgloss.Width(last) <= w {
		return current.Render(last)
	}
	rs := []rune(last)
	if w <= 1 {
		return current.Render("…")
	}
	return current.Render(string(rs[:w-1]) + "…")
}
