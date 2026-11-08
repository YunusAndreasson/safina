package sections

import (
	"context"
	"fmt"
	"testing"

	"github.com/charmbracelet/x/ansi"

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

func TestConnectedKeyboardCanReachEveryLinkInSmallPanels(t *testing.T) {
	c := NewConnected()
	c.Focused = true
	c.snap = &connectedSnapshot{}
	for n := range 8 {
		c.snap.uses = append(c.snap.uses, k8s.ConnectedRef{Kind: k8s.KindSecret, Namespace: "ns", Name: fmt.Sprintf("secret-%d", n)})
		c.snap.usedBy = append(c.snap.usedBy, k8s.ConnectedRef{Kind: k8s.KindService, Namespace: "ns", Name: fmt.Sprintf("service-%d", n)})
	}
	ctx := newEventsCtx()
	for _, height := range []int{3, 4, 5, 7} {
		c.cursor = 0
		for n := range c.Count() {
			body := ansi.Strip(c.renderBody(ctx, 40, height-2))
			selected, ok := c.Selected()
			require.True(t, ok)
			require.Contains(t, body, selected.Name, "cursor %d height %d", n, height)
			require.Contains(t, body, "›")
			hit := false
			for row := range height - 2 {
				ref, ok := c.Hit(row)
				hit = hit || (ok && ref == selected)
			}
			require.True(t, hit, "selected link must also be clickable")
			c.MoveCursor(1)
		}
	}
}

func TestConnectedRefreshPreservesSelectedResource(t *testing.T) {
	c := NewConnected()
	c.ref = NodeRef{Kind: k8s.KindService, Namespace: "shop", Name: "service"}
	c.snap = &connectedSnapshot{uses: []k8s.ConnectedRef{{Kind: k8s.KindSecret, Name: "target", Namespace: "shop"}}}
	c.Apply(ConnectedMsg{Kind: k8s.KindService, Namespace: "shop", Name: "service", Connections: k8s.Connections{Uses: []k8s.ConnectedRef{{Kind: k8s.KindConfigMap, Name: "new", Namespace: "shop"}, {Kind: k8s.KindSecret, Name: "target", Namespace: "shop"}}}})
	selected, ok := c.Selected()
	require.True(t, ok)
	require.Equal(t, "target", selected.Name)
}
