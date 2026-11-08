package tui

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/runtime/schema"

	tea "charm.land/bubbletea/v2"
	"github.com/YunusAndreasson/safina/internal/k8s"
	"github.com/YunusAndreasson/safina/internal/tui/sections"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func connectedModel(t *testing.T) *Model {
	t.Helper()
	m := shiftFooterModel(t)
	m.syncSelection()
	m.connected.Apply(sections.ConnectedMsg{Kind: k8s.KindApp, SubKind: k8s.AppKindDeployment, Namespace: "shop", Name: "api", Connections: k8s.Connections{
		Uses:   []k8s.ConnectedRef{{Kind: k8s.KindSecret, Namespace: "shop", Name: "db"}, {Kind: k8s.KindConfigMap, Namespace: "shop", Name: "settings"}},
		UsedBy: []k8s.ConnectedRef{{Kind: k8s.KindService, Namespace: "shop", Name: "web"}},
	}})
	m.View()
	return m
}

func TestConnectedKeyboardOpensResourceAndEscapeReturnsToSource(t *testing.T) {
	m := connectedModel(t)
	m.handleKey(keyMsg("tab"))
	frame := ansi.Strip(m.View().Content)
	require.Contains(t, frame, "› Uses")
	m.handleKey(keyMsg("down"))
	m.View()
	m.handleKey(keyMsg("enter"))
	require.False(t, m.workspaceOwnsFocus())
	require.NotNil(t, m.paletteTarget)
	require.Equal(t, "settings", m.paletteTarget.Name)
	require.Equal(t, k8s.KindConfigFold, m.tree.Focus().Kind)
	// Simulate a cold-list result after the next key was already accepted.
	destination := m.tree.Path()
	m.Update(sections.TreeChildrenLoadedMsg{Path: destination, Children: &sections.Children{Kind: k8s.KindConfigMap, ConfigMaps: []k8s.ConfigMapInfo{{Namespace: "shop", Name: "settings"}}}})
	require.Equal(t, "settings", m.tree.SelectedRef().Name)
	require.Nil(t, m.paletteTarget)
	m.handleKey(keyMsg("esc"))
	require.Equal(t, k8s.KindContext, m.tree.Focus().Kind)
	require.NotNil(t, m.paletteTarget)
	require.Equal(t, "api", m.paletteTarget.Name)
	m.finishPaletteNavigation()
	require.Equal(t, "api", m.tree.SelectedRef().Name)
	require.Empty(t, m.connectedBack)
}

func TestConnectedClickTargetsTheRenderedResource(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 36}} {
		m := connectedModel(t)
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		lines := strings.Split(ansi.Strip(m.View().Content), "\n")
		clicked := false
		for y, line := range lines {
			if x := strings.Index(line, "Secret db"); x >= 0 {
				m.Update(tea.MouseClickMsg{X: x + 7, Y: y, Button: tea.MouseLeft})
				clicked = true
				break
			}
		}
		require.True(t, clicked, "resource must be visible at %v", size)
		require.NotNil(t, m.paletteTarget)
		require.Equal(t, "db", m.paletteTarget.Name)
		require.Equal(t, k8s.KindSecretsFold, m.tree.Focus().Kind)
	}
}

func TestConnectedNavigationCanBeCancelledWhileLoading(t *testing.T) {
	m := connectedModel(t)
	m.openConnected(k8s.ConnectedRef{Kind: k8s.KindSecret, Namespace: "shop", Name: "db"})
	oldPath := m.tree.Path()
	m.handleKey(keyMsg("esc"))
	require.NotNil(t, m.paletteTarget)
	require.Equal(t, "api", m.paletteTarget.Name)
	m.Update(sections.TreeChildrenLoadedMsg{Path: oldPath, Children: &sections.Children{Kind: k8s.KindSecret}})
	require.Equal(t, "api", m.paletteTarget.Name, "late link load must not steal the pending return")
}

func TestConnectedDestinationsCoverCoreAndDiscoveredKinds(t *testing.T) {
	for _, tc := range []struct {
		kind, subKind, fold string
		gvr                 schema.GroupVersionResource
	}{
		{k8s.KindApp, k8s.AppKindDeployment, k8s.KindWorkloadsFold, schema.GroupVersionResource{}},
		{k8s.KindSecret, "", k8s.KindSecretsFold, schema.GroupVersionResource{}},
		{k8s.KindService, "", k8s.KindNetworkFold, schema.GroupVersionResource{}},
		{k8s.KindServiceAccount, "", k8s.KindCustomFold, schema.GroupVersionResource{Group: "", Version: "v1", Resource: "serviceaccounts"}},
		{k8s.KindHPA, "", k8s.KindCustomFold, schema.GroupVersionResource{Group: "autoscaling", Version: "v2", Resource: "horizontalpodautoscalers"}},
		{"ReplicaSet", "", k8s.KindCustomFold, schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "replicasets"}},
		{"Widget", "", k8s.KindCustomFold, schema.GroupVersionResource{Group: "example.io", Version: "v1", Resource: "widgets"}},
	} {
		e, err := connectedEntry(k8s.ConnectedRef{Kind: tc.kind, SubKind: tc.subKind, Namespace: "other", Name: "target"}, nil, "demo", []k8s.ResourceKind{{Kind: "Widget", GroupVersionResource: schema.GroupVersionResource{Group: "example.io", Version: "v1", Resource: "widgets"}, Namespaced: true}})
		require.NoError(t, err)
		require.Equal(t, "other", e.Path[2].Namespace)
		require.Equal(t, tc.fold, e.Path[len(e.Path)-2].Kind)
		require.Equal(t, tc.gvr, e.Path[len(e.Path)-1].GVR)
	}
	_, err := connectedEntry(k8s.ConnectedRef{Kind: "Unknown", Name: "x"}, nil, "demo", nil)
	require.Error(t, err)
}

func TestConnectedClusterScopedOwnerIgnoresSourceNamespace(t *testing.T) {
	entry, err := connectedEntry(k8s.ConnectedRef{Kind: k8s.KindNode, Namespace: "kube-system", Name: "node"}, nil, "demo", nil)
	require.NoError(t, err)
	require.Empty(t, entry.Namespace)
	require.Len(t, entry.Path, 4)
	require.Equal(t, k8s.KindCustomFold, entry.Path[2].Kind)
	require.Equal(t, schema.GroupVersionResource{Group: "", Version: "v1", Resource: "nodes"}, entry.Path[2].GVR)
}

func TestConnectedInspectorZoomAndTabRestoreTree(t *testing.T) {
	m := connectedModel(t)
	m.handleKey(keyMsg("tab"))
	m.handleKey(keyMsg("z"))
	require.True(t, m.workspaceExpanded)
	frame := ansi.Strip(m.View().Content)
	require.Contains(t, frame, "Secret db")
	require.Equal(t, 1, m.tabStripOriginX)
	m.handleKey(keyMsg("tab"))
	require.False(t, m.workspaceExpanded)
	require.False(t, m.workspaceOwnsFocus())
	m.View()
	require.Greater(t, m.tabStripOriginX, 1)
}
