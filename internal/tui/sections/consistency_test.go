package sections

import (
	"slices"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/YunusAndreasson/safina/internal/k8s"
	"github.com/YunusAndreasson/safina/internal/tui/theme"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestTabLayoutKeepsActiveViewAndMouseTargetsTogether(t *testing.T) {
	tabs := AvailableTabs(WSTabAvailability{Children: true, Logs: true, Events: true, Yaml: true, Metrics: true})
	labels := TabLabels(tabs, k8s.KindApp)
	for _, state := range []string{"", "1 204 lines · ● follow"} {
		for _, width := range []int{4, 18, 28, 42, 60, 100, 200} {
			for _, active := range tabs {
				rule := ansi.Strip(RenderTabRule(theme.Default(), tabs, k8s.KindApp, active, state, width))
				require.Equal(t, width, lipgloss.Width(rule), "the rule spans the page")
				hits := WorkspaceTabHitboxes(tabs, k8s.KindApp, active, state, width)
				found := false
				for _, hit := range hits {
					require.LessOrEqual(t, hit.X1, width)
					visible := ansi.Cut(rule, hit.X0, hit.X1)
					label := labels[slices.Index(tabs, hit.Tab)]
					if hit.X1-hit.X0 >= lipgloss.Width(label) {
						require.Equal(t, label, visible)
					}
					if hit.Tab == active {
						found = true
						if hit.X0 >= 2 && hit.X1+1 < width {
							// Heavy cells bracket the active label.
							require.Equal(t, "━", ansi.Cut(rule, hit.X0-2, hit.X0-1))
							require.Equal(t, "━", ansi.Cut(rule, hit.X1+1, hit.X1+2))
						}
					}
				}
				require.True(t, found, "active tab %v missing at width %d", active, width)
				if width >= 18 {
					require.Equal(t, 2, strings.Count(rule, "━"), "only the active tab is marked at width %d", width)
				}
			}
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
		prefix, _, found := strings.Cut(lines[1], "250m")
		require.True(t, found)
		require.Equal(t, strings.Index(lines[0], "cpu")+3, ansi.StringWidth(prefix)+4, "CPU values must align with their heading")
		require.LessOrEqual(t, len(strings.Split(body, "\n")), 12)
	}
}
