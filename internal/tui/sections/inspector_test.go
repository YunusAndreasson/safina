package sections

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yunus/safina/internal/k8s"
	"github.com/yunus/safina/internal/tui/keys"
	"github.com/yunus/safina/internal/tui/theme"
	"github.com/yunus/safina/internal/tui/tuictx"
)

// newTestTree returns a NewTree-compatible Tree wired to a minimal Ctx —
// enough for filter / theme access without spinning up a k8s.Manager.
func newTestTree(t *testing.T) Tree {
	t.Helper()
	c := tuictx.New(theme.Default(), keys.Default(), nil, nil)
	return NewTree(c)
}

// collectRowValues flattens snapshot sections into a label→value map.
// Multiple rows may share a label (e.g. one row per container under
// "containers") — the map only keeps the first; tests that care about
// duplicates use sliceContains on snap.sections directly.
func collectRowValues(s *inspectorSnapshot) map[string]string {
	out := map[string]string{}
	for _, sec := range s.sections {
		for _, r := range sec.rows {
			if _, ok := out[r.label]; !ok {
				out[r.label] = r.value
			}
		}
	}
	return out
}

func sectionTitles(s *inspectorSnapshot) []string {
	out := []string{}
	for _, sec := range s.sections {
		if sec.title != "" {
			out = append(out, sec.title)
		}
	}
	return out
}

func TestInspector_EmptyByDefault(t *testing.T) {
	i := NewInspector()
	require.Nil(t, i.snap, "Inspector should start empty until SetSelection is called")
}

func TestInspector_ClearWipesSnapshotAndCache(t *testing.T) {
	i := NewInspector()
	i.snap = &inspectorSnapshot{title: "stale"}
	i.detail["x"] = &inspectorDetail{}
	i.Clear()
	assert.Nil(t, i.snap)
	assert.Empty(t, i.detail, "Clear should drop the detail cache so cross-cluster data can't bleed")
}

func TestInspector_PodBaseSnapshotSkipsRowDuplicates(t *testing.T) {
	tr := newTestTree(t)
	tr.path = []NodeRef{{Kind: k8s.KindPod, Display: "Pods"}}
	tr.children = &Children{
		Kind: k8s.KindPod,
		Pods: []k8s.PodInfo{{
			Name: "checkout-0", Namespace: "shop",
			Status: "Running", NodeName: "gke-1", Age: "5d", Restarts: 0,
		}},
	}
	tr.cursor = 0

	ins := NewInspector()
	// No manager / no context → no detail fetch; base snapshot only.
	cmd := ins.SetSelection(context.Background(), nil, "", &tr)
	assert.Nil(t, cmd, "no fetch should run without a manager + context")
	require.NotNil(t, ins.snap)
	assert.Contains(t, ins.snap.title, "checkout-0")
	assert.Contains(t, ins.snap.subtitle, "gke-1",
		"node name belongs in the subtitle (it's not always in the row column)")

	// The pre-detail snapshot must NOT carry namespace/age/restarts/cpu/mem —
	// those are already in the row columns. The user explicitly called out
	// the duplication and this test is what stops a regression.
	values := collectRowValues(ins.snap)
	for _, dup := range []string{"namespace", "age", "restarts", "cpu", "mem", "status"} {
		_, has := values[dup]
		assert.False(t, has, "inspector pre-detail snapshot should not duplicate row column %q", dup)
	}
}

func TestInspector_PodApplyDetailAddsImagesAndIP(t *testing.T) {
	tr := newTestTree(t)
	tr.path = []NodeRef{{Kind: k8s.KindPod, Display: "Pods"}}
	tr.children = &Children{
		Kind: k8s.KindPod,
		Pods: []k8s.PodInfo{{
			Name: "checkout-0", Namespace: "shop",
			Status: "Running", NodeName: "gke-1", Age: "5d",
		}},
	}
	tr.cursor = 0

	ins := NewInspector()
	ins.SetSelection(context.Background(), nil, "", &tr)
	ins.Apply(InspectorDetailMsg{
		Kind: k8s.KindPod, Namespace: "shop", Name: "checkout-0",
		Pod: &k8s.PodDetail{
			PodIP: "10.4.0.12", HostIP: "10.0.0.5", QoS: "Burstable",
			OwnerKind: "ReplicaSet", OwnerName: "checkout-7d9f",
			CPURequestMilli: 100, CPULimitMilli: 500,
			MemRequestBytes: 64 << 20, MemLimitBytes: 256 << 20,
			Containers: []k8s.ContainerSnap{{
				Name: "main", Image: "ghcr.io/co/checkout:v1.2.3",
				Ready: true, State: "Running",
			}},
		},
	}, &tr)

	require.NotNil(t, ins.snap)
	values := collectRowValues(ins.snap)
	assert.Equal(t, "10.4.0.12", values["pod IP"], "pod IP belongs in the runtime section")
	assert.Equal(t, "Burstable", values["QoS"])
	assert.Equal(t, "ReplicaSet/checkout-7d9f", values["owner"])
	// Containers section shortens the image to the trailing segment + tag.
	assert.Contains(t, values["main"], "checkout:v1.2.3",
		"container row should show the short image:tag")
	// Sections must include "runtime", "resources (configured)", and "containers".
	titles := sectionTitles(ins.snap)
	for _, want := range []string{"runtime", "resources (configured)", "containers"} {
		assert.Contains(t, titles, want)
	}
}

