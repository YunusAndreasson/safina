package sections

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/YunusAndreasson/safina/internal/k8s"
	"github.com/YunusAndreasson/safina/internal/tui/theme"
)

// TestAvailableTabs_OrderAndPresence pins the resource page's strip: what
// the resource holds first, then what it is (Details, always present), then
// how it behaves. Tool pages never appear in a page's strip.
func TestAvailableTabs_OrderAndPresence(t *testing.T) {
	cases := map[string]struct {
		avail WSTabAvailability
		want  []WorkspaceTab
	}{
		"a leaf has only Details": {
			avail: WSTabAvailability{},
			want:  []WorkspaceTab{WSTabDetails},
		},
		"an app holds pods": {
			avail: WSTabAvailability{Children: true, Logs: true, Events: true, Yaml: true, Metrics: true},
			want:  []WorkspaceTab{WSTabChildren, WSTabDetails, WSTabLogs, WSTabEvents, WSTabYaml, WSTabMetrics},
		},
		"a ConfigMap": {
			avail: WSTabAvailability{Events: true, Yaml: true},
			want:  []WorkspaceTab{WSTabDetails, WSTabEvents, WSTabYaml},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, AvailableTabs(tc.avail))
		})
	}
}

func TestTabLabelsAreNumberedByPositionAndNameTheChildren(t *testing.T) {
	tabs := AvailableTabs(WSTabAvailability{Children: true, Logs: true})
	assert.Equal(t, []string{"1 Pods", "2 Details", "3 Logs"}, TabLabels(tabs, k8s.KindApp))
	assert.Equal(t, []string{"1 Containers", "2 Details", "3 Logs"}, TabLabels(tabs, k8s.KindPod))
	assert.Empty(t, ChildrenLabel(k8s.KindConfigMap), "a leaf has no children tab")
}

// TestWorkspaceTabHitboxes_Disjoint asserts that the per-tab x-ranges are
// non-overlapping and ascending, so a click never routes to two tabs.
func TestWorkspaceTabHitboxes_Disjoint(t *testing.T) {
	tabs := AvailableTabs(WSTabAvailability{Children: true, Logs: true, Events: true, Yaml: true})
	hits := WorkspaceTabHitboxes(tabs, k8s.KindApp, WSTabDetails, "", 200)
	require.Len(t, hits, len(tabs))
	for i := 1; i < len(hits); i++ {
		assert.GreaterOrEqual(t, hits[i].X0, hits[i-1].X1)
	}
	for _, h := range hits {
		assert.Greater(t, h.X1, h.X0, "every hitbox must have width > 0")
	}
}

// TestWorkspaceTabHitboxes_TruncatesPastWidth: when the strip would
// overflow, the hitboxes stop at the visible region.
func TestWorkspaceTabHitboxes_TruncatesPastWidth(t *testing.T) {
	tabs := AvailableTabs(WSTabAvailability{Children: true, Logs: true, Events: true, Yaml: true, Metrics: true})
	full := WorkspaceTabHitboxes(tabs, k8s.KindApp, WSTabChildren, "", 200)
	require.Len(t, full, len(tabs))
	narrow := WorkspaceTabHitboxes(tabs, k8s.KindApp, WSTabChildren, "", 30)
	assert.Less(t, len(narrow), len(tabs))
	for _, h := range narrow {
		assert.LessOrEqual(t, h.X1, 30)
	}
}

func TestWorkspaceTabHitboxes_EmptyInput(t *testing.T) {
	assert.Nil(t, WorkspaceTabHitboxes(nil, k8s.KindApp, WSTabDetails, "", 80))
}

// TestRenderTabRule_DegradesGracefully covers the width regimes: fits,
// drops trailing tabs with "…", and clips a single tab.
func TestRenderTabRule_DegradesGracefully(t *testing.T) {
	th := theme.Default()
	tabs := []WorkspaceTab{WSTabDetails, WSTabLogs, WSTabEvents, WSTabYaml}
	wide := ansi.Strip(RenderTabRule(th, tabs, k8s.KindPod, WSTabLogs, "", 200))
	for _, label := range TabLabels(tabs, k8s.KindPod) {
		assert.Contains(t, wide, label)
	}
	assert.Contains(t, RenderTabRule(th, tabs, k8s.KindPod, WSTabDetails, "", 25), "…")
	assert.NotEmpty(t, RenderTabRule(th, tabs, k8s.KindPod, WSTabYaml, "", 4))
}

func TestTabRuleCarriesTheLabelsAndTheTabState(t *testing.T) {
	th := theme.Default()
	tabs := []WorkspaceTab{WSTabDetails, WSTabLogs}
	rule := ansi.Strip(RenderTabRule(th, tabs, k8s.KindPod, WSTabLogs, "312 lines", 50))
	assert.Equal(t, "─ 1 Details ─━ 2 Logs ━"+strings.Repeat("─", 15)+" 312 lines ─", rule)
	assert.Equal(t, 50, ansi.StringWidth(rule))
	// The state gives way when the tabs need the room.
	narrow := ansi.Strip(RenderTabRule(th, AvailableTabs(WSTabAvailability{Children: true, Logs: true, Events: true, Yaml: true, Metrics: true}), k8s.KindPod, WSTabLogs, "312 lines", 60))
	assert.NotContains(t, narrow, "312 lines")
}
