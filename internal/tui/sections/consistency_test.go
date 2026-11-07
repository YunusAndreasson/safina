package sections

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
	"github.com/yunus/safina/internal/k8s"
	"github.com/yunus/safina/internal/tui/theme"
)

func TestTabLayoutKeepsActiveViewAndMouseTargetsTogether(t *testing.T) {
	tabs := AvailableTabs(WSTabAvailability{Logs: true, Events: true, Yaml: true, Chat: true, Hot: true, Forwards: true, Drain: true})
	for _, width := range []int{4, 18, 28, 42, 60, 100, 200} {
		for _, active := range tabs {
			line := ansi.Strip(RenderWorkspaceTabStrip(theme.Default(), tabs, active, width))
			require.LessOrEqual(t, lipgloss.Width(line), width)
			hits := WorkspaceTabHitboxes(tabs, active, width)
			found := false
			for _, hit := range hits {
				require.LessOrEqual(t, hit.X1, width)
				visible := ansi.Cut(line, hit.X0, hit.X1)
				if hit.X1-hit.X0 >= lipgloss.Width(hit.Tab.Label()) {
					require.Equal(t, hit.Tab.Label(), visible)
				}
				found = found || hit.Tab == active
			}
			require.True(t, found, "active tab %v missing at width %d", active, width)
		}
	}
}

func TestHotMetricsStayVisibleInNarrowPanes(t *testing.T) {
	c := newEventsCtx()
	hot := NewHot()
	hot.items = []k8s.PodInfo{{Name: "payments-api-long-deployment-name-abc12", Namespace: "payments", CPU: 250, Memory: 64 * 1024 * 1024, Status: "Running"}}
	for _, width := range []int{28, 42, 60, 100, 150} {
		body := hot.Body(c, width, 12)
		require.LessOrEqual(t, lipgloss.Width(body), width)
		plain := ansi.Strip(body)
		require.Contains(t, plain, "250m")
		require.Contains(t, plain, "abc12")
		require.Contains(t, plain, "mem")
		lines := strings.Split(plain, "\n")
		prefix, _, found := strings.Cut(lines[2], "250m")
		require.True(t, found)
		require.Equal(t, strings.Index(lines[1], "cpu")+3, ansi.StringWidth(prefix)+4, "CPU values must align with their heading")
		require.Contains(t, plain, c.Keys.Refresh.Help().Key+" refresh")
		require.LessOrEqual(t, len(strings.Split(body, "\n")), 12)
	}
}
