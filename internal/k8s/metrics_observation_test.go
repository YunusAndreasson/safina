package k8s

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/types"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yunus/safina/internal/metrics"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

func TestMetricAvailabilityAndSourceMetadata(t *testing.T) {
	for _, status := range []int{200, 403, 404, 503} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				if status == 200 {
					_, _ = fmt.Fprint(w, `{"items":[{"metadata":{"namespace":"shop","name":"api"},"timestamp":"2026-11-07T10:00:00Z","window":"15s","containers":[{"name":"app","usage":{"cpu":"0","memory":"0"}}]}]}`)
				} else {
					_, _ = fmt.Fprint(w, `{"kind":"Status","apiVersion":"v1","status":"Failure","code":`+strconv.Itoa(status)+`}`)
				}
			}))
			defer server.Close()
			cs, err := kubernetes.NewForConfig(&rest.Config{Host: server.URL})
			require.NoError(t, err)
			values, err := ListPodMetrics(t.Context(), cs, "shop")
			if status == 200 {
				require.NoError(t, err)
				r := values[types.NamespacedName{Namespace: "shop", Name: "api"}]
				assert.True(t, r.Valid())
				assert.Zero(t, r.CPU)
				assert.Equal(t, 15*time.Second, r.Window)
				assert.False(t, r.SourceAt.IsZero())
			} else {
				require.Error(t, err)
				want := map[int]string{403: metrics.Forbidden, 404: metrics.Unsupported, 503: metrics.Unavailable}[status]
				assert.Equal(t, want, withMetricError(ResourceUsage{}, []error{err}).State)
			}
		})
	}
}

func TestMetricBudgetsAndPartialWorkload(t *testing.T) {
	now := time.Now()
	limits := corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1"), corev1.ResourceMemory: resource.MustParse("256Mi")}
	pod := corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "shop", Labels: map[string]string{"app": "api"}}, Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Resources: corev1.ResourceRequirements{Limits: limits, Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m")}}}}}}
	r := ResourceUsage{CPU: 20, Memory: 32 << 20, State: metrics.Available, SourceAt: now, ContainerNames: "/app/"}
	budget := podMetricBudget(&pod, r)
	assert.Equal(t, int64(1000), budget.CPULimit)
	assert.Equal(t, int64(100), budget.CPURequest)
	other := pod.DeepCopy()
	other.Name = "api-2"
	selector := &metav1.LabelSelector{MatchLabels: map[string]string{"app": "api"}}
	aggregate := aggregateMetrics("shop", selector, []corev1.Pod{pod, *other}, map[types.NamespacedName]ResourceUsage{{Namespace: "shop", Name: "api"}: r})
	assert.Equal(t, metrics.Partial, aggregate.State)
	assert.Equal(t, 1, aggregate.Covered)
	assert.Equal(t, 2, aggregate.Expected)
	assert.Equal(t, int64(2000), aggregate.CPULimit)
	pod.Spec.Containers = append(pod.Spec.Containers, corev1.Container{Name: "sidecar"})
	budget = podMetricBudget(&pod, r)
	assert.Zero(t, budget.CPULimit, "one unbounded container makes the aggregate limit unknown")
	assert.Equal(t, metrics.Partial, budget.State, "a missing container is not zero")
	pod.Spec.Resources = &corev1.ResourceRequirements{Limits: limits}
	budget = podMetricBudget(&pod, r)
	assert.Equal(t, int64(1000), budget.CPULimit, "pod-level limit applies to all containers")
}

func TestPoolFingerprintTracksEveryNodeSource(t *testing.T) {
	now := time.Now()
	nodes := []NodeInfo{
		{Name: "a", Pool: "workers", Metrics: ResourceUsage{State: metrics.Available, SourceAt: now}},
		{Name: "b", Pool: "workers", Metrics: ResourceUsage{State: metrics.Available, SourceAt: now}},
	}
	first := groupIntoPools(nodes)[0]
	nodes[1].Metrics.SourceAt = now.Add(time.Second)
	next := groupIntoPools(nodes)[0]
	assert.Equal(t, first.Metrics.SourceAt, next.Metrics.SourceAt)
	assert.NotEqual(t, first.Metrics.Fingerprint, next.Metrics.Fingerprint, "a slower node must not hide its peer's fresh reading")
}
