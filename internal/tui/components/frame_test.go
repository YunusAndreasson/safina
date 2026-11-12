package components

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"

	"github.com/YunusAndreasson/safina/internal/tui/textw"
	"github.com/YunusAndreasson/safina/internal/tui/theme"
)

func TestFrameEdgeSetsTheTextIntoTheRuleAtExactWidth(t *testing.T) {
	th := theme.Default()
	pad := func(l, r string, w int) string {
		return " " + l + strings.Repeat(" ", max(0, w-2-len(l)-len(r))) + r + " "
	}
	for _, w := range []int{8, 20, 60, 160} {
		for _, line := range []string{
			pad("prod > shop", "restarted", w),
			pad("prod > shop", "", w),
			pad("", "cached", w),
			pad("", "", w),
			pad(strings.Repeat("x", w), "", w),
		} {
			line = ansi.Truncate(line, w, "")
			for _, top := range []bool{true, false} {
				got := FrameEdge(th, line, w, top)
				require.Equal(t, w, textw.Width(got), "w=%d line=%q", w, line)
			}
		}
	}
	got := ansi.Strip(FrameEdge(th, pad("prod > shop", "restarted", 40), 40, true))
	require.True(t, strings.HasPrefix(got, "╭─ prod > shop ─"), got)
	require.True(t, strings.HasSuffix(got, "─ restarted ─╮"), got)
}
