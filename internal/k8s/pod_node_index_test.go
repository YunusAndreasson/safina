package k8s

import (
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestPodNodeIndexTracksWatchChanges(t *testing.T) {
	cs := fake.NewSimpleClientset(
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "a"}, Spec: corev1.PodSpec{NodeName: "node-a"}},
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "b"}, Spec: corev1.PodSpec{NodeName: "node-a"}},
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "pending", Namespace: "a"}},
	)
	c := &Client{Clientset: cs}
	require.Nil(t, c.Informers().PodIndex())
	c.StartInformers()
	t.Cleanup(c.StopInformers)
	waitFor(t, func() bool { return c.Informers().PodIndex() != nil }, "pod index sync")
	rows, err := c.ListPodsOnNode(t.Context(), "node-a")
	require.NoError(t, err)
	require.Len(t, rows, 2)
	require.Equal(t, "a", rows[0].Namespace)
	require.Equal(t, "b", rows[1].Namespace)
	pending, err := cs.CoreV1().Pods("a").Get(t.Context(), "pending", metav1.GetOptions{})
	require.NoError(t, err)
	pending.Spec.NodeName = "node-b"
	_, err = cs.CoreV1().Pods("a").Update(t.Context(), pending, metav1.UpdateOptions{})
	require.NoError(t, err)
	waitFor(t, func() bool {
		assigned, lookupErr := c.Informers().PodIndex().ByTypedIndex(podNodeIndex, "node-b")
		return lookupErr == nil && len(assigned) == 1
	}, "newly scheduled pod indexed")
	unscheduled, err := c.Informers().PodIndex().ByTypedIndex(podNodeIndex, "")
	require.NoError(t, err)
	require.Empty(t, unscheduled)
	require.NoError(t, cs.CoreV1().Pods("a").Delete(t.Context(), "api", metav1.DeleteOptions{}))
	waitFor(t, func() bool {
		remaining, lookupErr := c.Informers().PodIndex().ByTypedIndex(podNodeIndex, "node-a")
		return lookupErr == nil && len(remaining) == 1 && remaining[0].Namespace == "b"
	}, "deleted pod removed from index")
	missing, err := c.ListPodsOnNode(t.Context(), "missing")
	require.NoError(t, err)
	require.Empty(t, missing)
}
