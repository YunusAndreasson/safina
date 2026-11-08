package k8s

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/types"

	"github.com/stretchr/testify/require"
	"github.com/yunus/safina/internal/metrics"
	corev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	metricsv1beta1 "k8s.io/metrics/pkg/apis/metrics/v1beta1"
)

func TestMetricsDecoderPreservesSDKMetadataAndQuantities(t *testing.T) {
	want := metricsv1beta1.PodMetricsList{
		TypeMeta: metav1.TypeMeta{APIVersion: "metrics.k8s.io/v1beta1", Kind: "PodMetricsList"},
		ListMeta: metav1.ListMeta{ResourceVersion: "42"},
		Items: []metricsv1beta1.PodMetrics{{
			ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "shop", UID: "pod-uid"},
			Timestamp:  metav1.NewTime(time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC)),
			Window:     metav1.Duration{Duration: 15 * time.Second},
			Containers: []metricsv1beta1.ContainerMetrics{{Name: "app", Usage: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("250m"),
				corev1.ResourceMemory: resource.MustParse("64Mi"),
			}}},
		}},
	}
	raw, err := json.Marshal(want)
	require.NoError(t, err)
	got, err := decodePodMetrics(raw)
	require.NoError(t, err)
	require.True(t, apiequality.Semantic.DeepEqual(want, got.PodMetricsList), "SDK metadata and quantities must survive decoding")
	require.Empty(t, got.invalid)
}

func TestMetricsSDKDecodingIsolatesInvalidUsage(t *testing.T) {
	for _, tc := range []struct{ name, usage string }{
		{"malformed", `{"cpu":"not-a-quantity","memory":"1Mi"}`},
		{"missing", `{"memory":"1Mi"}`},
		{"null", `{"cpu":null,"memory":"1Mi"}`},
		{"negative", `{"cpu":"-1m","memory":"1Mi"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/apis/metrics.k8s.io/v1beta1/nodes" {
					_, err := fmt.Fprintf(w, `{"items":[{"metadata":{"name":"bad"},"timestamp":"2026-11-08T10:00:00Z","window":"15s","usage":%s},{"metadata":{"name":"good"},"timestamp":"2026-11-08T10:00:00Z","window":"15s","usage":{"cpu":"0","memory":"0"}}]}`, tc.usage)
					if err != nil {
						t.Error(err)
					}
					return
				}
				_, err := fmt.Fprintf(w, `{"items":[{"metadata":{"namespace":"shop","name":"api"},"timestamp":"2026-11-08T10:00:00Z","window":"15s","containers":[{"name":"bad","usage":%s},{"name":"good","usage":{"cpu":"250m","memory":"64Mi"}}]}]}`, tc.usage)
				if err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			cs, err := kubernetes.NewForConfig(&rest.Config{Host: server.URL})
			require.NoError(t, err)
			containers, err := ListPodMetricsByContainer(t.Context(), cs, "shop")
			require.NoError(t, err)
			require.Equal(t, metrics.Invalid, containers[types.NamespacedName{Namespace: "shop", Name: "api"}]["bad"].State)
			good := containers[types.NamespacedName{Namespace: "shop", Name: "api"}]["good"]
			require.True(t, good.Valid())
			require.Equal(t, int64(250), good.CPU)
			require.Equal(t, int64(64<<20), good.Memory)
			pods, err := ListPodMetrics(t.Context(), cs, "shop")
			require.NoError(t, err)
			require.Equal(t, metrics.Invalid, pods[types.NamespacedName{Namespace: "shop", Name: "api"}].State)
			nodes, err := ListNodeMetrics(t.Context(), cs)
			require.NoError(t, err)
			require.Equal(t, metrics.Invalid, nodes["bad"].State)
			require.True(t, nodes["good"].Valid(), "a genuine zero remains available")
		})
	}
}
