package sections

import (
	"context"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/YunusAndreasson/safina/internal/k8s"
	"github.com/stretchr/testify/require"
)

func TestSelectionFetchCancelsReplacedQueuedWorkAndReleasesCompletedContext(t *testing.T) {
	parent, stop := context.WithCancel(t.Context())
	defer stop()
	var cancel context.CancelFunc
	contexts := make([]context.Context, 0, 2)
	old := delayedSelectionFetch(parent, &cancel, 0, func(ctx context.Context) tea.Cmd {
		contexts = append(contexts, ctx)
		return selectionContextError(ctx)
	})
	current := delayedSelectionFetch(parent, &cancel, 0, func(ctx context.Context) tea.Cmd {
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

// Moving on before the delay ends sends nothing for the row left behind; the
// row the cursor rests on is fetched once the delay has passed.
func TestSelectionFetchWaitsForTheCursorToRest(t *testing.T) {
	var cancel context.CancelFunc
	sent := 0
	fetch := func(context.Context) tea.Cmd {
		return func() tea.Msg { sent++; return "detail" }
	}
	left := delayedSelectionFetch(t.Context(), &cancel, time.Hour, fetch)
	rested := delayedSelectionFetch(t.Context(), &cancel, 10*time.Millisecond, fetch)
	require.Nil(t, left())
	require.Zero(t, sent)
	start := time.Now()
	require.Equal(t, "detail", rested())
	require.Equal(t, 1, sent)
	require.GreaterOrEqual(t, time.Since(start), 10*time.Millisecond)
	cancelSelectionFetch(&cancel)
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
