package sections

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yunus/safina/internal/k8s"
)

func TestDetailCachesBoundResourceChurn(t *testing.T) {
	i := NewInspector()
	c := NewConnected()
	for n := range maxDetailCacheEntries * 3 {
		name := fmt.Sprintf("pod-%d", n)
		i.Apply(InspectorDetailMsg{Kind: k8s.KindPod, Namespace: "shop", Name: name}, nil)
		c.Apply(ConnectedMsg{Kind: k8s.KindPod, Namespace: "shop", Name: name})
	}
	require.LessOrEqual(t, len(i.detail), maxDetailCacheEntries)
	require.LessOrEqual(t, len(c.cache), maxDetailCacheEntries)
}

func TestTreeCachesBoundNavigationAndPreserveAncestors(t *testing.T) {
	tr := newTestTree(t)
	tr.Push(NodeRef{Kind: k8s.KindNamespaceFolder, Name: "shop", Namespace: "shop"})
	parent := tr.Path()
	tr.SetChildren(parent, &Children{Kind: k8s.KindApp})
	for n := range maxNavigationPositions * 2 {
		tr.Push(NodeRef{Kind: k8s.KindApp, Name: fmt.Sprintf("workload-%d", n)})
		tr.SetChildren(tr.Path(), &Children{Kind: k8s.KindPod, Pods: []k8s.PodInfo{{Name: "pod"}}})
		tr.Pop()
	}
	require.LessOrEqual(t, len(tr.cache), maxTreeCacheEntries)
	require.LessOrEqual(t, len(tr.cursorByKey), maxNavigationPositions)
	require.NotNil(t, tr.cache[pathKeyFor(parent)], "the active ancestor must survive churn")
}

func TestTreeRetainedRowsAndHealthAreBounded(t *testing.T) {
	tr := newTestTree(t)
	for n := range 5 {
		path := append(tr.Path(), NodeRef{Kind: k8s.KindCustomFold, Name: fmt.Sprintf("kind-%d", n)})
		tr.cacheLevel(pathKeyFor(path), &Children{Kind: k8s.KindPod, Pods: make([]k8s.PodInfo, 20000)})
	}
	require.LessOrEqual(t, tr.cachedRowCount(), maxTreeCachedRows)
	for n := range maxHealthEntries * 2 {
		name := fmt.Sprintf("pod-%d", n)
		tr.recordHealthPulse(k8s.KindPod, "shop", name, k8s.HealthFailing)
		tr.recordHealthPulse(k8s.KindPod, "shop", name, k8s.HealthHealthy)
	}
	tr.pruneHealthRetention()
	require.LessOrEqual(t, len(tr.lastHealth), maxHealthEntries)
	require.LessOrEqual(t, len(tr.healthPulse), maxHealthEntries)
}
