package sections

import (
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
	"github.com/yunus/safina/internal/config"
	"github.com/yunus/safina/internal/k8s"
	"github.com/yunus/safina/internal/tui/keys"
	"github.com/yunus/safina/internal/tui/theme"
	"github.com/yunus/safina/internal/tui/tuictx"
)

func TestTreeRowProjectionMatchesReferenceList(t *testing.T) {
	c := tuictx.New(theme.Default(), keys.Default(), nil, nil)
	for _, kind := range []string{k8s.KindApp, k8s.KindPod} {
		for _, focus := range []string{k8s.KindWorkloadsFold, k8s.KindNamespaceFolder, k8s.KindNamespace, k8s.KindAllNamespacesFold} {
			for _, enabled := range [][]config.ResourceRef{nil, config.DefaultEnabledResources()} {
				for _, query := range []string{"", "api", "missing"} {
					for _, alpha := range []bool{false, true} {
						for _, cursor := range []int{-1, 0, 12, 39, 40} {
							tree := NewTree(c)
							tree.enabledResources = enabled
							tree.path = append(tree.path, NodeRef{Kind: focus})
							tree.alphaSort = alpha
							tree.cursor = cursor
							tree.children = &Children{Kind: kind}
							for i := range 40 {
								name := fmt.Sprintf("api-%03d", 40-i)
								ns := fmt.Sprintf("ns-%d", i%3)
								tree.children.Apps = append(tree.children.Apps, k8s.AppInfo{Name: name, Namespace: ns, Kind: "Deployment", Ready: "1/1", Health: k8s.Health(i % 4), HPA: &k8s.HPABadge{Current: 2}, CPU: int64(i)})
								tree.children.Pods = append(tree.children.Pods, k8s.PodInfo{Name: name, Namespace: ns, Status: "Running", Restarts: int32(i), CPU: int64(i)})
							}
							tree.filter.Open()
							tree.filter.Update(tea.PasteMsg{Content: query})
							tree.filter.Close()
							rows := tree.computeVisibleRows()
							require.Equal(t, len(rows), tree.visibleCount())
							if cursor >= 0 && cursor < len(rows) {
								require.Equal(t, rows[cursor], tree.SelectedRef())
							} else {
								require.Empty(t, tree.SelectedRef())
							}
							// Exercise representative renders; ranking and cursor bounds
							// above cover the full matrix without repeated frame styling.
							if cursor == 12 && query == "api" && alpha {
								selected := tree.SelectedRef()
								for _, width := range []int{40, 100} {
									tree.View(c, width, 15)
									require.Equal(t, selected, tree.SelectedRef())
								}
							}
						}
					}
				}
			}
		}
	}
}

func TestOverviewPreviewDoesNotSeedLiveHistoryOrNavigation(t *testing.T) {
	c := tuictx.New(theme.Default(), keys.Default(), nil, nil)
	tree := NewTree(c)
	tree.path = append(tree.path, NodeRef{Kind: k8s.KindContext, Name: "old"})
	tree.SetChildren(tree.path, &Children{Kind: k8s.KindNamespaceFolder, Apps: []k8s.AppInfo{{Name: "api", Namespace: "shop", CPU: 100, Health: k8s.HealthFailing}}})
	require.NotEmpty(t, tree.metricsHist)
	tree.ResetForContextSwitch()
	require.Empty(t, tree.metricsHist)
	require.Empty(t, tree.lastHealth)
	require.Empty(t, tree.nsHealthCache)
	tree.path[len(tree.path)-1] = NodeRef{Kind: k8s.KindContext, Name: "new"}
	tree.ShowOverviewPreview(&Children{Kind: k8s.KindNamespaceFolder, UserNS: []k8s.NamespaceInfo{{Name: "shop"}}, Apps: []k8s.AppInfo{{Name: "api", Namespace: "shop", Health: k8s.HealthHealthy}}})
	require.Empty(t, tree.metricsHist)
	require.Empty(t, tree.lastHealth)
	require.Empty(t, tree.healthPulse)
	require.Equal(t, k8s.HealthHealthy, tree.nsHealthCache["shop"].worst)
	tree.Push(NodeRef{Kind: k8s.KindNamespaceFolder, Name: "shop", Namespace: "shop"})
	tree.Pop()
	require.Nil(t, tree.Children())
}
