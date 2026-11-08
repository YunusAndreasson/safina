package sections

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/YunusAndreasson/safina/internal/k8s"
)

func newChatTree(t *testing.T) *Tree {
	t.Helper()
	tr := newTestTree(t)
	return &tr
}

func TestBuildContextSnapshot_NoSelectionLeavesEmptyBlock(t *testing.T) {
	// Before the user has drilled in, only [Selection]\n header is built —
	// the function trims that to "" so the chat panel doesn't prepend an
	// empty stub. Confirm via Block being empty.
	tr := newChatTree(t)
	snap := BuildContextSnapshot(tr, "")
	assert.Empty(t, snap.Block, "no selection + no context → no block")
	assert.Empty(t, snap.Crumbs)
}

func TestBuildContextSnapshot_SkipsContextListInCrumbs(t *testing.T) {
	// "Clusters" is visual chrome — drop it. The strip starts at the
	// cluster the user is in, not the picker root.
	tr := newChatTree(t)
	tr.path = []NodeRef{
		{Kind: k8s.KindContextList, Display: "Clusters"},
		{Kind: k8s.KindContext, Name: "prod-eu-1", Display: "prod-eu-1"},
	}
	snap := BuildContextSnapshot(tr, "prod-eu-1")
	require.NotEmpty(t, snap.Crumbs)
	for _, c := range snap.Crumbs {
		assert.NotEqual(t, "Clusters", c.Label, "ContextList root must be suppressed")
	}
	assert.Contains(t, snap.Block, "cluster: prod-eu-1")
}

func TestBuildContextSnapshot_PodSelectionSerializesAllNonZeroFields(t *testing.T) {
	tr := newChatTree(t)
	tr.path = []NodeRef{
		{Kind: k8s.KindContext, Name: "prod", Display: "prod"},
		{Kind: k8s.KindNamespace, Name: "shop", Display: "shop"},
		{Kind: k8s.KindPod, Display: "Pods"},
	}
	tr.children = &Children{
		Kind: k8s.KindPod,
		Pods: []k8s.PodInfo{
			{
				Name: "checkout-0", Namespace: "shop", Status: "Running",
				Restarts: 2, Age: "5d", NodeName: "gke-1",
				CPU: 234, Memory: 64 << 20,
			},
		},
	}
	tr.cursor = 0

	snap := BuildContextSnapshot(tr, "prod")
	require.NotEmpty(t, snap.Block)
	assert.Contains(t, snap.Block, "cluster: prod")
	assert.Contains(t, snap.Block, "namespace: shop")
	assert.Contains(t, snap.Block, "pod/checkout-0:")
	assert.Contains(t, snap.Block, "status: Running")
	assert.Contains(t, snap.Block, "restarts: 2")
	assert.Contains(t, snap.Block, "age: 5d")
	assert.Contains(t, snap.Block, "node: gke-1")
	assert.Contains(t, snap.Block, "cpu:")
	assert.Contains(t, snap.Block, "mem:")
}

func TestBuildContextSnapshot_PodOmitsZeroFields(t *testing.T) {
	// "restarts: 0" is fine to include (always shown), but absent CPU /
	// Memory shouldn't render as "cpu: —  mem: —" lines — Claude wastes
	// tokens reading them. Confirm zero CPU+Mem suppresses the line.
	tr := newChatTree(t)
	tr.path = []NodeRef{{Kind: k8s.KindPod, Display: "Pods"}}
	tr.children = &Children{
		Kind: k8s.KindPod,
		Pods: []k8s.PodInfo{{Name: "p1", Namespace: "ns", Status: "Running"}},
	}
	tr.cursor = 0
	snap := BuildContextSnapshot(tr, "")
	assert.NotContains(t, snap.Block, "cpu:")
	assert.NotContains(t, snap.Block, "node:", "node line must be absent when NodeName is empty")
	assert.NotContains(t, snap.Block, "age:")
}

func TestBuildContextSnapshot_AppEnclosingPodAppearsInBlock(t *testing.T) {
	// Pod under a Deployment → block must include both the app line AND
	// the pod block, so Claude can reason about the workload, not just
	// the lonely pod.
	tr := newChatTree(t)
	tr.path = []NodeRef{
		{Kind: k8s.KindNamespace, Name: "shop", Display: "shop"},
		{Kind: k8s.KindApp, SubKind: k8s.AppKindDeployment, Namespace: "shop", Name: "checkout", Display: "checkout"},
		{Kind: k8s.KindPod, Display: "Pods"},
	}
	tr.children = &Children{
		Kind: k8s.KindPod,
		Pods: []k8s.PodInfo{{Name: "checkout-0", Namespace: "shop", Status: "Running"}},
	}
	tr.cursor = 0
	snap := BuildContextSnapshot(tr, "prod")
	// The path-enclosing line must name the CONCRETE workload kind
	// (deployment / statefulset / …), not the generic "app:" tag — that's
	// the disambiguation that lets Claude give kind-specific advice.
	assert.Contains(t, snap.Block, "deployment: checkout",
		"path-enclosing line must use the SubKind, not generic 'app:'")
	assert.NotContains(t, snap.Block, "app: checkout",
		"regression: generic 'app:' label leaks the internal KindApp tag to Claude")
	assert.Contains(t, snap.Block, "pod/checkout-0:")
}

