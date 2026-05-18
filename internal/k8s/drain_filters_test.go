package k8s

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// All these tests pin filter behavior for the drain path. Subtle bugs here
// can either (a) leave a pod un-evicted forever (drain hangs) or (b) evict
// something that shouldn't be touched (mirror pods, terminating pods,
// orphans without --force) — both are user-visible foot-guns.

func ownedByReplicaSet(name string) []metav1.OwnerReference {
	ctrl := true
	return []metav1.OwnerReference{{Kind: "ReplicaSet", Name: "rs", Controller: &ctrl}}
}

func newPod(name, ns, node string, opts ...func(*corev1.Pod)) *corev1.Pod {
	p := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: ns,
			OwnerReferences: ownedByReplicaSet(name),
		},
		Spec: corev1.PodSpec{NodeName: node},
	}
	for _, o := range opts {
		o(p)
	}
	return p
}

func TestListDrainablePods_SkipsTerminatingPods(t *testing.T) {
	// A pod with DeletionTimestamp set is already being torn down. Trying
	// to evict it again is wasted work and surfaces noisy errors.
	now := metav1.NewTime(time.Now())
	p := newPod("dying", "default", "n1", func(p *corev1.Pod) {
		p.DeletionTimestamp = &now
	})
	cs := fake.NewSimpleClientset(p)

	pods, err := listDrainablePods(context.Background(), cs, "n1", DefaultDrainOptions())
	require.NoError(t, err)
	assert.Empty(t, pods, "terminating pod must be skipped")
}

func TestListDrainablePods_SkipsOrphansWhenForceFalse(t *testing.T) {
	// An orphan pod (no controller) would NOT be re-created after eviction.
	// Skipping unless Force=true matches kubectl drain behavior. The TUI's
	// hardcoded Force=false means an orphan keeps the drain from
	// completing — that's a load-bearing failure mode, not a bug.
	orphan := newPod("orphan", "default", "n1", func(p *corev1.Pod) {
		p.OwnerReferences = nil
	})
	cs := fake.NewSimpleClientset(orphan)

	pods, err := listDrainablePods(context.Background(), cs, "n1", DefaultDrainOptions())
	require.NoError(t, err)
	assert.Empty(t, pods, "orphan pod must be skipped when Force=false")
}

func TestListDrainablePods_IncludesOrphansWhenForceTrue(t *testing.T) {
	orphan := newPod("orphan", "default", "n1", func(p *corev1.Pod) {
		p.OwnerReferences = nil
	})
	cs := fake.NewSimpleClientset(orphan)

	opts := DefaultDrainOptions()
	opts.Force = true
	pods, err := listDrainablePods(context.Background(), cs, "n1", opts)
	require.NoError(t, err)
	require.Len(t, pods, 1)
	assert.Equal(t, "orphan", pods[0].Name)
}

func TestListDrainablePods_SkipsEmptyDirWhenDeleteEmptyDirFalse(t *testing.T) {
	p := newPod("scratch", "default", "n1", func(p *corev1.Pod) {
		p.Spec.Volumes = []corev1.Volume{{
			Name: "tmp", VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		}}
	})
	cs := fake.NewSimpleClientset(p)

	opts := DefaultDrainOptions()
	opts.DeleteEmptyDir = false
	pods, err := listDrainablePods(context.Background(), cs, "n1", opts)
	require.NoError(t, err)
	assert.Empty(t, pods, "emptyDir pod must be skipped when DeleteEmptyDir=false")
}

func TestListDrainablePods_IncludesEmptyDirWhenDefault(t *testing.T) {
	// DefaultDrainOptions sets DeleteEmptyDir=true — the operator
	// implicitly agreed that scratch space is expendable when they
	// invoked drain from safina. This test pins that default.
	p := newPod("scratch", "default", "n1", func(p *corev1.Pod) {
		p.Spec.Volumes = []corev1.Volume{{
			Name: "tmp", VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		}}
	})
	cs := fake.NewSimpleClientset(p)

	pods, err := listDrainablePods(context.Background(), cs, "n1", DefaultDrainOptions())
	require.NoError(t, err)
	require.Len(t, pods, 1, "default options must evict emptyDir-backed pods")
	assert.Equal(t, "scratch", pods[0].Name)
}

func TestListDrainablePods_DaemonSetAlwaysSkippedWithDefaults(t *testing.T) {
	// Sanity: DefaultDrainOptions.IgnoreDaemonSets=true. A DS pod must be
	// filtered or drain would hang re-listing the same DS-managed pod
	// forever (the DS recreates it immediately after eviction).
	ctrl := true
	ds := newPod("ds-pod", "default", "n1", func(p *corev1.Pod) {
		p.OwnerReferences = []metav1.OwnerReference{
			{Kind: "DaemonSet", Controller: &ctrl},
		}
	})
	cs := fake.NewSimpleClientset(ds)

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
	good := newPod("good", "default", "n1")
	ds := newPod("dsp", "default", "n1", func(p *corev1.Pod) {
		ctrl := true
		p.OwnerReferences = []metav1.OwnerReference{{Kind: "DaemonSet", Controller: &ctrl}}
	})
	cs := fake.NewSimpleClientset(good, ds)

	n, err := PodsOnNode(context.Background(), cs, "n1", DefaultDrainOptions())
	require.NoError(t, err)
	drainable, err := listDrainablePods(context.Background(), cs, "n1", DefaultDrainOptions())
	require.NoError(t, err)
	assert.Equal(t, len(drainable), n, "PodsOnNode must match what would actually be evicted")
}
