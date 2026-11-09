package sections

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/YunusAndreasson/safina/internal/k8s"
	"github.com/YunusAndreasson/safina/internal/tui/keys"
	"github.com/YunusAndreasson/safina/internal/tui/theme"
	"github.com/YunusAndreasson/safina/internal/tui/tuictx"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestTreePreparedRowsTrackSortFilterAndPublishedData(t *testing.T) {
	c := tuictx.New(theme.Default(), keys.Default(), nil, nil)
	for _, kind := range []string{k8s.KindApp, k8s.KindPod} {
		t.Run(kind, func(t *testing.T) {
			tree := NewTree(c)
			tree.path = append(tree.path, NodeRef{Kind: k8s.KindWorkloadsFold})
			tree.children = &Children{
				Kind: kind,
				Apps: []k8s.AppInfo{{Name: "alpha", Kind: "Deployment"}, {Name: "bravo", Kind: "Deployment"}},
				Pods: []k8s.PodInfo{{Name: "alpha", Status: "Running"}, {Name: "bravo", Status: "CrashLoopBackOff"}},
			}
			tree.alphaSort = false
			before := tree.View(c, 100, 20)
			require.Contains(t, before, "alpha")
			require.Contains(t, before, "bravo")
			tree.alphaSort = true
			after := tree.View(c, 100, 20)
			require.Less(t, strings.Index(after, "alpha"), strings.Index(after, "bravo"))
			tree.filter.Open()
			tree.filter.Update(tea.PasteMsg{Content: "bravo"})
			tree.filter.Close()
			after = tree.View(c, 100, 20)
			require.NotContains(t, after, "alpha")
			require.Contains(t, after, "bravo")
			// Publish an in-place refresh, then replace the snapshot entirely.
			// Neither may reuse the previous data's derived order.
			tree.children.Apps[1].Name = "charlie"
			tree.children.Pods[1].Name = "charlie"
			tree.SetChildren(tree.path, tree.children)
			require.Contains(t, tree.View(c, 100, 20), "No matching resources")
			tree.filter.Clear()
			require.Contains(t, tree.View(c, 100, 20), "charlie")
			tree.children = &Children{Kind: kind}
			after = tree.View(c, 100, 20)
			require.NotContains(t, after, "charlie")
		})
	}
}

// Node names share a long pool prefix. In the default split the Nodes table
// used to cut every name to the same "pool-a-wo…" and push PODS off the edge.
func TestNodeRowsTellLongNamesApartInTheDefaultSplit(t *testing.T) {
	c := tuictx.New(theme.Default(), keys.Default(), nil, nil)
	tree := NewTree(c)
	tree.path = append(tree.path, NodeRef{Kind: k8s.KindNode, Display: "Nodes"})
	names := []string{"pool-a-workers-3f9c0e1d2b4a", "pool-a-workers-8e7d6c5b4a39", "pool-a-master1"}
	nodes := make([]k8s.NodeInfo, 0, len(names))
	for _, n := range names {
		nodes = append(nodes, k8s.NodeInfo{Name: n, Status: "Ready", Schedulable: true, PodsCurrent: 14, PodsMax: 110})
	}
	tree.children = &Children{Kind: k8s.KindNode, Nodes: nodes}
	view := ansi.Strip(tree.View(c, 59, 12))
	for _, row := range strings.Split(view, "\n") {
		require.LessOrEqual(t, lipgloss.Width(row), 59, row)
	}
	for _, suffix := range []string{"d2b4a", "b4a39", "master1"} {
		require.Contains(t, view, suffix)
	}
	require.Equal(t, len(names), strings.Count(view, "14/110"))
}

// Helm releases name workloads release-chart-component, so they differ at
// the end; cutting from the right left rows that all read "checkout-ap…".
func TestWorkloadRowsKeepTheDistinguishingSuffix(t *testing.T) {
	c := tuictx.New(theme.Default(), keys.Default(), nil, nil)
	tree := NewTree(c)
	tree.path = append(tree.path, NodeRef{Kind: k8s.KindWorkloadsFold})
	tree.children = &Children{Kind: k8s.KindApp, Apps: []k8s.AppInfo{
		{Name: "checkout-api-eu-canary", Namespace: "storefront", Kind: "Deployment"},
		{Name: "checkout-api-eu-stable", Namespace: "storefront", Kind: "Deployment"},
		{Name: "gateway", Namespace: "edge", Kind: "Deployment"},
	}}
	view := ansi.Strip(tree.View(c, 59, 10))
	require.Contains(t, view, "anary")
	require.Contains(t, view, "table")
}
