package tui

import (
	"errors"
	"testing"

	"k8s.io/apimachinery/pkg/runtime/schema"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
	"github.com/yunus/safina/internal/config"
	"github.com/yunus/safina/internal/k8s"
	"github.com/yunus/safina/internal/tui/sections"
)

func emptyResourceModel(t *testing.T) *Model {
	t.Helper()
	m := shiftFooterModel(t)
	m.tree.Push(sections.NodeRef{Kind: k8s.KindCustomFold, Name: "Pods", Display: "Pods", GVR: schema.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"}})
	m.tree.SetChildren(m.tree.Path(), &sections.Children{Kind: k8s.KindPod})
	return m
}

func TestEmptyInspectorUsesOneIllustrationAndNoVacantTiles(t *testing.T) {
	m := emptyResourceModel(t)
	body := ansi.Strip(m.renderTabBody(sections.WSTabInspector, 64, 30))
	require.Contains(t, body, "No resources to inspect")
	require.Contains(t, body, "#")
	require.NotContains(t, body, "nothing selected")
	require.NotContains(t, body, "Recent events")
	require.NotContains(t, body, "Connected")
	require.NotContains(t, ansi.Strip(m.tree.View(m.ctx, 40, 30)), "#", "only the detail pane gets artwork")
}

func TestNoSelectionArtworkDoesNotMaskLoadingErrorsOrFilters(t *testing.T) {
	for _, state := range []string{"loading", "error", "filter"} {
		t.Run(state, func(t *testing.T) {
			m := emptyResourceModel(t)
			want := ""
			switch state {
			case "loading":
				m.tree.Loading = true
				want = "Loading resources"
			case "error":
				m.tree.LoadErr = errors.New("permission denied")
				want = "Resource details unavailable"
			case "filter":
				m.tree.Focused = true
				m.tree.Update(keyMsg("/"), m.ctx)
				m.tree.Update(tea.PasteMsg{Content: "missing"}, m.ctx)
				m.tree.Update(keyMsg("enter"), m.ctx)
				want = "No matching resources"
			}
			body := ansi.Strip(m.renderTabBody(sections.WSTabInspector, 64, 30))
			require.Contains(t, body, want)
			require.NotContains(t, body, "#")
		})
	}
}

func TestNavigationFolderGetsOrientationWithoutHidingResourceDetails(t *testing.T) {
	m := resourceBrowserModel(t)
	m.tree.SetEnabledResources(config.DefaultEnabledResources())
	m.tree.SetCursorByRef(k8s.KindWorkloadsFold, "", "Workloads")
	body := ansi.Strip(m.renderTabBody(sections.WSTabInspector, 64, 30))
	require.Contains(t, body, "Press Enter to list resources.")
	require.Contains(t, body, "#")
	require.Zero(t, m.connected.PanelHeight)
	m.tree.Push(m.tree.SelectedRef())
	m.tree.SetChildren(m.tree.Path(), &sections.Children{Kind: k8s.KindApp, Apps: []k8s.AppInfo{{Name: "api", Namespace: "shop", Kind: "Deployment", Health: k8s.HealthHealthy}}})
	m.syncSelection()
	body = ansi.Strip(m.renderTabBody(sections.WSTabInspector, 64, 30))
	require.Contains(t, body, "Deployment")
	require.Contains(t, body, "Recent events")
	require.NotContains(t, body, "Press Enter to list resources.")
}