func TestInspector_PodApplyStaleDetailIsCachedNotApplied(t *testing.T) {
	tr := newTestTree(t)
	tr.path = []NodeRef{{Kind: k8s.KindPod, Display: "Pods"}}
	tr.children = &Children{
		Kind: k8s.KindPod,
		Pods: []k8s.PodInfo{
			{Name: "p1", Namespace: "ns", Status: "Running"},
			{Name: "p2", Namespace: "ns", Status: "Running"},
		},
	}
	tr.cursor = 0

	ins := NewInspector()
	ins.SetSelection(context.Background(), nil, "", &tr)
	// User moves to p2 before p1's fetch returns.
	tr.cursor = 1
	ins.SetSelection(context.Background(), nil, "", &tr)
	// Now p1's stale detail arrives.
	ins.Apply(InspectorDetailMsg{
		Kind: k8s.KindPod, Namespace: "ns", Name: "p1",
		Pod: &k8s.PodDetail{PodIP: "10.0.0.1"},
	}, &tr)

	// Snapshot is for p2 still (no IP row from p1).
	require.NotNil(t, ins.snap)
	assert.Contains(t, ins.snap.title, "p2")
	values := collectRowValues(ins.snap)
	_, hasIP := values["pod IP"]
	assert.False(t, hasIP, "stale detail must not bleed into the current snapshot")
	// But the result IS cached so a return visit is free.
	assert.NotNil(t, ins.detail[inspectorDetailKey(k8s.KindPod, "ns", "p1")],
		"stale detail should be cached for future hits")
}

func TestInspector_NodeCordonedHeadline(t *testing.T) {
	tr := newTestTree(t)
	tr.path = []NodeRef{{Kind: k8s.KindNode, Display: "Nodes"}}
	tr.children = &Children{
		Kind: k8s.KindNode,
		Nodes: []k8s.NodeInfo{{
			Name: "n1", Pool: "default", Status: "Ready",
			Schedulable: false,
		}},
	}
	tr.cursor = 0

	ins := NewInspector()
	ins.SetSelection(context.Background(), nil, "", &tr)
	require.NotNil(t, ins.snap)
	assert.Contains(t, ins.snap.statusText, "cordoned",
		"cordoned node should be called out in the headline")
}

func TestInspector_ContainerSnapPreservesState(t *testing.T) {
	// The container inspector deliberately omits an image row — the
	// tree row's NAME·IMAGE column carries it. What it must surface is
	// the container's state headline plus the kind glyph (regular /
	// init / ephemeral).
	tr := newTestTree(t)
	tr.path = []NodeRef{{Kind: k8s.KindContainer, Display: "Containers"}}
	tr.children = &Children{
		Kind: k8s.KindContainer,
		Containers: []k8s.ContainerInfo{{
			Name: "main", PodName: "p1", Namespace: "ns",
			Image: "ghcr.io/co/checkout:v1.2.3", Kind: "regular",
			State: "Running", Ready: true,
		}},
	}
	tr.cursor = 0

	ins := NewInspector()
	ins.SetSelection(context.Background(), nil, "", &tr)
	require.NotNil(t, ins.snap)
	values := collectRowValues(ins.snap)
	_, hasImage := values["image"]
	assert.False(t, hasImage, "container inspector must not duplicate the row's image column")
	assert.Equal(t, "regular", values["kind"])
	assert.Equal(t, "Running", ins.snap.statusText)
}

func TestShortenImage(t *testing.T) {
	cases := map[string]string{
		"ghcr.io/co/checkout:v1.2.3":           "checkout:v1.2.3",
		"checkout:v1.2.3":                      "checkout:v1.2.3",
		"checkout":                             "checkout",
		"registry.k8s.io/pause:3.9":            "pause:3.9",
		"gcr.io/x/y/z@sha256:abc123":           "z",
		"gcr.io/x/y/z:tag@sha256:abc123":       "z:tag",
		"":                                     "",
		"a/b/c/d/e/f/g/longimagename:verylong": "longimagename:verylong",
	}
	for in, want := range cases {
		got := shortenImage(in)
		assert.Equal(t, want, got, "shortenImage(%q)", in)
	}
}

func TestFormatReqLim(t *testing.T) {
	// 100 millicores req, 500 milli lim
	assert.Equal(t, "100m req · 500m lim", formatReqLim(100, 500, "cpu"))
	// Equal req/lim collapses to the "req=lim" form.
	assert.Contains(t, formatReqLim(250, 250, "cpu"), "req=lim")
	// Only one side configured.
	assert.Equal(t, "250m req · — lim", formatReqLim(250, 0, "cpu"))
	assert.Equal(t, "— req · 500m lim", formatReqLim(0, 500, "cpu"))
	// Nothing configured.
	assert.Equal(t, "—", formatReqLim(0, 0, "cpu"))
}