func TestBuildContextSnapshot_NodeNotANamespaceResource(t *testing.T) {
	// Nodes are cluster-scoped; the block must NOT include a stray
	// namespace line — that would mislead Claude about the resource scope.
	tr := newChatTree(t)
	tr.path = []NodeRef{
		{Kind: k8s.KindContext, Name: "prod", Display: "prod"},
		{Kind: k8s.KindNode, Display: "Nodes"},
	}
	tr.children = &Children{
		Kind: k8s.KindNode,
		Nodes: []k8s.NodeInfo{{
			Name: "gke-default-1", Pool: "default", Status: "Ready",
			Roles: "worker", Schedulable: true,
			PodsCurrent: 14, PodsMax: 110,
			CPUAlloc: 4000, CPUUsed: 800, MemAlloc: 16 << 30, MemUsed: 8 << 30,
		}},
	}
	tr.cursor = 0
	snap := BuildContextSnapshot(tr, "prod")
	assert.NotContains(t, snap.Block, "namespace:", "Node block must not contain namespace")
	assert.Contains(t, snap.Block, "node/gke-default-1:")
	assert.Contains(t, snap.Block, "pool: default")
	assert.Contains(t, snap.Block, "status: Ready")
	assert.Contains(t, snap.Block, "schedulable: true")
	assert.Contains(t, snap.Block, "pods: 14/110")
}

func TestBuildContextSnapshot_NamespaceSelectionWritesNamespaceLine(t *testing.T) {
	tr := newChatTree(t)
	tr.path = []NodeRef{
		{Kind: k8s.KindContext, Name: "prod", Display: "prod"},
		{Kind: k8s.KindNamespace, Display: "Namespaces"},
	}
	tr.children = &Children{
		Kind:       k8s.KindNamespace,
		Namespaces: []k8s.NamespaceInfo{{Name: "shop"}, {Name: "auth"}},
	}
	tr.cursor = 0
	snap := BuildContextSnapshot(tr, "prod")
	assert.Contains(t, snap.Block, "selected: namespace/shop",
		"namespace selection must explicitly tag the chosen namespace")
}

func TestBuildContextSnapshot_AppHealthSerialized(t *testing.T) {
	tr := newChatTree(t)
	tr.path = []NodeRef{
		{Kind: k8s.KindNamespace, Name: "shop", Display: "shop"},
		{Kind: k8s.KindApp, Display: "Apps"},
	}
	tr.children = &Children{
		Kind: k8s.KindApp,
		Apps: []k8s.AppInfo{{
			Kind: k8s.AppKindDeployment, Name: "checkout",
			Namespace: "shop",
			Health:    k8s.HealthDegraded,
			Ready:     "1/3",
			Age:       "4d",
		}},
	}
	tr.cursor = 0
	snap := BuildContextSnapshot(tr, "prod")
	assert.Contains(t, snap.Block, "deployment/checkout:")
	assert.Contains(t, snap.Block, "health: degraded")
	assert.Contains(t, snap.Block, "ready: 1/3")
	assert.Contains(t, snap.Block, "age: 4d")
}

func TestHealthLabel_AllVariants(t *testing.T) {
	// Words are sent verbatim to Claude — typos here propagate into
	// every chat response. Pin the spelling.
	cases := map[k8s.Health]string{
		k8s.HealthHealthy:    "healthy",
		k8s.HealthDegraded:   "degraded",
		k8s.HealthFailing:    "failing",
		k8s.HealthInProgress: "in_progress",
		k8s.Health(255):      "unknown",
	}
	for in, want := range cases {
		assert.Equal(t, want, healthLabel(in))
	}
}

func TestPathNamespace_FromSelectionDirect(t *testing.T) {
	// The selected ref's Namespace wins — that's the most specific scope.
	ns := pathNamespace(
		[]NodeRef{{Kind: k8s.KindNamespace, Name: "outer"}},
		NodeRef{Kind: k8s.KindPod, Namespace: "shop", Name: "p"},
	)
	assert.Equal(t, "shop", ns)
}

func TestPathNamespace_FromNamespaceFolderInPath(t *testing.T) {
	// Selection has no Namespace (e.g. a synthetic fold) — fall back to
	// the deepest namespace-bearing path segment.
	ns := pathNamespace(
		[]NodeRef{
			{Kind: k8s.KindContext, Name: "prod"},
			{Kind: k8s.KindNamespaceFolder, Name: "shop"},
			{Kind: k8s.KindApp, Display: "Apps"},
		},
		NodeRef{Kind: k8s.KindApp, Display: "Apps"},
	)
	assert.Equal(t, "shop", ns)
}

func TestPathNamespace_NodeLevelHasNone(t *testing.T) {
	// Cluster-scoped path → no namespace, no inferred wrong answer.
	ns := pathNamespace(
		[]NodeRef{
			{Kind: k8s.KindContext, Name: "prod"},
			{Kind: k8s.KindNode, Display: "Nodes"},
		},
		NodeRef{Kind: k8s.KindNode, Name: "gke-1"},
	)
	assert.Empty(t, ns, "Node row must not synthesize a namespace")
}

func TestBuildContextSnapshot_BlockStartsWithSelectionHeader(t *testing.T) {
	// The header isn't decorative — Claude's system prompt specifically
	// references "[Selection]" as the marker telling it what scope to
	// answer for. Pin its presence as the first line.
	tr := newChatTree(t)
	tr.path = []NodeRef{{Kind: k8s.KindContext, Name: "prod", Display: "prod"}}
	snap := BuildContextSnapshot(tr, "prod")
	require.NotEmpty(t, snap.Block)
	first := strings.SplitN(snap.Block, "\n", 2)[0]
	assert.Equal(t, "[Selection]", first, "block must lead with [Selection] marker")
}
