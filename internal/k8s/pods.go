package k8s

import (
	"context"
	"io"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

func DeletePod(ctx context.Context, cs kubernetes.Interface, namespace, name string) error {
	return cs.CoreV1().Pods(namespace).Delete(ctx, name, metav1.DeleteOptions{})
}

type PodLogEvent struct {
	Line string
	Err  error
	Done bool
}

func StreamPodLogs(ctx context.Context, cs kubernetes.Interface, namespace, name string, previous bool, ch chan<- PodLogEvent) {
	StreamPodContainerLogs(ctx, cs, namespace, name, "", previous, ch)
}

// StreamPodContainerLogs is the variant that targets a specific container
// inside a multi-container pod. Pass container == "" to let kubelet pick
// the default (first) container.
func StreamPodContainerLogs(ctx context.Context, cs kubernetes.Interface, namespace, name, container string, previous bool, ch chan<- PodLogEvent) {
	defer close(ch)

	req := cs.CoreV1().Pods(namespace).GetLogs(name, &corev1.PodLogOptions{
		Container: container,
		Follow:    true,
		Previous:  previous,
		TailLines: int64Ptr(500),
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
