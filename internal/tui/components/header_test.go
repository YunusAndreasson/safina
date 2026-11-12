package components

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"

	"github.com/YunusAndreasson/safina/internal/tui/theme"
)

func TestPageHeaderFillsItsRowsAndGivesWayInOrder(t *testing.T) {
	in := HeaderInput{
		Context: "eu-north-1", Cluster: "gke_shop_eu-north-1", User: "oncall", ServerVersion: "v1.31.2",
		Health: ClusterHealth{Show: true, NodesReady: 3, NodesTotal: 3, PodsRunning: 40, PodsCrashing: 2, PodsTotal: 42, CPUFrac: 0.36, MemFrac: 0.81},
		Keys: []KeyDesc{
			{Key: "l", Desc: "logs"},
			{Key: "e", Desc: "events"},
			{Key: "d", Desc: "yaml"},
			{Key: "m", Desc: "metrics"},
			{Key: "s", Desc: "shell"},
			{Key: "f", Desc: "forward"},
		},
		AppVersion: "v0.3.0",
	}
	th := theme.Default()
	wide := ansi.Strip(PageHeader(th, 160, in))
	require.Contains(t, wide, "context  eu-north-1")
	require.Contains(t, wide, "2 crashing")
	require.Contains(t, wide, "36%")
	require.Contains(t, wide, "m metrics")
	require.Contains(t, wide, "f forward")
	require.Contains(t, wide, "s a f i n a")
	narrow := ansi.Strip(PageHeader(th, 100, in))
	require.NotContains(t, narrow, "s a f i n a", "the sail gives way before the keys")
	require.Contains(t, narrow, "l logs")
	for _, w := range []int{60, 100, 130, 160, 220} {
		out := PageHeader(th, w, in)
		require.Equal(t, HeaderRows, lipgloss.Height(out))
		for _, row := range strings.Split(out, "\n") {
			require.Equal(t, w, lipgloss.Width(row), "width %d", w)
		}
		require.Contains(t, ansi.Strip(out), "eu-north-1", "the cluster never gives way")
	}
}
