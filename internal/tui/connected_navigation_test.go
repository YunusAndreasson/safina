package tui

import (
	"testing"

	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/YunusAndreasson/safina/internal/k8s"
	"github.com/YunusAndreasson/safina/internal/tui/sections"
	"github.com/stretchr/testify/require"
)

// connectedModel is the api app's page on its Details tab, with its
// connections loaded.
func connectedModel(t *testing.T) *Model {
	t.Helper()
	m := pageModel(t)
	m.handleKey(keyMsg("enter"))
	loadPods(m)
	m.handleKey(keyMsg("2"))
	m.connected.Apply(sections.ConnectedMsg{Kind: k8s.KindApp, SubKind: k8s.AppKindDeployment, Namespace: "shop", Name: "api", Connections: k8s.Connections{
		Uses:   []k8s.ConnectedRef{{Kind: k8s.KindSecret, Namespace: "shop", Name: "db"}, {Kind: k8s.KindConfigMap, Namespace: "shop", Name: "settings"}},
		UsedBy: []k8s.ConnectedRef{{Kind: k8s.KindService, Namespace: "shop", Name: "web"}},
	}})
	m.View()
	return m
}

func TestConnectedNavigationCanBeCancelledWhileLoading(t *testing.T) {
	m := connectedModel(t)
	source := m.tree.Path()
	m.openConnected(k8s.ConnectedRef{Kind: k8s.KindSecret, Namespace: "shop", Name: "db"})
	pending := m.tree.Path()
	require.Equal(t, "api", m.backLabel())
	m.handleKey(keyMsg("esc"))
	require.Equal(t, source, m.tree.Path(), "Esc cancels the jump and returns to its source")
	m.Update(sections.TreeChildrenLoadedMsg{Path: pending, Children: &sections.Children{Kind: k8s.KindSecret}})
	require.Nil(t, m.paletteTarget, "a late load must not finish the abandoned jump")
	require.Equal(t, source, m.tree.Path())
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
