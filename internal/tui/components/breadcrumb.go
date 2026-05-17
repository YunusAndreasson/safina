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

// BreadcrumbStyled returns a " ▸ "-joined path with per-segment styling:
// the trail (every segment except the last) in trailStyle, the current
// location (last segment) in currentStyle, and the separators in sepStyle.
// This builds visual hierarchy into the breadcrumb itself — "where am I"
// reads loudest, "where I came from" fades into context.
//
// Truncation: same rule as BreadcrumbPlain — drop leading segments first
// (keeping the most-recent context), then if even the last segment doesn't
// fit, truncate it with an ellipsis. The ellipsis inherits trailStyle so
// the elided trail still reads as elided trail.
func BreadcrumbStyled(crumbs []Crumb, w int, trailStyle, currentStyle, sepStyle lipgloss.Style) string {
	if len(crumbs) == 0 || w <= 0 {
		return ""
	}
	rawParts := make([]string, len(crumbs))
	styledParts := make([]string, len(crumbs))
	for i, c := range crumbs {
		raw := composeCrumb(c)
		rawParts[i] = raw
		if i == len(crumbs)-1 {
			styledParts[i] = currentStyle.Render(raw)
		} else {
			styledParts[i] = trailStyle.Render(raw)
		}
	}
	const sepText = " ▸ "
	sep := sepStyle.Render(sepText)

	full := strings.Join(styledParts, sep)
	if lipgloss.Width(full) <= w {
		return full
	}

	// Drop leading segments one at a time, keeping the current location and
	// as many tail segments as fit.
	ellipsis := trailStyle.Render("…")
	for keep := len(styledParts) - 1; keep >= 1; keep-- {
		candidate := ellipsis + sep + strings.Join(styledParts[len(styledParts)-keep:], sep)
		if lipgloss.Width(candidate) <= w {
			return candidate
		}
	}

	// Even the current segment alone overflows — truncate its raw text.
	last := rawParts[len(rawParts)-1]
	if lipgloss.Width(last) <= w {
		return currentStyle.Render(last)
	}
	rs := []rune(last)
	if w <= 1 {
		return currentStyle.Render("…")
	}
	return currentStyle.Render(string(rs[:w-1]) + "…")
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

// BreadcrumbPlain returns a plain (no-ANSI) " ▸ "-joined path that the panel
// title bar can render. Truncates from the left with "…" when the joined
// path overflows w; if even the last segment is too long, truncates that
// segment with an ellipsis.
//
// We render this without per-segment styling so the panel's own title color
// (cyan-bold when focused, dim when not) carries the focus signal — adding
// inline styles would fight with the title coloring.
func BreadcrumbPlain(crumbs []Crumb, w int) string {
	if len(crumbs) == 0 || w <= 0 {
		return ""
	}
	parts := make([]string, len(crumbs))
	for i, c := range crumbs {
		parts[i] = composeCrumb(c)
	}
	const sep = " ▸ "
	full := strings.Join(parts, sep)
	if lipgloss.Width(full) <= w {
		return full
	}
	for keep := len(parts) - 1; keep >= 1; keep-- {
		candidate := "…" + sep + strings.Join(parts[len(parts)-keep:], sep)
		if lipgloss.Width(candidate) <= w {
			return candidate
		}
	}
	last := parts[len(parts)-1]
	if lipgloss.Width(last) <= w {
		return last
	}
	rs := []rune(last)
	if w == 1 {
		return "…"
	}
	return string(rs[:w-1]) + "…"
}
