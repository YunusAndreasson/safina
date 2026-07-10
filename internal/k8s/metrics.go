package k8s

import (
	"context"
	"encoding/json"
	"fmt"

	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/client-go/kubernetes"
	restclient "k8s.io/client-go/rest"
)

// restClientOrNil returns the core REST client, or nil when the clientset
// can't produce a usable one — fake clientsets return a nil *rest.RESTClient
// that panics on first use. Metrics reads treat that as "no metrics-server",
// the same silent-degrade the contract already promises.
func restClientOrNil(cs kubernetes.Interface) restclient.Interface {
	rc := cs.CoreV1().RESTClient()
	if rc == nil {
		return nil
	}
	if concrete, ok := rc.(*restclient.RESTClient); ok && concrete == nil {
		return nil
	}
	return rc
}

// ResourceUsage carries current pod (or aggregated) CPU + memory usage.
// CPU is in millicores (1 core = 1000); Memory in bytes.
type ResourceUsage struct {
	CPU    int64
	Memory int64
}

// podMetricsList mirrors the metrics.k8s.io/v1beta1 PodMetricsList JSON. We
// don't pull in the typed metrics client to keep deps tight — the schema is
// stable and the response is small.
type podMetricsList struct {
	Items []podMetric `json:"items"`
}

type podMetric struct {
	Metadata struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	} `json:"metadata"`
	Containers []struct {
		Name  string `json:"name"`
		Usage struct {
			CPU    string `json:"cpu"`
			Memory string `json:"memory"`
		} `json:"usage"`
	} `json:"containers"`
}

// ListPodMetrics returns a map of "namespace/podName" → ResourceUsage for
// every pod in the namespace (or all namespaces if namespace == "").
//
// Best-effort: when metrics-server is not installed (404) or RBAC denies the
// request, returns (nil, nil) so callers can render "—" rather than an error
// banner. This is the right call for a TUI — operators on dev clusters
// without metrics-server still want their list view to render.
func ListPodMetrics(ctx context.Context, cs kubernetes.Interface, namespace string) (map[string]ResourceUsage, error) {
	list, ok := fetchPodMetrics(ctx, cs, namespace)
	if !ok {
		return nil, nil
	}
	out := make(map[string]ResourceUsage, len(list.Items))
	for _, m := range list.Items {
		var u ResourceUsage
		for _, c := range m.Containers {
			if cq, err := resource.ParseQuantity(c.Usage.CPU); err == nil {
				u.CPU += cq.MilliValue()
			}
			if mq, err := resource.ParseQuantity(c.Usage.Memory); err == nil {
				u.Memory += mq.Value()
			}
		}
		out[m.Metadata.Namespace+"/"+m.Metadata.Name] = u
	}
	return out, nil
}

// ListPodMetricsByContainer returns per-container usage:
//
//	map["ns/pod"]map["containerName"]ResourceUsage
//
// Same silent-degrade contract as ListPodMetrics.
func ListPodMetricsByContainer(ctx context.Context, cs kubernetes.Interface, namespace string) (map[string]map[string]ResourceUsage, error) {
	list, ok := fetchPodMetrics(ctx, cs, namespace)
	if !ok {
		return nil, nil
	}
	out := make(map[string]map[string]ResourceUsage, len(list.Items))
	for _, m := range list.Items {
		key := m.Metadata.Namespace + "/" + m.Metadata.Name
		byCtr := make(map[string]ResourceUsage, len(m.Containers))
		for _, c := range m.Containers {
			var u ResourceUsage
			if cq, err := resource.ParseQuantity(c.Usage.CPU); err == nil {
				u.CPU = cq.MilliValue()
			}
			if mq, err := resource.ParseQuantity(c.Usage.Memory); err == nil {
				u.Memory = mq.Value()
			}
			byCtr[c.Name] = u
		}
		out[key] = byCtr
	}
	return out, nil
}

func fetchPodMetrics(ctx context.Context, cs kubernetes.Interface, namespace string) (podMetricsList, bool) {
	rc := restClientOrNil(cs)
	if rc == nil {
		return podMetricsList{}, false
	}
	path := "/apis/metrics.k8s.io/v1beta1/pods"
	if namespace != "" {
		path = "/apis/metrics.k8s.io/v1beta1/namespaces/" + namespace + "/pods"
	}
	raw, err := rc.Get().AbsPath(path).DoRaw(ctx)
	if err != nil {
		return podMetricsList{}, false
	}
	var list podMetricsList
	if err := json.Unmarshal(raw, &list); err != nil {
		return podMetricsList{}, false
	}
	return list, true
}

// nodeMetricsList mirrors metrics.k8s.io/v1beta1 NodeMetricsList JSON.
type nodeMetricsList struct {
	Items []nodeMetric `json:"items"`
}

type nodeMetric struct {
	Metadata struct {
		Name string `json:"name"`
	} `json:"metadata"`
	Usage struct {
		CPU    string `json:"cpu"`
		Memory string `json:"memory"`
	} `json:"usage"`
}

// ListNodeMetrics returns "nodeName" → ResourceUsage. Same silent-degrade
// contract as ListPodMetrics.
func ListNodeMetrics(ctx context.Context, cs kubernetes.Interface) (map[string]ResourceUsage, error) {
	rc := restClientOrNil(cs)
	if rc == nil {
		return nil, nil
	}
	raw, err := rc.Get().AbsPath("/apis/metrics.k8s.io/v1beta1/nodes").DoRaw(ctx)
	if err != nil {
		return nil, nil //nolint:nilerr // silent-degrade: metrics-server unavailable
	}
	var list nodeMetricsList
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, nil //nolint:nilerr // silent-degrade: unparseable response
	}
	out := make(map[string]ResourceUsage, len(list.Items))
	for _, n := range list.Items {
		var u ResourceUsage
		if cq, err := resource.ParseQuantity(n.Usage.CPU); err == nil {
			u.CPU = cq.MilliValue()
		}
		if mq, err := resource.ParseQuantity(n.Usage.Memory); err == nil {
			u.Memory = mq.Value()
		}
		out[n.Metadata.Name] = u
	}
	return out, nil
}

// FormatCPU renders millicores kubectl-style (e.g. "234m"). Returns "—" when
// metrics are unavailable so the column visibly degrades.
func FormatCPU(milli int64) string {
	if milli == 0 {
		return "—"
	}
	return fmt.Sprintf("%dm", milli)
}

// FormatMem renders bytes in IEC binary units, sized for a tight column —
// "234B", "12Ki", "187Mi", "1.2Gi". Returns "—" when unavailable.
func FormatMem(bytes int64) string {
	if bytes == 0 {
		return "—"
	}
	const (
		ki = int64(1024)
		mi = ki * 1024
		gi = mi * 1024
	)
	switch {
	case bytes >= gi:
		return fmt.Sprintf("%.1fGi", float64(bytes)/float64(gi))
	case bytes >= mi:
		return fmt.Sprintf("%dMi", bytes/mi)
	case bytes >= ki:
		return fmt.Sprintf("%dKi", bytes/ki)
	default:
		return fmt.Sprintf("%dB", bytes)
	}
}
