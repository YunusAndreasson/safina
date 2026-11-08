package sections

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"sort"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/YunusAndreasson/safina/internal/k8s"
	"github.com/YunusAndreasson/safina/internal/tui/keys"
	"github.com/YunusAndreasson/safina/internal/tui/theme"
	"github.com/YunusAndreasson/safina/internal/tui/tuictx"
	"github.com/sahilm/fuzzy"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/types"
)

// Preserve the pre-optimization algorithm as an independent oracle: copy/sort
// full values, materialize names, then call fuzzy.Find and copy the matches.
func legacyFilteredApps(rows []k8s.AppInfo, query string, alpha bool) []k8s.AppInfo {
	if alpha {
		rows = append([]k8s.AppInfo(nil), rows...)
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].Kind != rows[j].Kind {
				return rows[i].Kind < rows[j].Kind
			}
			return rows[i].Name < rows[j].Name
		})
	}
	if query == "" {
		return rows
	}
	names := make([]string, len(rows))
	for i := range rows {
		names[i] = rows[i].Name
	}
	out := make([]k8s.AppInfo, 0)
	for _, match := range fuzzy.Find(query, names) {
		out = append(out, rows[match.Index])
	}
	return out
}

func legacyFilteredPods(rows []k8s.PodInfo, query string, alpha bool) []k8s.PodInfo {
	if !alpha {
		rows = append([]k8s.PodInfo(nil), rows...)
		sort.Slice(rows, func(i, j int) bool {
			hi, hj := k8s.PodHealth(rows[i]), k8s.PodHealth(rows[j])
			if hi != hj {
				return hi < hj
			}
			return rows[i].Name < rows[j].Name
		})
	}
	if query == "" {
		return rows
	}
	names := make([]string, len(rows))
	for i := range rows {
		names[i] = rows[i].Name
	}
	out := make([]k8s.PodInfo, 0)
	for _, match := range fuzzy.Find(query, names) {
		out = append(out, rows[match.Index])
	}
	return out
}

func TestTreeFilteredRowsPreserveRankingAndSelection(t *testing.T) {
	c := tuictx.New(theme.Default(), keys.Default(), nil, nil)
	names := []string{"api", "api-worker", "API", "a-p-i", "apiServer", "café-api", "سفينة", "db", "api"}
	kinds := []string{"Deployment", "StatefulSet", "Job"}
	statuses := []string{"Running", "Pending", "CrashLoopBackOff", "Succeeded", "Terminating", "Unknown"}
	for _, count := range []int{0, 1, 257} {
		apps := make([]k8s.AppInfo, count)
		pods := make([]k8s.PodInfo, count)
		for i := range apps {
			name, ns := names[i%len(names)], fmt.Sprintf("ns-%03d", i)
			apps[i] = k8s.AppInfo{Name: name, Namespace: ns, Kind: kinds[i%len(kinds)], UID: types.UID(ns), CPU: int64(i), HPA: &k8s.HPABadge{Name: ns}}
			pods[i] = k8s.PodInfo{Name: name, Namespace: ns, Status: statuses[i%len(statuses)], Restarts: int32(i % 15), CPU: int64(i)}
		}
		rng := rand.New(rand.NewPCG(1, 2)) //nolint:gosec // Fixed seed makes the test permutation reproducible; no security use.
		rng.Shuffle(len(apps), func(i, j int) { apps[i], apps[j] = apps[j], apps[i] })
		rng.Shuffle(len(pods), func(i, j int) { pods[i], pods[j] = pods[j], pods[i] })
		originalApps, originalPods := slices.Clone(apps), slices.Clone(pods)
		for _, alpha := range []bool{false, true} {
			for _, query := range []string{"", "api", "ap", "API", "café", "سفينة", "missing"} {
				t.Run(fmt.Sprintf("count=%d/alpha=%t/query=%s", count, alpha, query), func(t *testing.T) {
					tree := NewTree(c)
					tree.alphaSort = alpha
					tree.path = append(tree.path, NodeRef{Kind: k8s.KindWorkloadsFold})
					tree.children = &Children{Kind: k8s.KindApp, Apps: apps, Pods: pods}
					tree.filter.Open()
					tree.filter.Update(tea.PasteMsg{Content: query})
					tree.filter.Close()
					require.Equal(t, query, tree.filter.Value())
					wantApps := legacyFilteredApps(apps, query, alpha)
					wantPods := legacyFilteredPods(pods, query, alpha)
					require.Equal(t, wantApps, tree.computeFilteredApps())
					require.Equal(t, wantPods, tree.computeFilteredPods())
					require.Equal(t, originalApps, apps, "input snapshot must remain unchanged")
					require.Equal(t, originalPods, pods)
					for _, index := range []int{0, len(wantApps) / 2, len(wantApps) - 1} {
						if index < 0 || index >= len(wantApps) {
							continue
						}
						tree.cursor = index
						got := tree.SelectedRef()
						require.Equal(t, wantApps[index].Name, got.Name)
						require.Equal(t, wantApps[index].Namespace, got.Namespace)
					}
					tree.children.Kind = k8s.KindPod
					for _, index := range []int{0, len(wantPods) / 2, len(wantPods) - 1} {
						if index < 0 || index >= len(wantPods) {
							continue
						}
						tree.cursor = index
						got := tree.SelectedRef()
						require.Equal(t, wantPods[index].Name, got.Name)
						require.Equal(t, wantPods[index].Namespace, got.Namespace)
					}
					for _, kind := range []string{k8s.KindApp, k8s.KindPod} {
						tree.children.Kind = kind
						for _, width := range []int{40, 100} {
							tree.rowSnapshot = &treeRowSnapshot{children: tree.children, kind: kind, pathKey: tree.pathKey(), query: query, alphaSort: alpha, appsReady: true, podsReady: true, apps: wantApps, pods: wantPods}
							wantFrame := tree.renderBody(c, width, 8)
							tree.rowSnapshot = nil
							require.Equal(t, wantFrame, tree.renderBody(c, width, 8), "row rendering must be byte-identical")
							tree.rowSnapshot = nil
						}
					}
				})
			}
		}
	}
}

