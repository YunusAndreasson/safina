package k8s

import (
	"fmt"
	"hash/fnv"
	"sort"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/types"

	"github.com/YunusAndreasson/safina/internal/metrics"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	resourcehelper "k8s.io/component-helpers/resource"
)

func podMetricBudget(p *corev1.Pod, u ResourceUsage) ResourceUsage {
	req := resourcehelper.PodRequests(p, resourcehelper.PodResourcesOptions{})
	lim := resourcehelper.PodLimits(p, resourcehelper.PodResourcesOptions{})
	u.CPURequest, u.MemRequest = req.Cpu().MilliValue(), req.Memory().Value()
	u.CPULimit, u.MemLimit = lim.Cpu().MilliValue(), lim.Memory().Value()
	// A sum of known container limits is not a pod ceiling if one container
	// is unbounded. Include restartable init containers in this completeness check.
	for _, c := range append(append([]corev1.Container(nil), p.Spec.Containers...), p.Spec.InitContainers...) {
		if c.Resources.Limits.Cpu().IsZero() {
			u.CPULimit = 0
		}
		if c.Resources.Limits.Memory().IsZero() {
			u.MemLimit = 0
		}
	}
	if len(p.Spec.EphemeralContainers) > 0 {
		u.CPULimit = 0
		u.MemLimit = 0
	}

	// Pod-level budgets, when configured, govern the whole pod.
	if p.Spec.Resources != nil {
		if q, ok := p.Spec.Resources.Limits[corev1.ResourceCPU]; ok {
			u.CPULimit = q.MilliValue()
		}
		if q, ok := p.Spec.Resources.Limits[corev1.ResourceMemory]; ok {
			u.MemLimit = q.Value()
		}
	}

	expected := make([]string, 0, len(p.Spec.Containers)+len(p.Spec.InitContainers))
	for _, c := range p.Spec.Containers {
		expected = append(expected, c.Name)
	}
	for _, c := range p.Spec.InitContainers {
		if c.RestartPolicy != nil && *c.RestartPolicy == corev1.ContainerRestartPolicyAlways {
			expected = append(expected, c.Name)
		} else {
			for _, status := range p.Status.InitContainerStatuses {
				if status.Name == c.Name && status.State.Running != nil {
					expected = append(expected, c.Name)
					break
				}
			}
		}
	}
	for _, status := range p.Status.EphemeralContainerStatuses {
		if status.State.Running != nil {
			expected = append(expected, status.Name)
		}
	}
	u.Expected, u.Covered = len(expected), 0
	for _, name := range expected {
		if strings.Contains(u.ContainerNames, "/"+name+"/") {
			u.Covered++
		}
	}
	if u.Valid() && (u.Covered < u.Expected || u.Expected == 0) {
		u.State = metrics.Partial
	}

	return u
}

func aggregateMetrics(namespace string, sel *metav1.LabelSelector, pods []corev1.Pod, values map[types.NamespacedName]ResourceUsage) ResourceUsage {
	u := ResourceUsage{State: metrics.Unavailable}
	if sel == nil {
		return u
	}
	selector, err := metav1.LabelSelectorAsSelector(sel)
	if err != nil {
		return u
	}
	completeCPU, completeMem := true, true
	var fingerprints []string
	now := time.Now()
	for i := range pods {
		p := &pods[i]
		if p.Namespace != namespace || !selector.Matches(labels.Set(p.Labels)) {
			continue
		}
		if p.Status.Phase == corev1.PodSucceeded || p.Status.Phase == corev1.PodFailed {
			continue
		}
		u.Expected++
		r, ok := values[types.NamespacedName{Namespace: p.Namespace, Name: p.Name}]
		b := podMetricBudget(p, r)
		u.CPURequest += b.CPURequest
		u.MemRequest += b.MemRequest
		u.CPULimit += b.CPULimit
		u.MemLimit += b.MemLimit
		completeCPU = completeCPU && b.CPULimit > 0
		completeMem = completeMem && b.MemLimit > 0
		if !ok {
			continue
		}
		// Preserve numeric totals for existing list callers; coverage determines
		// whether the total may enter a chart.
		u.CPU += r.CPU
		u.Memory += r.Memory
		if !b.Valid() || b.Stale(now) {
			continue
		}
		u.Covered++
		if u.SourceAt.IsZero() || r.SourceAt.Before(u.SourceAt) {
			u.SourceAt = r.SourceAt
		}
		u.Window = max(u.Window, r.Window)
		fingerprints = append(fingerprints, fmt.Sprintf("%s/%s/%d", p.UID, p.Name, r.SourceAt.UnixNano()))
	}
	if !completeCPU {
		u.CPULimit = 0
	}
	if !completeMem {
		u.MemLimit = 0
	}
	if u.Expected > 0 && u.Covered == u.Expected {
		u.State = metrics.Available
	} else if u.Covered > 0 {
		u.State = metrics.Partial
	}
	sort.Strings(fingerprints)
	h := fnv.New64a()
	for _, part := range fingerprints {
		_, _ = fmt.Fprintln(h, part)
	}
	u.Fingerprint = h.Sum64()
	return u
}
