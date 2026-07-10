package k8s

import (
	"context"
	"io"
	"sort"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
)

type PodInfo struct {
	Name      string
	Namespace string
	Status    string
	Restarts  int32
	Age       string
	// CPU is current usage in millicores; Memory is current usage in bytes.
	// Both are zero when metrics-server is unavailable; render via FormatCPU
	// / FormatMem to get a "—" placeholder.
	CPU    int64
	Memory int64
	// NodeName is the node the pod is scheduled on; "" if unscheduled. Used
	// in the breadcrumb summary line for the Pod focus.
	NodeName string
}

func podToInfo(p corev1.Pod) PodInfo {
	restarts := int32(0)
	for _, cs := range p.Status.ContainerStatuses {
		restarts += cs.RestartCount
	}

	status := string(p.Status.Phase)
	// Container-level fault reasons (CrashLoopBackOff, ImagePullBackOff,
	// ErrImagePull) override the coarse Phase so the row tells the operator
	// what's actually wrong instead of just "Pending"/"Running".
	for _, cs := range p.Status.ContainerStatuses {
		if cs.State.Waiting != nil {
			reason := cs.State.Waiting.Reason
			if reason == "CrashLoopBackOff" || reason == "ImagePullBackOff" || reason == "ErrImagePull" {
				status = reason
				break
			}
		}
		if cs.State.Terminated != nil {
			reason := cs.State.Terminated.Reason
			if reason == "OOMKilled" || reason == "Error" {
				status = reason
				break
			}
		}
	}
	// Surface Evicted pods explicitly — p.Status.Phase is "Failed" but the
	// Reason field carries the more actionable "Evicted" label.
	if p.Status.Reason == "Evicted" {
		status = "Evicted"
	}
	// Terminating wins over everything else: a pod the kubelet is tearing
	// down isn't going to recover from a crash loop, and the operator's
	// next action depends on knowing it's already on its way out. Apply
	// this last so it overrides the container-fault overlay above.
	if p.DeletionTimestamp != nil {
		status = "Terminating"
	}

	age := HumanAge(p.CreationTimestamp.Time)

	return PodInfo{
		Name:      p.Name,
		Namespace: p.Namespace,
		Status:    status,
		Restarts:  restarts,
		Age:       age,
		NodeName:  p.Spec.NodeName,
	}
}

// ListHotPodsByCPU is the direct-API path. Callers with a *Client should use
// Client.ListHotPodsByCPU which prefers the informer cache. This function
// remains exported so callers without an informer-backed client (the bench
// harness, future ad-hoc callers) can still issue the underlying List.
//
// Returns the top `limit` pods cluster-wide sorted by current CPU usage
// descending. Pods without metrics-server data are excluded — the hot list
// is a "what's noisy right now" view, and a pod with no metrics has no claim
// to a spot at the top.
//
// The two list calls (pods + pod metrics, both cluster-scoped) are issued
// in parallel; metrics failure (404, RBAC) yields an empty list rather than
// an error, mirroring the silent-degrade contract of the rest of the
// metrics-server callers — the hot view simply has nothing to show.
func ListHotPodsByCPU(ctx context.Context, cs kubernetes.Interface, limit int) ([]PodInfo, error) {
	if limit <= 0 {
		limit = 20
	}

	// Server-side filter to Running pods: only Running pods have CPU metrics
	// (kubelet doesn't report for Pending/Succeeded/Failed), so we'd discard
	// non-Running rows downstream anyway. Pushing the filter to the API server
	// drops the payload ~5× on a typical cluster — measured at -50% latency,
	// 397 KiB → 83 KiB on the bench harness's 100-pod fixture (S1, V4 cell).
	// Phase=Running still matches Terminating and CrashLoopBackOff pods, which
	// remain visible.
	pods, err := cs.CoreV1().Pods("").List(ctx, metav1.ListOptions{
		FieldSelector: "status.phase=Running",
	})
	if err != nil {
		return nil, err
	}
	metrics, _ := ListPodMetrics(ctx, cs, "")
	if len(metrics) == 0 {
		// No metrics → nothing to sort by. Surface an empty list cleanly
		// so the modal can render its "metrics-server unavailable" hint.
		return nil, nil
	}

	out := make([]PodInfo, 0, len(pods.Items))
	for _, p := range pods.Items {
		u, ok := metrics[p.Namespace+"/"+p.Name]
		if !ok {
			continue
		}
		info := podToInfo(p)
		info.CPU = u.CPU
		info.Memory = u.Memory
		out = append(out, info)
	}

	return rankHotPods(out, limit), nil
}

