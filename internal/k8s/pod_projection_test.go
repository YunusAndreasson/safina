package k8s

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func TestPodProjectionsPreserveSources(t *testing.T) {
	pods := []corev1.Pod{
		{ObjectMeta: metav1.ObjectMeta{Name: "running", Namespace: "ns", OwnerReferences: []metav1.OwnerReference{{UID: "wanted"}}}, Status: corev1.PodStatus{Phase: corev1.PodRunning}},
		{ObjectMeta: metav1.ObjectMeta{Name: "failed", Namespace: "ns", OwnerReferences: []metav1.OwnerReference{{UID: "other"}, {UID: "wanted"}}}, Status: corev1.PodStatus{Phase: corev1.PodFailed}},
		{ObjectMeta: metav1.ObjectMeta{Name: "unrelated", Namespace: "ns", OwnerReferences: []metav1.OwnerReference{{UID: "other"}}}},
	}
	before := (&corev1.PodList{Items: pods}).DeepCopy()
	pointers := []*corev1.Pod{&pods[0], &pods[1], &pods[2]}
	usage := map[types.NamespacedName]ResourceUsage{{Namespace: "ns", Name: "failed"}: {CPU: 20, Memory: 30}, {Namespace: "ns", Name: "running"}: {CPU: 40, Memory: 50}}
	direct := filterPodsByOwner(podPointers(pods), []types.UID{"wanted"}, usage)
	cached := filterPodsByOwner(slices.Values(pointers), []types.UID{"wanted"}, usage)
	require.Equal(t, direct, cached)
	require.Len(t, direct, 2)
	require.Equal(t, "failed", direct[0].Name)
	require.Equal(t, int64(20), direct[0].CPU)
	require.Equal(t, int64(30), direct[0].Memory)
	require.Equal(t, "running", direct[1].Name)
	require.Equal(t, before.Items, pods)
	require.Empty(t, filterPodsByOwner(slices.Values(pointers), []types.UID{"missing"}, usage))
}

func TestPodPointersReusableAfterEarlyStop(t *testing.T) {
	pods := make([]corev1.Pod, 3)
	seq := podPointers(pods)
	for p := range seq {
		require.Same(t, &pods[0], p)
		break
	}
	i := 0
	for p := range seq {
		require.Same(t, &pods[i], p)
		i++
	}
	require.Equal(t, len(pods), i)
	require.Empty(t, slices.Collect(podPointers(nil)))
}
