package k8s

import (
	"context"
	"fmt"
	"sort"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// EventInfo is a flattened, UI-friendly view of a corev1.Event. Unlike the raw
// event we expose only what an SRE actually scans for: when, what kind,
// why, what happened, and how often.
type EventInfo struct {
	Age      string
	Type     string // Normal | Warning
	Reason   string
	Message  string
	Count    int32
	LastSeen time.Time
}

// ListEvents returns the events for a single involved object (kind + name)
// in a namespace, newest first. kind is the Kubernetes Kind ("Pod",
// "Deployment", "ReplicaSet", …) — must match the API server's casing.
//
// Note: events for a Deployment do NOT include events for its child
// ReplicaSets or Pods. Callers that need the full picture (kubectl describe
// style) should aggregate those separately.
func ListEvents(ctx context.Context, cs kubernetes.Interface, namespace, kind, name string) ([]EventInfo, error) {
	fs := fmt.Sprintf("involvedObject.kind=%s,involvedObject.name=%s", kind, name)
	list, err := cs.CoreV1().Events(namespace).List(ctx, metav1.ListOptions{
		FieldSelector: fs,
	})
	if err != nil {
		return nil, err
	}
	events := make([]EventInfo, 0, len(list.Items))
	for _, e := range list.Items {
		last := e.LastTimestamp.Time
		if last.IsZero() {
			last = e.EventTime.Time
		}
		if last.IsZero() {
			last = e.CreationTimestamp.Time
		}
		events = append(events, EventInfo{
			Age:      HumanAge(last),
			Type:     e.Type,
			Reason:   e.Reason,
			Message:  e.Message,
			Count:    e.Count,
			LastSeen: last,
		})
	}
	sort.Slice(events, func(i, j int) bool {
		return events[i].LastSeen.After(events[j].LastSeen)
	})
	return events, nil
}
