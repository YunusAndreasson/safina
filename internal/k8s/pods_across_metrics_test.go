package k8s

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// The all-namespaces pod list refreshes every few seconds. Its metrics must
// cost one request, not one per namespace, unless RBAC forbids the
// cluster-wide read; then each namespace is asked separately.
func TestPodsAcrossNamespacesFetchMetricsOnce(t *testing.T) {
	for _, tc := range []struct {
		name           string
		clusterForbids bool
		wantMetrics    int64
	}{
		{"cluster-wide", false, 1},
		{"forbidden falls back per namespace", true, 1 + 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var metricsCalls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				path := r.URL.Path
				switch {
				case strings.HasPrefix(path, "/apis/metrics.k8s.io/"):
					metricsCalls.Add(1)
					if tc.clusterForbids && path == "/apis/metrics.k8s.io/v1beta1/pods" {
						w.WriteHeader(http.StatusForbidden)
						_, _ = fmt.Fprint(w, `{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"Forbidden","code":403}`)
						return
					}
					items := []string{}
					for _, ns := range []string{"a", "b", "c"} {
						if path == "/apis/metrics.k8s.io/v1beta1/pods" || strings.Contains(path, "/namespaces/"+ns+"/") {
							items = append(items, fmt.Sprintf(`{"metadata":{"namespace":%q,"name":"web"},"timestamp":"2026-11-08T10:00:00Z","window":"15s","containers":[{"name":"web","usage":{"cpu":"7m","memory":"1Mi"}}]}`, ns))
						}
					}
					_, _ = fmt.Fprintf(w, `{"items":[%s]}`, strings.Join(items, ","))
				case strings.HasSuffix(path, "/pods"):
					ns := ""
					for _, known := range []string{"a", "b", "c"} {
						if path == "/api/v1/namespaces/"+known+"/pods" {
							ns = known
						}
					}
					_, _ = fmt.Fprintf(w, `{"kind":"PodList","apiVersion":"v1","items":[{"metadata":{"namespace":%q,"name":"web"},"spec":{"containers":[{"name":"web"}]},"status":{"phase":"Running"}}]}`, ns)
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			cs, err := kubernetes.NewForConfig(&rest.Config{Host: server.URL})
			require.NoError(t, err)
			c := &Client{Clientset: cs}

			pods, err := c.ListPodsAcrossNamespaces(t.Context(), []string{"a", "b", "c"})
			require.NoError(t, err)
			require.Len(t, pods, 3)
			for _, p := range pods {
				require.Equal(t, int64(7), p.CPU, "%s/%s keeps its metrics", p.Namespace, p.Name)
			}
			require.Equal(t, tc.wantMetrics, metricsCalls.Load())
		})
	}
}
