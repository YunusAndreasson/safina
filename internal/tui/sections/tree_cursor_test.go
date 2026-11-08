package sections

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yunus/safina/internal/k8s"
)

// TestTree_WorkloadsRefreshPreservesCursor reproduces the user-reported bug:
// the selected bar in the Workloads view jumped to the top on every refresh.
// Root cause: ListAppsInNamespaces fans out goroutines that append to a
// shared slice under a mutex, then sorts by (Health, Kind, Name) without
// Namespace as a final tiebreak. Two apps with the same Health+Kind+Name
// in different namespaces tie under sort.Slice (unstable) and may reorder
// on each refresh — moving the cursor off the user's selection.
//
// This test simulates one such reorder at the Tree layer and asserts the
// cursor follows the selected app's identity, not its index.
func TestTree_WorkloadsRefreshPreservesCursor(t *testing.T) {
	tr := newTestTree(t)

	// Drill: root → ns "foo" → WorkloadsFold. We bypass the real load by
	// pushing path entries and seeding children directly.
	tr.path = append(tr.path,
		NodeRef{Kind: k8s.KindNamespaceFolder, Name: "foo", Namespace: "foo"},
		NodeRef{Kind: k8s.KindWorkloadsFold, Name: "Workloads"},
	)

	first := &Children{
		Kind: k8s.KindApp,
		Apps: []k8s.AppInfo{
			{Kind: "Deployment", Name: "alpha", Namespace: "foo", Health: k8s.HealthHealthy},
			{Kind: "Deployment", Name: "bravo", Namespace: "foo", Health: k8s.HealthHealthy},
			{Kind: "Deployment", Name: "charlie", Namespace: "foo", Health: k8s.HealthHealthy},
			{Kind: "Deployment", Name: "delta", Namespace: "foo", Health: k8s.HealthHealthy},
		},
	}
	tr.SetChildren(tr.path, first)
	require.Equal(t, "charlie", tr.visibleRows()[2].Name, "test fixture invariant")
	tr.cursor = 2 // user navigated down to charlie

	// Refresh tick fires. Server returned the same four apps but in a
	// different order — this is what the fan-out + unstable-sort path
	// produces in real life when the user is in the All-Workloads view
	// with name collisions across namespaces.
	refresh := &Children{
		Kind: k8s.KindApp,
		Apps: []k8s.AppInfo{
			{Kind: "Deployment", Name: "charlie", Namespace: "foo", Health: k8s.HealthHealthy},
			{Kind: "Deployment", Name: "alpha", Namespace: "foo", Health: k8s.HealthHealthy},
			{Kind: "Deployment", Name: "delta", Namespace: "foo", Health: k8s.HealthHealthy},
			{Kind: "Deployment", Name: "bravo", Namespace: "foo", Health: k8s.HealthHealthy},
		},
	}
	tr.SetChildren(tr.path, refresh)

	got := tr.SelectedRef()
	assert.Equal(t, "charlie", got.Name,
		"cursor must track selection identity across reordering, not stay at the old index")
}

// TestTree_WorkloadsRefreshNoHealthReorder verifies the higher-level
// invariant: if the user's selected app is at index 2, and a refresh comes
// back with the same apps but with health flipped on a different row, the
// cursor and its visual position must both stay at index 2.
//
// Before the fix in k8s/tree.go (sort stripped Health from the key), a
// Degraded health on row 0 would float that row to the top and shift the
// remaining rows down by one — pushing charlie from index 2 to index 3 and
// landing the visual selection bar somewhere unexpected.
func TestTree_WorkloadsRefreshNoHealthReorder(t *testing.T) {
	tr := newTestTree(t)
	tr.path = append(tr.path,
		NodeRef{Kind: k8s.KindNamespaceFolder, Name: "foo", Namespace: "foo"},
		NodeRef{Kind: k8s.KindWorkloadsFold, Name: "Workloads"},
	)

	// Same names, same kinds — only health differs. Identity-based sort
	// must keep alphabetical order regardless of health.
	allHealthy := &Children{
		Kind: k8s.KindApp,
		Apps: []k8s.AppInfo{
			{Kind: "Deployment", Name: "alpha", Namespace: "foo", Health: k8s.HealthHealthy},
			{Kind: "Deployment", Name: "bravo", Namespace: "foo", Health: k8s.HealthHealthy},
			{Kind: "Deployment", Name: "charlie", Namespace: "foo", Health: k8s.HealthHealthy},
			{Kind: "Deployment", Name: "delta", Namespace: "foo", Health: k8s.HealthHealthy},
		},
	}
	tr.SetChildren(tr.path, allHealthy)
	tr.cursor = 2 // user is on charlie

	// Refresh: alpha's deployment is now Degraded. With the old
	// (Health, Kind, Name) sort this would float alpha to index 0 — but
	// the input order is already alpha-first, so what actually shifts is
	// the per-row badge, not the row order. The regression we're guarding
	// against is a future "let's add Health back to the sort key" change.
	healthFlipped := &Children{
		Kind: k8s.KindApp,
		Apps: []k8s.AppInfo{
			{Kind: "Deployment", Name: "alpha", Namespace: "foo", Health: k8s.HealthDegraded},
			{Kind: "Deployment", Name: "bravo", Namespace: "foo", Health: k8s.HealthHealthy},
			{Kind: "Deployment", Name: "charlie", Namespace: "foo", Health: k8s.HealthHealthy},
			{Kind: "Deployment", Name: "delta", Namespace: "foo", Health: k8s.HealthHealthy},
		},
	}
	tr.SetChildren(tr.path, healthFlipped)

	assert.Equal(t, 2, tr.cursor, "cursor stays at the same row index")
	assert.Equal(t, "charlie", tr.SelectedRef().Name, "still pointing at charlie")
}

func TestRefreshPreservesWorkloadKindWhenNamesCollide(t *testing.T) {
	tr := newTestTree(t)
	tr.path = append(tr.path, NodeRef{Kind: k8s.KindWorkloadsFold, Name: "Workloads"})
	deploy := k8s.AppInfo{Kind: k8s.AppKindDeployment, Namespace: "shop", Name: "api"}
	stateful := k8s.AppInfo{Kind: k8s.AppKindStatefulSet, Namespace: "shop", Name: "api"}
	tr.SetChildren(tr.path, &Children{Kind: k8s.KindApp, Apps: []k8s.AppInfo{deploy, stateful}})
	tr.cursor = 1
	tr.View(newEventsCtx(), 60, 20)
	tr.SetChildren(tr.path, &Children{Kind: k8s.KindApp, Apps: []k8s.AppInfo{stateful, deploy}})
	require.Equal(t, k8s.AppKindStatefulSet, tr.SelectedRef().SubKind)
}
