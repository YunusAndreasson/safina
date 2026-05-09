package components

import (
	"testing"

	"github.com/yunus/safina/internal/tui/theme"
)

// TestRefreshColor_Bands locks in the threshold semantics and proves the
// function does not panic on garbage input.
func TestRefreshColor_Bands(t *testing.T) {
	th := theme.Default()
	cases := []struct {
		age      string
		category string
	}{
		{"", "dim"},
		{"5", "dim"}, // too short for unit
		{"abc", "dim"},
		{"-5s", "dim"},   // sign is invalid digit, falls to dim
		{"5s", "fgfade"}, // fresh
		{"0s", "fgfade"},
		{"6s", "yellow"}, // boundary
		{"14s", "yellow"},
		{"15s", "red"}, // boundary
		{"99s", "red"},
		{"1m", "red"},
		{"5h", "red"},
		{"5x", "dim"},   // unknown unit
		{"  5s", "dim"}, // leading space → invalid digit
	}
	for _, tc := range cases {
		t.Run(tc.age, func(t *testing.T) {
			got := refreshColor(th, tc.age)
			want := mapCategoryToColor(tc.category, th)
			if got != want {
				t.Errorf("refreshColor(%q) = %v, want %v (%s)", tc.age, got, want, tc.category)
			}
		})
	}
}

func mapCategoryToColor(cat string, t *theme.Theme) any {
	switch cat {
	case "dim":
		return t.P.Dim
	case "fgfade":
		return t.P.FGFade
	case "yellow":
		return t.P.Yellow
	case "red":
		return t.P.Red
	}
	return nil
}
