package k8s

import (
	"context"
	"errors"
	"fmt"

	"github.com/yunus/safina/internal/metrics"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"

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
type ResourceUsage = metrics.Reading

// ListPodMetrics returns SDK namespaced pod identities mapped to ResourceUsage for
// every pod in the namespace (or all namespaces if namespace == "").
//
// Errors describe availability; resource-list callers retain rows and annotate metrics.
func ListPodMetrics(ctx context.Context, cs kubernetes.Interface, namespace string) (map[types.NamespacedName]ResourceUsage, error) {
	list, err := fetchPodMetrics(ctx, cs, namespace)
	if err != nil {
		return nil, err
	}
	out := make(map[types.NamespacedName]ResourceUsage, len(list.Items))
	for _, m := range list.Items {
		u := ResourceUsage{SourceAt: m.Timestamp.Time, Window: m.Window.Duration, State: metrics.Available, Covered: len(m.Containers), Expected: len(m.Containers)}
		for _, c := range m.Containers {
			u.ContainerNames += "/" + c.Name + "/"
			cpu, memory, valid := metricAmounts(c.Usage)
			u.CPU += cpu
			u.Memory += memory
			if !valid || list.invalid[types.NamespacedName{Namespace: m.Namespace, Name: m.Name}].Has(c.Name) {
				u.State = metrics.Invalid
			}
		}
		if u.SourceAt.IsZero() || u.CPU < 0 || u.Memory < 0 || u.Window < 0 {
			u.State = metrics.Invalid
		}
		out[types.NamespacedName{Namespace: m.Namespace, Name: m.Name}] = u
	}
	return out, nil
}

// ListPodMetricsByContainer returns per-container usage:
//
//	map[types.NamespacedName]map["containerName"]ResourceUsage
//
// Availability errors follow ListPodMetrics.
func ListPodMetricsByContainer(ctx context.Context, cs kubernetes.Interface, namespace string) (map[types.NamespacedName]map[string]ResourceUsage, error) {
	list, err := fetchPodMetrics(ctx, cs, namespace)
	if err != nil {
		return nil, err
	}
	out := make(map[types.NamespacedName]map[string]ResourceUsage, len(list.Items))
	for _, m := range list.Items {
		key := types.NamespacedName{Namespace: m.Namespace, Name: m.Name}
		byCtr := make(map[string]ResourceUsage, len(m.Containers))
		for _, c := range m.Containers {
			u := ResourceUsage{SourceAt: m.Timestamp.Time, Window: m.Window.Duration, State: metrics.Available, Covered: 1, Expected: 1}
			var valid bool
			u.CPU, u.Memory, valid = metricAmounts(c.Usage)
			if !valid || list.invalid[types.NamespacedName{Namespace: m.Namespace, Name: m.Name}].Has(c.Name) {
				u.State = metrics.Invalid
			}
			if u.SourceAt.IsZero() || u.CPU < 0 || u.Memory < 0 || u.Window < 0 {
				u.State = metrics.Invalid
			}
			byCtr[c.Name] = u
		}
		out[key] = byCtr
	}
	return out, nil
}

func fetchPodMetrics(ctx context.Context, cs kubernetes.Interface, namespace string) (podMetricsResult, error) {
	rc := restClientOrNil(cs)
	if rc == nil {
		return podMetricsResult{}, errMetricsUnsupported
	}
	path := "/apis/metrics.k8s.io/v1beta1/pods"
	if namespace != "" {
		path = "/apis/metrics.k8s.io/v1beta1/namespaces/" + namespace + "/pods"
	}
	raw, err := rc.Get().AbsPath(path).DoRaw(ctx)
	if err != nil {
		return podMetricsResult{}, err
	}
	return decodePodMetrics(raw)
}

// ListNodeMetrics returns "nodeName" → ResourceUsage. Same silent-degrade
// availability errors as ListPodMetrics.
func ListNodeMetrics(ctx context.Context, cs kubernetes.Interface) (map[string]ResourceUsage, error) {
	rc := restClientOrNil(cs)
	if rc == nil {
		return nil, errMetricsUnsupported
	}
	raw, err := rc.Get().AbsPath("/apis/metrics.k8s.io/v1beta1/nodes").DoRaw(ctx)
	if err != nil {
		return nil, err
	}
	list, err := decodeNodeMetrics(raw)
	if err != nil {
		return nil, err
	}
	out := make(map[string]ResourceUsage, len(list.Items))
	for _, n := range list.Items {
		u := ResourceUsage{SourceAt: n.Timestamp.Time, Window: n.Window.Duration, State: metrics.Available, Covered: 1, Expected: 1}
		var valid bool
		u.CPU, u.Memory, valid = metricAmounts(n.Usage)
		if !valid || list.invalid.Has(n.Name) {
			u.State = metrics.Invalid
		}
		if u.SourceAt.IsZero() || u.CPU < 0 || u.Memory < 0 || u.Window < 0 {
			u.State = metrics.Invalid
		}
		out[n.Name] = u
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

var errMetricsUnsupported = errors.New("metrics API unavailable")

func withMetricError(u ResourceUsage, errs []error) ResourceUsage {
	if len(errs) == 0 || errs[0] == nil {
		return u
	}
	switch {
	case apierrors.IsForbidden(errs[0]), apierrors.IsUnauthorized(errs[0]):
		u.State = metrics.Forbidden
	case apierrors.IsNotFound(errs[0]), errors.Is(errs[0], errMetricsUnsupported):
		u.State = metrics.Unsupported
	default:
		u.State = metrics.Unavailable
	}
	return u
}
