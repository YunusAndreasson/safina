package sections

import (
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/YunusAndreasson/safina/internal/k8s"
	"github.com/YunusAndreasson/safina/internal/tui/theme"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestPlaceRowsExplainThemselvesWhenThereIsRoom(t *testing.T) {
	th := theme.Default()
	bg := lipgloss.NewStyle()
	wide := ansi.Strip(withFoldHint(th, "Infrastructure", "Infrastructure", k8s.KindInfrastructureFold, 60, bg))
	require.Contains(t, wide, "nodes and system namespaces")
	require.Equal(t, 60, ansi.StringWidth(wide))
	narrow := ansi.Strip(withFoldHint(th, "Infrastructure", "Infrastructure", k8s.KindInfrastructureFold, 24, bg))
	require.NotContains(t, narrow, "nodes", "a hint shows whole or not at all")
	require.Equal(t, 24, ansi.StringWidth(narrow))
	require.Empty(t, foldHint(k8s.KindNamespaceFolder), "a namespace's name is its explanation")
}

func TestNoMetricsTargetUsesSharedEmptyComposition(t *testing.T) {
	m := Metrics{}
	body := ansi.Strip(m.Body(theme.Default(), 64, 30, time.Now()))
	require.Contains(t, body, "#")
	require.Contains(t, body, "Select a workload, pod, or node to see metrics.")
}
