package sections

import (
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
	"github.com/yunus/safina/internal/k8s"
	"github.com/yunus/safina/internal/tui/theme"
)

func TestResourceOrientationFitsAndExcludesRealResources(t *testing.T) {
	for _, ref := range []NodeRef{{}, {Kind: k8s.KindWorkloadsFold, Name: "Workloads"}, {Kind: k8s.KindResourceBrowser}, {Kind: k8s.KindInfrastructureFold}} {
		for _, size := range [][2]int{{16, 4}, {28, 12}, {48, 20}, {70, 32}} {
			frame, ok := ResourceOrientation(theme.Default(), ref, size[0], size[1])
			require.True(t, ok)
			require.Equal(t, size[1], lipgloss.Height(frame))
			for _, line := range strings.Split(frame, "\n") {
				require.Equal(t, size[0], ansi.StringWidth(line))
			}
		}
	}
	for _, kind := range []string{k8s.KindApp, k8s.KindPod, k8s.KindNamespaceFolder, k8s.KindNodePool, k8s.KindGenericResource} {
		_, ok := ResourceOrientation(theme.Default(), NodeRef{Kind: kind, Name: "real-resource"}, 70, 32)
		require.False(t, ok, "%s retains its facts and events", kind)
	}
}

func TestNoMetricsTargetUsesSharedEmptyComposition(t *testing.T) {
	m := Metrics{}
	body := ansi.Strip(m.Body(theme.Default(), 64, 30, time.Now()))
	require.Contains(t, body, "#")
	require.Contains(t, body, "Select a workload, pod, or node to see metrics.")
}
