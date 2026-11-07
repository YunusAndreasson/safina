package k8s

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestCordonUncordon(t *testing.T) {
	ctx := context.Background()
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n1"}}
	cs := fake.NewSimpleClientset(node)

	require.NoError(t, CordonNode(ctx, cs, "n1"))
	got, err := cs.CoreV1().Nodes().Get(ctx, "n1", metav1.GetOptions{})
	require.NoError(t, err)
	assert.True(t, got.Spec.Unschedulable, "expected cordoned")

	require.NoError(t, UncordonNode(ctx, cs, "n1"))
	got, err = cs.CoreV1().Nodes().Get(ctx, "n1", metav1.GetOptions{})
	require.NoError(t, err)
	assert.False(t, got.Spec.Unschedulable, "expected uncordoned")
}

func TestDrain_EmptyNode_FinishesImmediately(t *testing.T) {
	ctx := context.Background()
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n1"}}
	cs := fake.NewSimpleClientset(node)

	ch := make(chan DrainEvent, 8)
	go func() { _ = DrainNode(ctx, cs, "n1", DefaultDrainOptions(), ch) }()

	var phases []string
	for e := range ch {
		phases = append(phases, e.Phase)
		if e.Done {
			break
		}
	}
	assert.Contains(t, phases, "cordoning")
	assert.Equal(t, "done", phases[len(phases)-1])

	got, err := cs.CoreV1().Nodes().Get(ctx, "n1", metav1.GetOptions{})
	require.NoError(t, err)
	// committed=true path: the node STAYS cordoned after a successful
	// drain — that's the point of the operation. Caller uncordons later.
	assert.True(t, got.Spec.Unschedulable, "successful drain should leave node cordoned")
}

func TestListDrainablePods_FiltersDaemonSetAndMirror(t *testing.T) {
	ctx := context.Background()
	good := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "app", Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{
				{Kind: "ReplicaSet", Controller: boolPtr(true)},
			},
		},
		Spec: corev1.PodSpec{NodeName: "n1"},
	}
	ds := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "ds", Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{
				{APIVersion: "apps/v1", Kind: "DaemonSet", Name: "ds", Controller: boolPtr(true)},
			},
		},
		Spec: corev1.PodSpec{NodeName: "n1"},
	}
	mirror := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "kube-apiserver", Namespace: "kube-system",
			Annotations: map[string]string{"kubernetes.io/config.mirror": "x"},
		},
		Spec: corev1.PodSpec{NodeName: "n1"},
	}
	other := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "elsewhere", Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{
				{Kind: "ReplicaSet", Controller: boolPtr(true)},
			},
		},
		Spec: corev1.PodSpec{NodeName: "n2"},
	}
	cs := fake.NewSimpleClientset(good, ds, mirror, other, &appsv1.DaemonSet{ObjectMeta: metav1.ObjectMeta{Name: "ds", Namespace: "default"}})

	// NB: fake clientset's FieldSelector support is patchy; verify ours
	// works *for the fields we care about* by counting what we get.
	pods, err := listDrainablePods(ctx, cs, "n1", DefaultDrainOptions())
	require.NoError(t, err)
	names := make([]string, 0, len(pods))
	for _, p := range pods {
		names = append(names, p.Namespace+"/"+p.Name)
	}
	// Mirror + DaemonSet filtered; only `app` survives. `elsewhere` is on
	// n2 so it should be filtered by the FieldSelector — but the fake
	// clientset ignores FieldSelector. We assert by name to keep the test
	// independent of that quirk.
	assert.Contains(t, names, "default/app")
	for _, n := range names {
		assert.NotEqual(t, "default/ds", n, "daemonset pod should be filtered")
		assert.NotEqual(t, "kube-system/kube-apiserver", n, "mirror pod should be filtered")
	}
}

func boolPtr(b bool) *bool { return &b }
