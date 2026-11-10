package components

import (
	"math/rand/v2"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/require"

	"github.com/YunusAndreasson/safina/internal/tui/theme"
)

// A row built without lipgloss must be byte-identical to the lipgloss row,
// for plain and styled values, any alignment, and values that exactly fill,
// underfill or are empty. Values that overflow or wrap take the lipgloss
// path, so they match by construction; they are mixed in to prove the
// fallback triggers.
func TestFastRowMatchesLipgloss(t *testing.T) {
	th := theme.Default()
	red := lipgloss.NewStyle().Foreground(th.P.Red)
	bold := lipgloss.NewStyle().Bold(true).Background(th.P.BGRow)
	values := []string{
		"", "a", "api-7f77847d67-x2x9k", "Running", "2d", "↻ 3", "⠋", "●", "…",
		red.Render("CrashLoopBackOff"), bold.Render("12m"), "▁▂▃▅▇", "日本", "🚀",
		"é", "two words", "tab\there", "line\nbreak", " lead", "trail ",
	}
	rng := rand.New(rand.NewPCG(5, 6)) //nolint:gosec // reproducible test input
	fast := 0
	for range 20000 {
		n := 1 + rng.IntN(7)
		vals, widths, aligns := make([]string, n), make([]int, n), make([]Align, n)
		for i := range n {
			vals[i] = values[rng.IntN(len(values))]
			widths[i] = rng.IntN(22)
			aligns[i] = Align(rng.IntN(2))
		}
		if rng.IntN(4) == 0 {
			aligns = aligns[:rng.IntN(n)]
		}
		selected, idx := rng.IntN(5) == 0, rng.IntN(4)
		want := styledRow(th, vals, widths, aligns, selected, idx)
		got, ok := fastRow(th, vals, widths, aligns, selected, idx)
		if ok {
			fast++
			require.Equal(t, want, got, "vals %q widths %v aligns %v selected %v idx %d", vals, widths, aligns, selected, idx)
		}
		require.Equal(t, want, Row(th, vals, widths, aligns, selected, idx))
	}
	require.Greater(t, fast, 2000, "most fitting rows should take the fast path")
	require.True(t, paintFor(th, false, 0).ok && paintFor(th, true, 0).ok && paintFor(th, false, 1).ok)
}

func BenchmarkRow(b *testing.B) {
	th := theme.Default()
	vals := []string{"●", "api-123-7f77847d67-00123", "shop-1", "Running", "0", "2d", "120m", "64Mi"}
	widths := []int{1, 40, 12, 18, 4, 5, 8, 8}
	aligns := []Align{AlignLeft, AlignLeft, AlignLeft, AlignLeft, AlignRight, AlignRight, AlignRight, AlignRight}
	b.Run("fast", func(b *testing.B) {
		for b.Loop() {
			Row(th, vals, widths, aligns, false, 1)
		}
	})
	b.Run("lipgloss", func(b *testing.B) {
		for b.Loop() {
			styledRow(th, vals, widths, aligns, false, 1)
		}
	})
}
