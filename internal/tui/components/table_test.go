package components

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/YunusAndreasson/safina/internal/tui/theme"
)

func TestTruncPadDisplayWidth(t *testing.T) {
	for _, tc := range []struct {
		name, input, want string
		width             int
	}{
		{"wide", "界界界", "界… ", 4},
		{"combining", "e\u0301xyz", "e\u0301x…", 3},
		{"emoji", "👩‍💻xyz", "👩‍💻…", 3},
		{"styled", "\x1b[31mabcdef\x1b[0m", "abc…", 4},
		{"exact", "界界", "界界", 4},
		{"minimum", "abc", "…", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := TruncPad(tc.input, tc.width)
			if plain := ansi.Strip(got); plain != tc.want {
				t.Errorf("text = %q, want %q", plain, tc.want)
			}
			if width := lipgloss.Width(got); width != max(1, tc.width) {
				t.Errorf("width = %d, want %d", width, max(1, tc.width))
			}
		})
	}
}

// TestHeader_WidthMatchesSpec asserts the Header's rendered visual width is
// exactly sumWidths + (n-1)*gap + 2*edgePad. If lipgloss's display-width math
// disagrees with ours for any label content, the panel's right border ends
// up in the wrong column and downstream rows leak past it. (Discovered by
// the PTY harness when the Containers header rendered 8 cells short.)
func TestHeader_WidthMatchesSpec(t *testing.T) {
	th := theme.Default()
	cases := []struct {
		name   string
		labels []string
		widths []int
		aligns []Align
	}{
		{
			name:   "containers_header_with_middle_dot",
			labels: []string{"", "NAME · IMAGE", "STATE"},
			widths: []int{1, 73, 14},
			aligns: []Align{AlignLeft, AlignLeft, AlignLeft},
		},
		{
			name:   "pods_header",
			labels: []string{"", "NAME", "STATUS", "RST", "AGE"},
			widths: []int{1, 50, 14, 5, 5},
			aligns: []Align{AlignLeft, AlignLeft, AlignLeft, AlignRight, AlignRight},
		},
		{
			name:   "single_long_label",
			labels: []string{"DESCRIPTION"},
			widths: []int{20},
			aligns: []Align{AlignLeft},
		},
		{
			name:   "exact_fit_label",
			labels: []string{"FOO"},
			widths: []int{3}, // label exactly fills cell
			aligns: []Align{AlignLeft},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := Header(th, tc.labels, tc.widths, tc.aligns)
			want := sumInts(tc.widths) + GapTotal(len(tc.widths)) + EdgeTotal()
			got := lipgloss.Width(out)
			if got != want {
				t.Errorf("Header rendered width = %d, want %d (sum=%d gaps=%d edges=%d)\nlabels=%v widths=%v\noutput chars: %q",
					got, want, sumInts(tc.widths), GapTotal(len(tc.widths)), EdgeTotal(),
					tc.labels, tc.widths, stripStyling(out))
			}
		})
	}
}

func sumInts(ws []int) int {
	s := 0
	for _, w := range ws {
		s += w
	}
	return s
}

// stripStyling returns the non-ANSI characters of s — handy for diagnosing
// which cells got rendered when the test fails.
func stripStyling(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 0x20 || r == '\t' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// TestRow_WidthMatchesSpec same property for the data Row helper.
func TestRow_WidthMatchesSpec(t *testing.T) {
	th := theme.Default()
	cases := []struct {
		name   string
		vals   []string
		widths []int
		aligns []Align
	}{
		{"three_cols", []string{"▾", "logger · busybox", "Running"}, []int{1, 73, 14}, []Align{AlignLeft, AlignLeft, AlignLeft}},
		{"value_overflows", []string{"X", "ababababababababababab", "ok"}, []int{1, 5, 5}, []Align{AlignLeft, AlignLeft, AlignLeft}},
		{"mid_dot_in_value", []string{"K", "logger · long-image-name:v1.2.3", "S"}, []int{1, 30, 5}, []Align{AlignLeft, AlignLeft, AlignLeft}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := Row(th, tc.vals, tc.widths, tc.aligns, false, 0)
			want := sumInts(tc.widths) + GapTotal(len(tc.widths)) + EdgeTotal()
			got := lipgloss.Width(out)
			if got != want {
				t.Errorf("Row rendered width = %d, want %d", got, want)
			}
		})
	}
}
