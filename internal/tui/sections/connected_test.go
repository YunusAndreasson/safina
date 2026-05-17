package sections

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yunus/safina/internal/k8s"
)

func TestConnected_EmptyByDefault(t *testing.T) {
	c := NewConnected()
	require.Nil(t, c.snap, "Connected should start empty until SetSelection")
}

func TestConnected_ClearWipesSnapshotAndCache(t *testing.T) {
	c := NewConnected()
	c.snap = &connectedSnapshot{title: "stale"}
	c.cache["x"] = &k8s.Connections{}
	c.Clear()
	assert.Nil(t, c.snap)
	assert.Empty(t, c.cache, "Clear should drop the cache so cross-cluster data can't bleed")
}

func TestConnected_UnsupportedKindMarksNotApplicable(t *testing.T) {
	// A Node row has no useful connection graph in this v1 — Connected
	// should label the panel "no connections to show" rather than fire a
	// load.
	tr := newTestTree(t)
	tr.path = []NodeRef{{Kind: k8s.KindNode, Display: "Nodes"}}
	tr.children = &Children{
		Kind:  k8s.KindNode,
		Nodes: []k8s.NodeInfo{{Name: "node-1"}},
	}
	tr.cursor = 0

	c := NewConnected()
	cmd := c.SetSelection(context.Background(), nil, "", &tr)
	assert.Nil(t, cmd, "no fetch should fire for unsupported kinds")
	require.NotNil(t, c.snap)
	assert.True(t, c.snap.notApplicable, "Node should render the not-applicable hint")
}

func TestConnected_ApplyPopulatesGroupsForLiveSelection(t *testing.T) {
	tr := newTestTree(t)
	tr.path = []NodeRef{{Kind: k8s.KindApp, Display: "Apps"}}
	tr.children = &Children{
		Kind: k8s.KindApp,
		Apps: []k8s.AppInfo{{
			Kind: k8s.AppKindDeployment, Name: "checkout", Namespace: "shop",
		}},
	}
	tr.cursor = 0

	c := NewConnected()
	// No manager → no fetch, snap is the base frame.
	cmd := c.SetSelection(context.Background(), nil, "", &tr)
	assert.Nil(t, cmd)
	require.NotNil(t, c.snap)

	c.Apply(ConnectedMsg{
		Kind: k8s.KindApp, SubKind: k8s.AppKindDeployment,
		Namespace: "shop", Name: "checkout",
		Connections: k8s.Connections{
			Uses: []k8s.ConnectedRef{
				{Kind: k8s.KindService, Namespace: "shop", Name: "checkout-api", Reason: "selector"},
				{Kind: k8s.KindConfigMap, Namespace: "shop", Name: "checkout-config", Reason: "envFrom"},
			},
			UsedBy: []k8s.ConnectedRef{
				{Kind: k8s.KindHPA, Namespace: "shop", Name: "checkout-hpa", Reason: "scaleTargetRef"},
			},
		},
	})
	require.NotNil(t, c.snap)
	assert.Len(t, c.snap.uses, 2)
	assert.Len(t, c.snap.usedBy, 1)
}

func TestConnected_ApplyDropsStaleResults(t *testing.T) {
	tr := newTestTree(t)
	tr.path = []NodeRef{{Kind: k8s.KindApp, Display: "Apps"}}
	tr.children = &Children{
		Kind: k8s.KindApp,
		Apps: []k8s.AppInfo{{
			Kind: k8s.AppKindDeployment, Name: "checkout", Namespace: "shop",
		}},
	}
	tr.cursor = 0

	c := NewConnected()
	c.SetSelection(context.Background(), nil, "", &tr)

	// Apply a result for a different selection — should land in the cache
	// but not disturb the live snapshot's uses/usedBy.
	c.Apply(ConnectedMsg{
		Kind: k8s.KindApp, SubKind: k8s.AppKindDeployment,
		Namespace: "other", Name: "stale-thing",
		Connections: k8s.Connections{
			Uses: []k8s.ConnectedRef{
				{Kind: k8s.KindService, Namespace: "other", Name: "stale-svc"},
			},
		},
	})
	assert.Empty(t, c.snap.uses, "stale result must not alter the live render")
	assert.NotEmpty(t, c.cache, "stale result still caches for the future")
}
