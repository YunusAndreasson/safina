package components

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/YunusAndreasson/safina/internal/tui/theme"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestTopbarKeepsClusterVisibleBeforeHealthAndClock(t *testing.T) {
	crumbs := []Crumb{{Label: "Clusters"}, {Label: "eu-north-1", Pinned: true}, {Label: "storefront"}, {Label: "Workloads"}, {Label: strings.Repeat("app-", 20)}}
	for _, width := range []int{50, 60, 80, 140} {
		bar := Topbar(theme.Default(), width, TopbarInput{Crumbs: crumbs, Forwards: 2, Health: ClusterHealth{Show: true}})
		require.Contains(t, ansi.Strip(bar), "eu-north-1")
		require.Contains(t, ansi.Strip(bar), "2 fwd")
		require.LessOrEqual(t, lipgloss.Width(bar), width)
		require.Equal(t, 1, lipgloss.Height(bar))
	}
}

func TestTopbarStatusFollowsTheLeafAndHealthYieldsFirst(t *testing.T) {
	crumbs := []Crumb{{Label: "eu-north-1", Pinned: true}, {Label: "ledger"}, {Label: "Workloads"}, {Label: "ledger-api"}}
	in := TopbarInput{Crumbs: crumbs, Status: "Deployment · Progressing · 0/2 ready", StatusShort: "Deployment · Progressing", Notice: "✓ restarted", Health: ClusterHealth{Show: true, NodesReady: 3, NodesTotal: 3, PodsTotal: 9, PodsRunning: 9}}
	wide := ansi.Strip(Topbar(theme.Default(), 160, in))
	require.Contains(t, wide, "ledger-api  Deployment · Progressing · 0/2 ready")
	require.Contains(t, wide, "✓ restarted")
	require.Contains(t, wide, "Nodes 3/3")
	narrow := ansi.Strip(Topbar(theme.Default(), 100, in))
	require.NotContains(t, narrow, "Nodes", "health yields before the status")
	require.Contains(t, narrow, "Progressing")
	require.Contains(t, narrow, "✓ restarted")
	for _, w := range []int{50, 70, 100, 160} {
		bar := Topbar(theme.Default(), w, in)
		require.LessOrEqual(t, lipgloss.Width(bar), w)
		require.Equal(t, 1, lipgloss.Height(bar))
		require.Contains(t, ansi.Strip(bar), "eu-north-1")
	}
}
