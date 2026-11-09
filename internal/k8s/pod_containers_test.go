package k8s

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// The log target picker reads each pod of a workload; it must not pay a
// namespace-wide metrics request per pod for numbers it never shows.
func TestPodContainersAsksNoMetrics(t *testing.T) {
	var metricsCalls atomic.Int32
	pod := corev1.Pod{
		TypeMeta:   metav1.TypeMeta{Kind: "Pod", APIVersion: "v1"},
		ObjectMeta: metav1.ObjectMeta{Name: "web-1", Namespace: "shop"},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app"}}},
		Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{
			Name: "app", RestartCount: 2, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
		}}},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/apis/metrics.k8s.io/") {
			metricsCalls.Add(1)
		}
		if r.URL.Path != "/api/v1/namespaces/shop/pods/web-1" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(pod)
	}))
	t.Cleanup(server.Close)
	cs, err := kubernetes.NewForConfig(&rest.Config{Host: server.URL})
	require.NoError(t, err)

	containers, err := (&Client{Clientset: cs}).PodContainers(t.Context(), "shop", "web-1")
	require.NoError(t, err)
	require.Len(t, containers, 1)
	require.Equal(t, "app", containers[0].Name)
	require.Equal(t, int32(2), containers[0].Restarts)
	require.Zero(t, metricsCalls.Load())
}