// rankHotPods is the pure sort+truncate that ListHotPodsByCPU uses, lifted
// out for direct unit tests. Sort order: CPU desc, then memory desc, then
// namespace asc, then name asc. The tiebreak chain is what keeps the modal
// from flickering positions between refreshes when many pods are near-idle.
func rankHotPods(pods []PodInfo, limit int) []PodInfo {
	if limit <= 0 {
		limit = 20
	}
	sort.Slice(pods, func(i, j int) bool {
		if pods[i].CPU != pods[j].CPU {
			return pods[i].CPU > pods[j].CPU
		}
		if pods[i].Memory != pods[j].Memory {
			return pods[i].Memory > pods[j].Memory
		}
		if pods[i].Namespace != pods[j].Namespace {
			return pods[i].Namespace < pods[j].Namespace
		}
		return pods[i].Name < pods[j].Name
	})
	if len(pods) > limit {
		pods = pods[:limit]
	}
	return pods
}

type PodLogEvent struct {
	Line string
	Err  error
	Done bool
}

// StreamPodContainerLogs streams logs from a specific container inside a
// pod. Pass container == "" to let kubelet pick the default (first) container.
func StreamPodContainerLogs(ctx context.Context, cs kubernetes.Interface, namespace, name, container string, previous bool, ch chan<- PodLogEvent) {
	defer close(ch)

	req := cs.CoreV1().Pods(namespace).GetLogs(name, &corev1.PodLogOptions{
		Container: container,
		Follow:    true,
		Previous:  previous,
		TailLines: int64Ptr(500),
		// TailLines bounds lines, not bytes — a container that logged one
		// pathological multi-hundred-MB line would flood the stream (and the
		// TUI buffer) through a 500-line window. 10 MiB is far beyond any
		// legitimate on-call read.
		LimitBytes: int64Ptr(10 << 20),
		// Ask kubelet to prepend an RFC3339Nano timestamp to every line.
		// Without this, apps that emit plain output ("Starting server",
		// "Listening on :8080") arrive with no temporal anchor — useless
		// for correlation during an incident, and the log colorizer has
		// nothing to dim. With timestamps, every line gets a recessive
		// time prefix the eye glides past, and operators can match
		// log entries against the kubelet's view of "when did this
		// happen". Same default stern / k9s ship with.
		Timestamps: true,
	})

	stream, err := req.Stream(ctx)
	if err != nil {
		ch <- PodLogEvent{Err: err, Done: true}
		return
	}
	defer func() { _ = stream.Close() }()

	buf := make([]byte, 4096)
	for {
		select {
		case <-ctx.Done():
			ch <- PodLogEvent{Done: true}
			return
		default:
		}
		n, err := stream.Read(buf)
		if n > 0 {
			ch <- PodLogEvent{Line: string(buf[:n])}
		}
		if err != nil {
			if err == io.EOF {
				ch <- PodLogEvent{Done: true}
				return
			}
			ch <- PodLogEvent{Err: err, Done: true}
			return
		}
	}
}

func int64Ptr(i int64) *int64 {
	return &i
}

// ListHotPodsByCPU is the cache-aware variant of the package-level function.
// When the informer cache is available, the cluster-wide pod list is served
// from local memory (sub-ms p95 vs ~40ms direct List on a 100-pod fixture);
// metrics-server is always queried directly since metrics aren't cacheable.
// Falls back to the direct-API path when informers failed to sync (timeout,
// RBAC denying Watch on Pods, or no informer set was built).
func (c *Client) ListHotPodsByCPU(ctx context.Context, limit int) ([]PodInfo, error) {
	podLister := c.Informers().Pods()
	if podLister == nil {
		return ListHotPodsByCPU(ctx, c.Clientset, limit)
	}
	if limit <= 0 {
		limit = 20
	}

	// Cache holds every phase; filter to Running in-process to match the
	// direct-API path's server-side FieldSelector. Predicate cost is trivial
	// next to the cached lookup itself.
	cached, err := podLister.List(labels.Everything())
	if err != nil {
		return nil, err
	}
	metrics, _ := ListPodMetrics(ctx, c.Clientset, "")
	if len(metrics) == 0 {
		return nil, nil
	}

	out := make([]PodInfo, 0, len(cached))
	for _, p := range cached {
		if p.Status.Phase != corev1.PodRunning {
			continue
		}
		u, ok := metrics[p.Namespace+"/"+p.Name]
		if !ok {
			continue
		}
		info := podToInfo(*p)
		info.CPU = u.CPU
		info.Memory = u.Memory
		out = append(out, info)
	}

	return rankHotPods(out, limit), nil
}
