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

func newDrainCtx() *tuictx.Ctx {
	return tuictx.New(theme.Default(), keys.Default(), nil, nil)
}

// emptyManager builds a Manager with no registered contexts so we can
// exercise the "context not found" branches without a real kubeconfig.
func emptyManager() *k8s.Manager {
	return &k8s.Manager{Clients: map[string]*k8s.Client{}}
}

func TestDrain_ResetCarriesNodeAndCancel(t *testing.T) {
	d := NewDrain(newDrainCtx())
	called := false
	cancel := func() { called = true }
	d.Reset("node-1", cancel)
	assert.Equal(t, "node-1", d.node)
	assert.True(t, d.started)
	assert.False(t, d.finished)
	d.Cancel()
	assert.True(t, called, "Cancel must invoke the saved cancel func")
}

func TestDrain_CancelNoopWhenIdle(t *testing.T) {
	// Section opened but never armed (modal closed before drain started).
	// Cancel must not panic — chat panel wires it to a keyboard handler
	// that fires regardless of armed-state.
	d := NewDrain(newDrainCtx())
	require.NotPanics(t, func() { d.Cancel() })
}

func TestDrain_ApplyReturnsTrueOnDone(t *testing.T) {
	// Apply returns the Done flag so the root model knows to flip back to
	// the main view. A bug that swallowed the Done signal would leave the
	// modal stuck on a finished drain.
	d := NewDrain(newDrainCtx())
	d.Reset("n", func() {})
	assert.False(t, d.Apply(k8s.DrainEvent{Phase: "cordoning"}))
	assert.False(t, d.Apply(k8s.DrainEvent{Phase: "evicting", PodsRemaining: 3}))
	assert.True(t, d.Apply(k8s.DrainEvent{Phase: "done", Done: true}))
	assert.True(t, d.finished)
}

func TestDrain_ApplyRecordsLatestEvent(t *testing.T) {
	// Each Apply must overwrite latest so the View renders the freshest
	// progress, not a stale snapshot. Regression: keeping the first event
	// would lock the modal at "cordoning" forever.
	d := NewDrain(newDrainCtx())
	d.Reset("n", func() {})
	d.Apply(k8s.DrainEvent{Phase: "cordoning"})
	d.Apply(k8s.DrainEvent{Phase: "evicting", PodsRemaining: 5})
	d.Apply(k8s.DrainEvent{Phase: "evicting", PodsRemaining: 2, RemainingPods: []string{"a", "b"}})
	assert.Equal(t, "evicting", d.latest.Phase)
	assert.Equal(t, 2, d.latest.PodsRemaining)
	assert.Equal(t, []string{"a", "b"}, d.latest.RemainingPods)
}

func TestDrain_ResetClearsPreviousProgress(t *testing.T) {
	// Operator drains node-A, finishes, then drains node-B. Stale
	// "PodsRemaining: 5" from A must not show on B's freshly opened modal.
	d := NewDrain(newDrainCtx())
	d.Reset("node-A", func() {})
	d.Apply(k8s.DrainEvent{Phase: "evicting", PodsRemaining: 5, Msg: "old data"})
	d.Reset("node-B", func() {})
	assert.Equal(t, "node-B", d.node)
	assert.Equal(t, 0, d.latest.PodsRemaining, "Reset must clear stale progress")
	assert.Empty(t, d.latest.Msg)
	assert.False(t, d.finished, "Reset must arm a fresh modal")
}

func TestDrain_PaneTitleIncludesNode(t *testing.T) {
	d := NewDrain(newDrainCtx())
	d.Reset("gke-prod-1-default-pool-abc", func() {})
	got := d.PaneTitle()
	assert.Contains(t, got, "gke-prod-1-default-pool-abc",
		"PaneTitle must name the node so the workspace tab is unambiguous")
}

// ── NextDrainEvent ─────────────────────────────────────────────────────

func TestNextDrainEvent_ChannelClosedTreatedAsGracefulDone(t *testing.T) {
	// Closing the channel without a final Done event must still surface a
	// DrainProgressMsg with Done=true. Otherwise the model would keep
	// pumping a closed channel and hang.
	ch := make(chan k8s.DrainEvent)
	close(ch)
	cmd := NextDrainEvent(ch)
	msg := cmd()
	prog, ok := msg.(DrainProgressMsg)
	require.True(t, ok)
	assert.True(t, prog.Event.Done, "closed channel must surface Done=true")
}

func TestNextDrainEvent_ReadsOneAndForwards(t *testing.T) {
	ch := make(chan k8s.DrainEvent, 1)
	ch <- k8s.DrainEvent{Phase: "evicting", PodsRemaining: 2}
	cmd := NextDrainEvent(ch)
	msg := cmd()
	prog, ok := msg.(DrainProgressMsg)
	require.True(t, ok)
	assert.Equal(t, "evicting", prog.Event.Phase)
	assert.Equal(t, 2, prog.Event.PodsRemaining)
	assert.False(t, prog.Event.Done)
}

// ── StartDrain & CordonNode at the section layer ──────────────────────

func TestStartDrain_NoSuchContext_EmitsErrorEventThroughChannel(t *testing.T) {
	// Wrong / unknown context: StartDrain still returns a DrainStartedMsg
	// with a populated channel so the model's NextDrainEvent loop reads
	// the error and routes it. Without this, the UI would silently fail
	// to flip to the modal.
	mgr := emptyManager()
	cmd := StartDrain(context.Background(), mgr, "no-such-ctx", "n1")
	msg := cmd()
	started, ok := msg.(DrainStartedMsg)
	require.True(t, ok)
	require.NotNil(t, started.Channel)
	assert.Equal(t, "n1", started.Node)
	// The error tick should already be queued on the channel.
	ev := <-started.Channel
	assert.True(t, ev.Done)
	require.Error(t, ev.Err)
}

func TestCordonNode_UnknownContext_ReportsContextNotFound(t *testing.T) {
	mgr := emptyManager()
	cmd := CordonNode(context.Background(), mgr, "no-such-ctx", "n1", false)
	msg := cmd()
	done, ok := msg.(ActionDoneMsg)
	require.True(t, ok)
	assert.Equal(t, "cordon", done.Action)
	assert.Equal(t, "n1", done.Subject)
	require.Error(t, done.Err)
}

func TestCordonNode_UncordonActionLabel(t *testing.T) {
	// desiredSchedulable=true means "uncordon". The action label in the
	// resulting ActionDoneMsg must reflect that — toast wording flows from
	// it.
	mgr := emptyManager()
	cmd := CordonNode(context.Background(), mgr, "no-such-ctx", "n1", true)
	msg := cmd()
	done, ok := msg.(ActionDoneMsg)
	require.True(t, ok)
	assert.Equal(t, "uncordon", done.Action, "desiredSchedulable=true → action='uncordon'")
}

func TestCountPodsOnNode_UnknownContextSurfacesErr(t *testing.T) {
	mgr := emptyManager()
	cmd := CountPodsOnNode(context.Background(), mgr, "no-such-ctx", "n1")
	msg := cmd()
	got, ok := msg.(PodsOnNodeMsg)
	require.True(t, ok)
	assert.Equal(t, "n1", got.Node)
	assert.Equal(t, 0, got.Count)
	require.Error(t, got.Err)
}
