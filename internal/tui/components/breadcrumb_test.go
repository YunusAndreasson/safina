package components

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// TestBreadcrumbPlain_Edges checks the plain (no-ANSI) breadcrumb at edge
// widths and shapes that have historically tripped truncation logic.
func TestBreadcrumbPlain_Edges(t *testing.T) {
	mk := func(labels ...string) []Crumb {
		c := make([]Crumb, len(labels))
		for i, l := range labels {
			c[i] = Crumb{Label: l}
		}
		return c
	}
	cases := []struct {
		name   string
		crumbs []Crumb
		w      int
		want   string
	}{
		{"empty_crumbs", nil, 80, ""},
		{"zero_width", mk("a", "b"), 0, ""},
		{"width_one_one_segment", mk("abc"), 1, "…"},
		{"width_two_one_segment", mk("abc"), 2, "a…"},
		{"width_three_one_segment", mk("abc"), 3, "abc"},
		{"fits_full", mk("a", "b"), 80, "a ▸ b"},
		{"truncate_leading", mk("aaaa", "bbbb", "cccc"), 9, "… ▸ cccc"},
		{"only_last_fits", mk("aaaaaaaa", "bbbbbbbb"), 8, "bbbbbbbb"},
		{"last_too_long_truncate", mk("aaaaaaaa", "verylonglabel"), 5, "very…"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := BreadcrumbPlain(tc.crumbs, tc.w)
			if got != tc.want {
				t.Errorf("BreadcrumbPlain(%v, %d) = %q, want %q", tc.crumbs, tc.w, got, tc.want)
			}
			// Sanity: rendered width must never exceed the budget.
			if w := lipgloss.Width(got); tc.w > 0 && w > tc.w {
				t.Errorf("BreadcrumbPlain produced width %d, exceeds budget %d (out=%q)", w, tc.w, got)
			}
		})
	}
}

// TestBreadcrumbStyled_NeverExceedsWidth fuzzes the styled version with
// pseudo-random crumbs at narrow budgets. The visible (ANSI-stripped) width
// must never exceed the requested budget.
func TestBreadcrumbStyled_NeverExceedsWidth(t *testing.T) {
	plain := lipgloss.NewStyle()
	current := lipgloss.NewStyle().Bold(true)
	sep := lipgloss.NewStyle()

	pieces := []string{"a", "ab", "abc", "x", "longer", "verylonglabel"}
	for _, w := range []int{1, 2, 3, 4, 5, 10, 20, 50, 100} {
		for n := 1; n <= 5; n++ {
			crumbs := make([]Crumb, n)
			for i := 0; i < n; i++ {
				crumbs[i] = Crumb{Label: pieces[(i+w)%len(pieces)]}
			}
			out := BreadcrumbStyled(crumbs, w, plain, current, sep)
			if got := lipgloss.Width(out); got > w {
				t.Errorf("BreadcrumbStyled(n=%d, w=%d) produced width %d, exceeds budget", n, w, got)
			}
		}
	}
}

// TestBreadcrumbPlain_NeverExceedsWidth: same property, plain version.
func TestBreadcrumbPlain_NeverExceedsWidth(t *testing.T) {
	pieces := []string{"a", "ab", "abc", "x", "longer", "verylonglabel"}
	for _, w := range []int{1, 2, 3, 4, 5, 10, 20, 50, 100} {
		for n := 1; n <= 5; n++ {
			crumbs := make([]Crumb, n)
			for i := 0; i < n; i++ {
				crumbs[i] = Crumb{Label: pieces[(i+w)%len(pieces)]}
			}
			out := BreadcrumbPlain(crumbs, w)
			if got := lipgloss.Width(out); got > w {
				t.Errorf("BreadcrumbPlain(n=%d, w=%d) produced width %d, exceeds budget; out=%q",
					n, w, got, out)
			}
		}
	}
}

// TestBreadcrumb_KindAndHealthGlyphs makes sure compose pulls the optional
// glyphs into the rendered string (the topbar uses them for kind/health hints).
func TestBreadcrumb_KindAndHealthGlyphs(t *testing.T) {
	c := []Crumb{{KindGlyph: "K", HealthGlyph: "H", Label: "name"}}
	got := BreadcrumbPlain(c, 80)
	for _, want := range []string{"K", "H", "name"} {
		if !strings.Contains(got, want) {
			t.Errorf("BreadcrumbPlain output %q missing %q", got, want)
		}
	}
}
