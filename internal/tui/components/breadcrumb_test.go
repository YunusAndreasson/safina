package components

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// TestBreadcrumbRamped_NeverExceedsWidth fuzzes the ramp-styled breadcrumb at
// narrow budgets. The visible (ANSI-stripped) width must never exceed the
// requested budget — the topbar relies on this to keep the header on one row.
func TestBreadcrumbRamped_NeverExceedsWidth(t *testing.T) {
	ramp := []lipgloss.Style{
		lipgloss.NewStyle(),
		lipgloss.NewStyle().Bold(true),
	}
	sep := lipgloss.NewStyle()

	pieces := []string{"a", "ab", "abc", "x", "longer", "verylonglabel"}
	for _, w := range []int{1, 2, 3, 4, 5, 10, 20, 50, 100} {
		for n := 1; n <= 5; n++ {
			crumbs := make([]Crumb, n)
			for i := range n {
				crumbs[i] = Crumb{Label: pieces[(i+w)%len(pieces)]}
			}
			out := BreadcrumbRamped(crumbs, w, ramp, sep)
			if got := lipgloss.Width(out); got > w {
				t.Errorf("BreadcrumbRamped(n=%d, w=%d) produced width %d, exceeds budget", n, w, got)
			}
		}
	}
}

// TestBreadcrumb_KindAndHealthGlyphs makes sure compose pulls the optional
// glyphs into the rendered string (the topbar uses them for kind/health hints).
func TestBreadcrumb_KindAndHealthGlyphs(t *testing.T) {
	c := []Crumb{{KindGlyph: "K", HealthGlyph: "H", Label: "name"}}
	ramp := []lipgloss.Style{lipgloss.NewStyle()}
	got := BreadcrumbRamped(c, 80, ramp, lipgloss.NewStyle())
	for _, want := range []string{"K", "H", "name"} {
		if !strings.Contains(got, want) {
			t.Errorf("BreadcrumbRamped output %q missing %q", got, want)
		}
	}
}
