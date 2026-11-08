package k8s

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/YunusAndreasson/safina/internal/k8s/testutil"
)

// All these tests pin filter behavior for the drain path. Subtle bugs here
// can either (a) leave a pod un-evicted forever (drain hangs) or (b) evict
// something that shouldn't be touched (mirror pods,
// orphans without --force) — both are user-visible foot-guns.

func TestListDrainablePods_WaitsForTerminatingPods(t *testing.T) {
	// Recorded fixture has DeletionTimestamp set in the JSON — closer to a
	// real terminating pod than a synthesized one with `time.Now()` baked
	// in at test runtime.
	p := testutil.LoadPod(t, "po_terminating")
	p.Spec.NodeName = "n1"
	cs := testutil.NewClientset(t, p)

	pods, err := listDrainablePods(context.Background(), cs, "n1", DefaultDrainOptions())
	require.NoError(t, err)
	assert.Len(t, pods, 1, "terminating pod must remain until deletion completes")
}

func TestListDrainablePods_RejectsOrphansWhenForceFalse(t *testing.T) {
	// An orphan pod (no controller) would NOT be re-created after eviction.
	// Without Force, unmanaged pods must block drain rather than disappear
	// from its completion criteria.
	orphan := testutil.Pod("orphan", testutil.WithOrphan())
	cs := testutil.NewClientset(t, orphan)

	pods, err := listDrainablePods(context.Background(), cs, "n1", DefaultDrainOptions())
	require.Error(t, err)
	assert.Empty(t, pods)
}

func TestListDrainablePods_IncludesOrphansWhenForceTrue(t *testing.T) {
	orphan := testutil.Pod("orphan", testutil.WithOrphan())
	cs := testutil.NewClientset(t, orphan)

	opts := DefaultDrainOptions()
	opts.Force = true
	pods, err := listDrainablePods(context.Background(), cs, "n1", opts)
	require.NoError(t, err)
	require.Len(t, pods, 1)
	assert.Equal(t, "orphan", pods[0].Name)
}

func TestListDrainablePods_RejectsEmptyDirWhenDeleteEmptyDirFalse(t *testing.T) {
	p := testutil.Pod("scratch", testutil.WithEmptyDirVolume("tmp"))
	cs := testutil.NewClientset(t, p)

	opts := DefaultDrainOptions()
	opts.DeleteEmptyDir = false
	pods, err := listDrainablePods(context.Background(), cs, "n1", opts)
	require.Error(t, err)
	assert.Empty(t, pods)
}

func TestListDrainablePods_IncludesEmptyDirWhenDefault(t *testing.T) {
	// DefaultDrainOptions sets DeleteEmptyDir=true — the operator
	// implicitly agreed that scratch space is expendable when they
	// invoked drain from safina. This test pins that default.
	p := testutil.Pod("scratch", testutil.WithEmptyDirVolume("tmp"))
	cs := testutil.NewClientset(t, p)

	pods, err := listDrainablePods(context.Background(), cs, "n1", DefaultDrainOptions())
	require.NoError(t, err)
	require.Len(t, pods, 1, "default options must evict emptyDir-backed pods")
	assert.Equal(t, "scratch", pods[0].Name)
}

func TestListDrainablePods_DaemonSetAlwaysSkippedWithDefaults(t *testing.T) {
	// Sanity: DefaultDrainOptions.IgnoreDaemonSets=true. A DS pod must be
	// filtered or drain would hang re-listing the same DS-managed pod
	// forever (the DS recreates it immediately after eviction).
	ds := testutil.Pod("ds-pod", testutil.WithOwnedByDaemonSet())
	cs := testutil.NewClientset(t, ds, &appsv1.DaemonSet{ObjectMeta: metav1.ObjectMeta{Name: "ds", Namespace: "default"}})

	pods, err := listDrainablePods(context.Background(), cs, "n1", DefaultDrainOptions())
	require.NoError(t, err)
	assert.Empty(t, pods, "DaemonSet pod must be filtered with default opts")
}

func TestDefaultDrainOptions_OpinionsMatchTUIRequirements(t *testing.T) {
	// Pin the documented opinions. If anyone flips one of these silently
	// the drain UX would change without an explanation; this test forces
	// the change to be visible in a code review.
	o := DefaultDrainOptions()
	assert.True(t, o.IgnoreDaemonSets, "DS pods would otherwise hang the drain")
	assert.True(t, o.DeleteEmptyDir, "operators don't want to be quizzed about scratch volumes")
	assert.False(t, o.Force, "fail-loud on orphans is intentional")
	assert.Equal(t, 30*time.Second, o.GracePeriod)
	assert.Equal(t, 10*time.Minute, o.Timeout)
}

func TestPodsOnNode_MatchesListDrainablePodsCount(t *testing.T) {
	// The drain-confirm modal shows "evicts N pods" from PodsOnNode. If
	// this number disagrees with the actual eviction count, the operator
	// is being lied to about a destructive action. Pin the invariant.
	good := testutil.Pod("good")
	ds := testutil.Pod("dsp", testutil.WithOwnedByDaemonSet())
	cs := testutil.NewClientset(t, good, ds, &appsv1.DaemonSet{ObjectMeta: metav1.ObjectMeta{Name: "ds", Namespace: "default"}})

	n, err := PodsOnNode(context.Background(), cs, "n1", DefaultDrainOptions())
	require.NoError(t, err)
	drainable, err := listDrainablePods(context.Background(), cs, "n1", DefaultDrainOptions())
	require.NoError(t, err)
	assert.Equal(t, len(drainable), n, "PodsOnNode must match what would actually be evicted")
}
