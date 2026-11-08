package sections

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/YunusAndreasson/safina/internal/k8s"
	"github.com/YunusAndreasson/safina/internal/tui/keys"
	"github.com/YunusAndreasson/safina/internal/tui/theme"
	"github.com/YunusAndreasson/safina/internal/tui/tuictx"
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