func TestCancellingFilterRestoresOriginalResourceSelection(t *testing.T) {
	c := newEventsCtx()
	tree := NewTree(c)
	tree.Focused = true
	tree.path = append(tree.path, NodeRef{Kind: k8s.KindWorkloadsFold, Name: "Workloads"})
	tree.SetChildren(tree.path, &Children{Kind: k8s.KindApp, Apps: []k8s.AppInfo{{Kind: "Deployment", Name: "alpha"}, {Kind: "Deployment", Name: "beta"}, {Kind: "Deployment", Name: "gamma"}}})
	tree.cursor = 2
	original := tree.SelectedRef()
	tree.Update(tea.KeyPressMsg{Code: '/', Text: "/"}, c)
	tree.Update(tea.KeyPressMsg{Code: 'a', Text: "a"}, c)
	tree.Update(tea.KeyPressMsg{Code: tea.KeyEscape}, c)
	require.Equal(t, original, tree.SelectedRef(), "Esc cancels the query edit, not the original selection")
}

func TestClearingCommittedFilterKeepsTheSelectedResource(t *testing.T) {
	c := newEventsCtx()
	tree := NewTree(c)
	tree.Focused = true
	tree.path = append(tree.path, NodeRef{Kind: k8s.KindWorkloadsFold, Name: "Workloads"})
	tree.SetChildren(tree.path, &Children{Kind: k8s.KindApp, Apps: []k8s.AppInfo{{Kind: "Deployment", Name: "alpha"}, {Kind: "Deployment", Name: "beta"}, {Kind: "Deployment", Name: "gamma"}}})
	tree.Update(tea.KeyPressMsg{Code: '/', Text: "/"}, c)
	tree.Update(tea.PasteMsg{Content: "gamma"}, c)
	tree.Update(tea.KeyPressMsg{Code: tea.KeyEnter}, c)
	require.Equal(t, "gamma", tree.SelectedRef().Name)
	tree.Update(tea.KeyPressMsg{Code: tea.KeyEscape}, c)
	require.Empty(t, tree.FilterValue())
	require.Equal(t, "gamma", tree.SelectedRef().Name, "clearing a filter must not jump to row zero")
}
