package sections

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yunus/safina/internal/k8s"
)

func TestActivity_StartsEmpty(t *testing.T) {
	a := NewActivity()
	_, _, _, ok := a.Target()
	assert.False(t, ok, "fresh Activity has no target")
	assert.False(t, a.NeedsRefresh(time.Now()))
}

func TestActivity_SetSelectionSchedulesLoad(t *testing.T) {
	a := NewActivity()
	cmd := a.SetSelection(context.Background(), nil, "ctx", "Pod", "p1", "ns")
	require.NotNil(t, cmd, "first selection should return a load Cmd")
	k, n, ns, ok := a.Target()
	assert.True(t, ok)
	assert.Equal(t, "Pod", k)
	assert.Equal(t, "p1", n)
	assert.Equal(t, "ns", ns)
}

func TestActivity_SetSelectionSameTargetSkipsLoad(t *testing.T) {
	a := NewActivity()
	a.SetSelection(context.Background(), nil, "ctx", "Pod", "p1", "ns")
	cmd := a.SetSelection(context.Background(), nil, "ctx", "Pod", "p1", "ns")
	assert.Nil(t, cmd, "re-pointing at the same resource should not refetch")
}

func TestActivity_SetSelectionEmptyKindClears(t *testing.T) {
	a := NewActivity()
	a.SetSelection(context.Background(), nil, "ctx", "Pod", "p1", "ns")
	require.NotEmpty(t, a.kind)
	cmd := a.SetSelection(context.Background(), nil, "ctx", "", "", "")
	assert.Nil(t, cmd)
	_, _, _, ok := a.Target()
	assert.False(t, ok, "empty selection should clear the tile")
}

func TestActivity_ApplyStaleResultDropped(t *testing.T) {
	a := NewActivity()
	a.SetSelection(context.Background(), nil, "ctx", "Pod", "p1", "ns")
	// While that was in flight, the user moved to a different pod.
	a.SetSelection(context.Background(), nil, "ctx", "Pod", "p2", "ns")
	// The original fetch now returns. Apply should ignore it.
	a.Apply(ActivityLoadedMsg{
		Kind: "Pod", Namespace: "ns", Name: "p1",
		Events: []k8s.EventInfo{{Reason: "Pulled", Message: "image pulled"}},
	})
	assert.Empty(t, a.items, "stale result should not overwrite current selection")
}

func TestActivity_ApplyCurrentResultLands(t *testing.T) {
	a := NewActivity()
	a.SetSelection(context.Background(), nil, "ctx", "Pod", "p1", "ns")
	a.Apply(ActivityLoadedMsg{
		Kind: "Pod", Namespace: "ns", Name: "p1",
		Events: []k8s.EventInfo{{Reason: "Pulled", Message: "image pulled"}},
	})
	assert.Len(t, a.items, 1)
	assert.False(t, a.loading)
	assert.False(t, a.lastFetch.IsZero())
	assert.False(t, a.NeedsRefresh(a.lastFetch.Add(time.Second)),
		"fresh result should defeat NeedsRefresh until throttle elapses")
	assert.True(t, a.NeedsRefresh(a.lastFetch.Add(activityRefreshInterval+time.Second)),
		"after throttle elapses, NeedsRefresh should report true")
}

func TestActivity_ClearWipesState(t *testing.T) {
	a := NewActivity()
	a.SetSelection(context.Background(), nil, "ctx", "Pod", "p1", "ns")
	a.Apply(ActivityLoadedMsg{
		Kind: "Pod", Namespace: "ns", Name: "p1",
		Events: []k8s.EventInfo{{Reason: "Pulled"}},
	})
	a.Clear()
	assert.Empty(t, a.items)
	assert.Empty(t, a.kind)
	assert.True(t, a.lastFetch.IsZero())
}
