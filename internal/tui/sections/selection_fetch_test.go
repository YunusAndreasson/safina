package sections

import (
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
	"github.com/yunus/safina/internal/k8s"
)

func TestSelectionFetchCancelsReplacedQueuedWorkAndReleasesCompletedContext(t *testing.T) {
	parent, stop := context.WithCancel(t.Context())
	defer stop()
	var cancel context.CancelFunc
	contexts := make([]context.Context, 0, 2)
	old := selectionFetch(parent, &cancel, func(ctx context.Context) tea.Cmd {
		contexts = append(contexts, ctx)
		return selectionContextError(ctx)
	})
	current := selectionFetch(parent, &cancel, func(ctx context.Context) tea.Cmd {
		contexts = append(contexts, ctx)
		return selectionContextError(ctx)
	})
	require.ErrorIs(t, contexts[0].Err(), context.Canceled)
	err, ok := old().(error)
	require.True(t, ok)
	require.ErrorIs(t, err, context.Canceled)
	require.NoError(t, contexts[1].Err())
	require.Nil(t, current())
	require.ErrorIs(t, contexts[1].Err(), context.Canceled)
	cancelSelectionFetch(&cancel)
	require.Nil(t, cancel)
}

func TestClearingSelectionCancelsAmbientRequests(t *testing.T) {
	cancelled := 0
	i := NewInspector()
	i.cancelFetch = func() { cancelled++ }
	i.Clear()
	c := NewConnected()
	c.cancelFetch = func() { cancelled++ }
	c.Clear()
	a := NewActivity()
	a.cancelFetch = func() { cancelled++ }
	a.Clear()
	require.Equal(t, 3, cancelled)
}

func TestCanceledAmbientLoadsDoNotFillCaches(t *testing.T) {
	i := NewInspector()
	i.Apply(InspectorDetailMsg{Kind: k8s.KindPod, Name: "gone", Err: context.Canceled}, nil)
	c := NewConnected()
	c.Apply(ConnectedMsg{Kind: k8s.KindPod, Name: "gone", Err: context.Canceled})
	require.Empty(t, i.detail)
	require.Empty(t, c.cache)
}

func selectionContextError(ctx context.Context) tea.Cmd {
	return func() tea.Msg { return ctx.Err() }
}
