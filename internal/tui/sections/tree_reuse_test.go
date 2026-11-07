package sections

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yunus/safina/internal/k8s"
	"github.com/yunus/safina/internal/tui/keys"
	"github.com/yunus/safina/internal/tui/theme"
	"github.com/yunus/safina/internal/tui/tuictx"
)

func TestWorkloadDrillReusesParentSnapshot(t *testing.T) {
	for _, kind := range []string{k8s.KindNamespaceFolder, k8s.KindNamespace, k8s.KindAllNamespacesFold} {
		t.Run(kind, func(t *testing.T) {
			tree := newTestTree(t)
			tree.path = append(tree.path, NodeRef{Kind: kind, Name: "ns"})
			parent := &Children{Kind: k8s.KindApp, Apps: []k8s.AppInfo{{Name: "api", Namespace: "ns", Kind: "Deployment", Health: k8s.HealthHealthy}}}
			tree.SetChildren(tree.path, parent)
			tree.Push(NodeRef{Kind: k8s.KindWorkloadsFold, Name: "Workloads"})
			require.NotNil(t, tree.Children(), "root can render immediately without another load")
			require.Equal(t, parent.Apps, tree.Children().Apps)
			require.Equal(t, "api", tree.SelectedRef().Name)
			// The immediate frame must match an ordinary completed load exactly.
			fresh := newTestTree(t)
			fresh.path = clonePath(tree.path)
			fresh.SetChildren(fresh.path, parent)
			ctx := tuictx.New(theme.Default(), keys.Default(), nil, nil)
			require.Equal(t, fresh.View(ctx, 100, 30), tree.View(ctx, 100, 30))
			tree.InvalidateFocus()
			tree.Pop()
			tree.Push(NodeRef{Kind: k8s.KindWorkloadsFold, Name: "Workloads"})
			require.Nil(t, tree.Children(), "invalidated revisits must reload")
		})
	}
}

func TestWorkloadReuseDoesNotSeedUnrelatedDrills(t *testing.T) {
	tree := newTestTree(t)
	tree.path = append(tree.path, NodeRef{Kind: k8s.KindNamespaceFolder, Name: "ns"})
	tree.SetChildren(tree.path, &Children{Kind: k8s.KindApp, Apps: []k8s.AppInfo{{Name: "api"}}})
	tree.Push(NodeRef{Kind: k8s.KindApp, Name: "api"})
	require.Nil(t, tree.Children(), "pod drill must fetch its own data")
}
