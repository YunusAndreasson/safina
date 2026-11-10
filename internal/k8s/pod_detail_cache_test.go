package k8s

import (
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func detailFixtures() (*corev1.Pod, *corev1.Service, *corev1.Service) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "api-1", Namespace: "shop", Labels: map[string]string{"app": "api"},
			OwnerReferences: []metav1.OwnerReference{{Kind: "ReplicaSet", Name: "api-7f", Controller: new(true)}},
		},
		Spec: corev1.PodSpec{
			ServiceAccountName: "api",
			Containers: []corev1.Container{{
				Name: "api", Image: "api:1",
				EnvFrom: []corev1.EnvFromSource{{ConfigMapRef: &corev1.ConfigMapEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: "api-env"}}}},
			}},
		},
		Status: corev1.PodStatus{PodIP: "10.0.0.7", HostIP: "192.168.1.2", QOSClass: corev1.PodQOSBestEffort},
	}
	selects := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "shop"}, Spec: corev1.ServiceSpec{Selector: map[string]string{"app": "api"}}}
	other := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "shop"}, Spec: corev1.ServiceSpec{Selector: map[string]string{"app": "web"}}}
	return pod, selects, other
}

// Once the pod and service caches are synced, the inspector's and the
// connected pane's pod reads must give the direct reads' answer without a
// request: k9s moves its cursor on watch caches alone.
func TestPodDetailAndConnectionsComeFromTheCache(t *testing.T) {
	pod, selects, other := detailFixtures()
	cs := fake.NewSimpleClientset(pod, selects, other)
	wantDetail, err := InspectPod(t.Context(), cs, "shop", "api-1")
	require.NoError(t, err)
	wantConns, err := ConnectionsForPod(t.Context(), cs, "shop", "api-1")
	require.NoError(t, err)
	require.Len(t, wantConns.UsedBy, 1, "the fixture has one selecting service")

	c := &Client{Clientset: cs}
	c.StartInformers()
	t.Cleanup(c.StopInformers)
	waitFor(t, func() bool { return c.Informers().Pods() != nil && c.Informers().Services() != nil }, "pod and service caches sync")

	before := len(cs.Actions())
	gotDetail, err := c.InspectPod(t.Context(), "shop", "api-1")
	require.NoError(t, err)
	gotConns, err := c.ConnectionsForPod(t.Context(), "shop", "api-1")
	require.NoError(t, err)
	require.Equal(t, wantDetail, gotDetail)
	require.Equal(t, wantConns, gotConns)
	require.Len(t, cs.Actions(), before, "cached reads send nothing")
}

// A pod the cache does not hold yet (just created, or the cache is still
// syncing) is read from the API as before.
func TestPodDetailFallsBackToTheAPI(t *testing.T) {
	pod, selects, _ := detailFixtures()
	cs := fake.NewSimpleClientset(selects)
	c := &Client{Clientset: cs}
	_, err := c.InspectPod(t.Context(), "shop", "api-1")
	require.Error(t, err, "no cache, no pod")
	_, err = cs.CoreV1().Pods("shop").Create(t.Context(), pod, metav1.CreateOptions{})
	require.NoError(t, err)
	d, err := c.InspectPod(t.Context(), "shop", "api-1")
	require.NoError(t, err)
	require.Equal(t, "10.0.0.7", d.PodIP)
	conns, err := c.ConnectionsForPod(t.Context(), "shop", "api-1")
	require.NoError(t, err)
	require.Len(t, conns.UsedBy, 1)
}
