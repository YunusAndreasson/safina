package components

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
	"github.com/yunus/safina/internal/tui/theme"
)

func TestTopbarKeepsClusterVisibleBeforeHealthAndClock(t *testing.T) {
	crumbs := []Crumb{{Label: "Clusters"}, {Label: "eu-north-1", Pinned: true}, {Label: "storefront"}, {Label: "Workloads"}, {Label: strings.Repeat("app-", 20)}}
	for _, width := range []int{50, 60, 80, 140} {
		bar := Topbar(theme.Default(), width, crumbs, 2, ClusterHealth{Show: true}, false)
		require.Contains(t, ansi.Strip(bar), "eu-north-1")
		require.Contains(t, ansi.Strip(bar), "2 fwd")
		require.LessOrEqual(t, lipgloss.Width(bar), width)
		require.Equal(t, 1, lipgloss.Height(bar))
	}
}
