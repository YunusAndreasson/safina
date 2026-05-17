package k8s

import (
	"context"
	"fmt"
	"sort"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// Detail structs returned by the Inspect* family. Each is a small,
// UI-friendly snapshot of "the fields an on-caller actually wants to see
// behind the row" — image lists, conditions, scheduling reasons, taints.
// We deliberately don't bridge the full API object: it would force the
// UI to know about k8s API types and would blow up the inspector tile
// with fields nobody scans for.

// PodDetail is the deep-fetch view of a single Pod, surfaced under the
// pod row in the Inspector tile.
type PodDetail struct {
	PodIP      string
	HostIP     string
	QoS        string // BestEffort | Burstable | Guaranteed
	Containers []ContainerSnap
	// Aggregate requests / limits across all containers. The list row
	// already shows live CPU/MEM usage as a spark + number — this is
	// the *configured* sister stat ("how big are you allowed to get?")
	// that operators reference for capacity / HPA-tuning conversations.
	CPURequestMilli int64
	CPULimitMilli   int64
	MemRequestBytes int64
	MemLimitBytes   int64
	// OwnerKind / OwnerName: the controller that "owns" this pod
	// (Deployment via ReplicaSet, StatefulSet, DaemonSet, Job…). Empty
	// for orphan pods. Useful so the operator knows whether killing the
	// pod will be recovered automatically.
	OwnerKind string
	OwnerName string
	// SchedulingReason explains *why* a pod is Pending (FailedScheduling,
	// Unschedulable, etc.) by reading the most recent PodScheduled or
	// ContainersReady condition. Empty when the pod is already running.
	SchedulingReason string
	// Reason is the pod-level reason (Evicted, NodeLost, …). Distinct
	// from the container-level reasons the row's status text shows.
	Reason string
}

// ContainerSnap is one container's headline detail — image string, ready
// flag, last termination reason. The inspector renders one row per
// container so an operator can see at a glance which one is
// CrashLoopBackOff.
type ContainerSnap struct {
	Name       string
	Image      string
	Ready      bool
	State      string // "Running" | "Waiting:Reason" | "Terminated:Reason"
	Restarts   int32
	LastReason string
}

// AppDetail is the deep-fetch view of an "App" — covers Deployment,
// StatefulSet, DaemonSet, CronJob, Job. Only the fields populated for
// the actual kind get rendered; the rest stay zero-value and the
// inspector skips them.
type AppDetail struct {
	Kind   string
	Images []string // unique image strings across containers
	// Aggregate per-replica requests/limits across pod-template
	// containers. Same conceptual sister stat to the row's live CPU/MEM
	// columns: "how big is one replica configured to be?".
	CPURequestMilli int64
	CPULimitMilli   int64
	MemRequestBytes int64
	MemLimitBytes   int64

	// Deployment / StatefulSet / DaemonSet
	Strategy        string // RollingUpdate | Recreate | OnDelete
	Selector        string // "app=foo,team=x"
	Generation      int64
	ObservedGen     int64
	UpdatedReplicas int32
	ReadyReplicas   int32
	DesiredReplicas int32
	Conditions      []AppCondition

	// CronJob
	Schedule     string
	Suspended    bool
	LastSchedule string // HumanAge of the last successful schedule
	NextSchedule string // HumanAge until next scheduled run (if computable)

	// Job
	JobActive    int32
	JobSucceeded int32
	JobFailed    int32
}

// AppCondition is a one-row condition surfaced as "Type · Status · Reason".
// We collapse k8s's conditions because operators care about the headline,
// not the per-condition probe details.
type AppCondition struct {
	Type    string
	Status  string // True | False | Unknown
	Reason  string
	Message string
}

// NodeDetail is the deep-fetch view of a Node — kernel / OS / runtime
// versions an operator needs for compat questions, taints (the answer to
// "why won't anything schedule here?"), and the pressure conditions.
type NodeDetail struct {
	KernelVersion           string
	OSImage                 string
	ContainerRuntimeVersion string
	KubeletVersion          string
	Architecture            string
	Taints                  []TaintSnap
	Conditions              []NodeCondition // only abnormal ones (not "Ready" if Ready)
}

// TaintSnap is one taint formatted as kubectl prints it: "key=value:effect".
type TaintSnap struct {
	Key    string
	Value  string
	Effect string // NoSchedule | PreferNoSchedule | NoExecute
}

// NodeCondition is a node-level health condition (DiskPressure,
// MemoryPressure, PIDPressure, NetworkUnavailable). We only surface
// True-state non-Ready conditions in the inspector — they're the
// actionable signal.
type NodeCondition struct {
	Type   string
	Reason string
}

// ServiceDetail is the deep-fetch view of a Service — selector (so the
// operator can match "what pods does this point at?"), endpoint count
// (live readiness), and external traffic policy bits a row can't carry.
type ServiceDetail struct {
	Selector              string
	EndpointCount         int // total addresses across all endpoint slices
	ReadyEndpointCount    int // ready addresses only
	SessionAffinity       string
	ExternalTrafficPolicy string
	ExternalName          string
	HeadlessClusterIP     bool // true when ClusterIP == "None"
	LoadBalancerIngress   []string
}

// IngressDetail is the deep-fetch view of an Ingress — TLS host count
// and rule count, the answer to "is this HTTPS terminated here?".
type IngressDetail struct {
	TLSHosts  []string
	Rules     int
	IngressIP []string // status.loadBalancer.ingress entries
}

// ── Inspect* loaders ─────────────────────────────────────────────────

// InspectPod fetches one pod and projects it into a PodDetail.
func InspectPod(ctx context.Context, cs kubernetes.Interface, namespace, name string) (PodDetail, error) {
	p, err := cs.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return PodDetail{}, classifyDetailErr(err, "pod", name)
	}
	d := PodDetail{
		PodIP:  p.Status.PodIP,
		HostIP: p.Status.HostIP,
		QoS:    string(p.Status.QOSClass),
		Reason: p.Status.Reason,
	}
	d.Containers = collectContainerSnaps(p)
	if owner := primaryOwner(p.OwnerReferences); owner != nil {
		d.OwnerKind = owner.Kind
		d.OwnerName = owner.Name
	}
	d.SchedulingReason = schedulingReason(p)
	d.CPURequestMilli, d.CPULimitMilli, d.MemRequestBytes, d.MemLimitBytes = sumResources(p.Spec)
	return d, nil
}

// InspectApp dispatches across Deployment / StatefulSet / DaemonSet /
// CronJob / Job. `kind` is the AppKind* constant the tree already carries
// on AppInfo.Kind.
func InspectApp(ctx context.Context, cs kubernetes.Interface, kind, namespace, name string) (AppDetail, error) {
	switch kind {
	case AppKindDeployment:
		return inspectDeployment(ctx, cs, namespace, name)
	case AppKindStatefulSet:
		return inspectStatefulSet(ctx, cs, namespace, name)
	case AppKindDaemonSet:
		return inspectDaemonSet(ctx, cs, namespace, name)
	case AppKindCronJob:
		return inspectCronJob(ctx, cs, namespace, name)
	case AppKindJob:
		return inspectJob(ctx, cs, namespace, name)
	}
	return AppDetail{}, fmt.Errorf("unsupported app kind %q", kind)
}

func inspectDeployment(ctx context.Context, cs kubernetes.Interface, namespace, name string) (AppDetail, error) {
	d, err := cs.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return AppDetail{}, classifyDetailErr(err, "deployment", name)
	}
	out := AppDetail{
		Kind:            AppKindDeployment,
		Images:          uniqueImages(d.Spec.Template.Spec),
		Strategy:        string(d.Spec.Strategy.Type),
		Selector:        formatLabelSelector(d.Spec.Selector),
		Generation:      d.Generation,
		ObservedGen:     d.Status.ObservedGeneration,
		UpdatedReplicas: d.Status.UpdatedReplicas,
		ReadyReplicas:   d.Status.ReadyReplicas,
	}
	if d.Spec.Replicas != nil {
		out.DesiredReplicas = *d.Spec.Replicas
	}
	out.Conditions = mapDeploymentConditions(d.Status.Conditions)
	out.CPURequestMilli, out.CPULimitMilli, out.MemRequestBytes, out.MemLimitBytes = sumResources(d.Spec.Template.Spec)
	return out, nil
}

func inspectStatefulSet(ctx context.Context, cs kubernetes.Interface, namespace, name string) (AppDetail, error) {
	s, err := cs.AppsV1().StatefulSets(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return AppDetail{}, classifyDetailErr(err, "statefulset", name)
	}
	out := AppDetail{
		Kind:            AppKindStatefulSet,
		Images:          uniqueImages(s.Spec.Template.Spec),
		Strategy:        string(s.Spec.UpdateStrategy.Type),
		Selector:        formatLabelSelector(s.Spec.Selector),
		Generation:      s.Generation,
		ObservedGen:     s.Status.ObservedGeneration,
		UpdatedReplicas: s.Status.UpdatedReplicas,
		ReadyReplicas:   s.Status.ReadyReplicas,
	}
	if s.Spec.Replicas != nil {
		out.DesiredReplicas = *s.Spec.Replicas
	}
	out.CPURequestMilli, out.CPULimitMilli, out.MemRequestBytes, out.MemLimitBytes = sumResources(s.Spec.Template.Spec)
	return out, nil
}

func inspectDaemonSet(ctx context.Context, cs kubernetes.Interface, namespace, name string) (AppDetail, error) {
	ds, err := cs.AppsV1().DaemonSets(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return AppDetail{}, classifyDetailErr(err, "daemonset", name)
	}
	out := AppDetail{
		Kind:            AppKindDaemonSet,
		Images:          uniqueImages(ds.Spec.Template.Spec),
		Strategy:        string(ds.Spec.UpdateStrategy.Type),
		Selector:        formatLabelSelector(ds.Spec.Selector),
		Generation:      ds.Generation,
		ObservedGen:     ds.Status.ObservedGeneration,
		UpdatedReplicas: ds.Status.UpdatedNumberScheduled,
		ReadyReplicas:   ds.Status.NumberReady,
		DesiredReplicas: ds.Status.DesiredNumberScheduled,
	}
	out.CPURequestMilli, out.CPULimitMilli, out.MemRequestBytes, out.MemLimitBytes = sumResources(ds.Spec.Template.Spec)
	return out, nil
}

func inspectCronJob(ctx context.Context, cs kubernetes.Interface, namespace, name string) (AppDetail, error) {
	cj, err := cs.BatchV1().CronJobs(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return AppDetail{}, classifyDetailErr(err, "cronjob", name)
	}
	out := AppDetail{
		Kind:     AppKindCronJob,
		Images:   uniqueImages(cj.Spec.JobTemplate.Spec.Template.Spec),
		Schedule: cj.Spec.Schedule,
	}
	if cj.Spec.Suspend != nil {
		out.Suspended = *cj.Spec.Suspend
	}
	if cj.Status.LastScheduleTime != nil {
		out.LastSchedule = HumanAge(cj.Status.LastScheduleTime.Time)
	}
	out.CPURequestMilli, out.CPULimitMilli, out.MemRequestBytes, out.MemLimitBytes = sumResources(cj.Spec.JobTemplate.Spec.Template.Spec)
	return out, nil
}

func inspectJob(ctx context.Context, cs kubernetes.Interface, namespace, name string) (AppDetail, error) {
	j, err := cs.BatchV1().Jobs(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return AppDetail{}, classifyDetailErr(err, "job", name)
	}
	out := AppDetail{
		Kind:         AppKindJob,
		Images:       uniqueImages(j.Spec.Template.Spec),
		Selector:     formatLabelSelector(j.Spec.Selector),
		JobActive:    j.Status.Active,
		JobSucceeded: j.Status.Succeeded,
		JobFailed:    j.Status.Failed,
	}
	out.CPURequestMilli, out.CPULimitMilli, out.MemRequestBytes, out.MemLimitBytes = sumResources(j.Spec.Template.Spec)
	return out, nil
}

// InspectNode fetches one node and projects it into a NodeDetail.
func InspectNode(ctx context.Context, cs kubernetes.Interface, name string) (NodeDetail, error) {
	n, err := cs.CoreV1().Nodes().Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return NodeDetail{}, classifyDetailErr(err, "node", name)
	}
	out := NodeDetail{
		KernelVersion:           n.Status.NodeInfo.KernelVersion,
		OSImage:                 n.Status.NodeInfo.OSImage,
		ContainerRuntimeVersion: n.Status.NodeInfo.ContainerRuntimeVersion,
		KubeletVersion:          n.Status.NodeInfo.KubeletVersion,
		Architecture:            n.Status.NodeInfo.Architecture,
	}
	for _, t := range n.Spec.Taints {
		out.Taints = append(out.Taints, TaintSnap{
			Key:    t.Key,
			Value:  t.Value,
			Effect: string(t.Effect),
		})
	}
	// Surface non-Ready conditions whose Status is True — these are the
	// actionable ones (DiskPressure, MemoryPressure, etc.). Ready itself
	// is covered by the row's status column, so we exclude it.
	for _, c := range n.Status.Conditions {
		if c.Type == corev1.NodeReady {
			continue
		}
		if c.Status == corev1.ConditionTrue {
			out.Conditions = append(out.Conditions, NodeCondition{
				Type:   string(c.Type),
				Reason: c.Reason,
			})
		}
	}
	return out, nil
}

// InspectService fetches one service + its endpoints subset and projects
// the result into a ServiceDetail. Endpoints come from the legacy
// endpoints API (still the cheapest single call) — modern EndpointSlices
// would require N requests per service.
func InspectService(ctx context.Context, cs kubernetes.Interface, namespace, name string) (ServiceDetail, error) {
	s, err := cs.CoreV1().Services(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return ServiceDetail{}, classifyDetailErr(err, "service", name)
	}
	out := ServiceDetail{
		Selector:              formatStringMap(s.Spec.Selector),
		SessionAffinity:       string(s.Spec.SessionAffinity),
		ExternalTrafficPolicy: string(s.Spec.ExternalTrafficPolicy),
		ExternalName:          s.Spec.ExternalName,
		HeadlessClusterIP:     s.Spec.ClusterIP == "None",
	}
	for _, lb := range s.Status.LoadBalancer.Ingress {
		if lb.IP != "" {
			out.LoadBalancerIngress = append(out.LoadBalancerIngress, lb.IP)
		} else if lb.Hostname != "" {
			out.LoadBalancerIngress = append(out.LoadBalancerIngress, lb.Hostname)
		}
	}

	// EndpointSlices: the modern way (the legacy Endpoints API is
	// deprecated in k8s 1.33+). Slices for a given service carry a
	// well-known label `kubernetes.io/service-name=<svc>`. A missing /
	// empty slice list is normal for headless / external-name services
	// — we silently treat that as "zero endpoints" rather than an error.
	slices, err := cs.DiscoveryV1().EndpointSlices(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: discoveryv1.LabelServiceName + "=" + name,
	})
	if err == nil {
		for _, slice := range slices.Items {
			for _, ep := range slice.Endpoints {
				out.EndpointCount++
				if ep.Conditions.Ready != nil && *ep.Conditions.Ready {
					out.ReadyEndpointCount++
				}
			}
		}
	}
	return out, nil
}

// InspectIngress fetches one ingress and projects it into an IngressDetail.
func InspectIngress(ctx context.Context, cs kubernetes.Interface, namespace, name string) (IngressDetail, error) {
	ing, err := cs.NetworkingV1().Ingresses(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return IngressDetail{}, classifyDetailErr(err, "ingress", name)
	}
	out := IngressDetail{Rules: len(ing.Spec.Rules)}
	for _, t := range ing.Spec.TLS {
		out.TLSHosts = append(out.TLSHosts, t.Hosts...)
	}
	for _, lb := range ing.Status.LoadBalancer.Ingress {
		if lb.IP != "" {
			out.IngressIP = append(out.IngressIP, lb.IP)
		} else if lb.Hostname != "" {
			out.IngressIP = append(out.IngressIP, lb.Hostname)
		}
	}
	return out, nil
}

// ── helpers ──────────────────────────────────────────────────────────

func collectContainerSnaps(p *corev1.Pod) []ContainerSnap {
	out := make([]ContainerSnap, 0, len(p.Spec.Containers))
	// Map status by name so we can stitch images (from spec) with state
	// (from status) without an O(N×M) walk per container.
	statusByName := map[string]corev1.ContainerStatus{}
	for _, cs := range p.Status.ContainerStatuses {
		statusByName[cs.Name] = cs
	}
	for _, c := range p.Spec.Containers {
		snap := ContainerSnap{Name: c.Name, Image: c.Image}
		if st, ok := statusByName[c.Name]; ok {
			snap.Ready = st.Ready
			snap.Restarts = st.RestartCount
			snap.State, snap.LastReason = containerStateString(st)
		}
		out = append(out, snap)
	}
	return out
}

func containerStateString(s corev1.ContainerStatus) (state, lastReason string) {
	switch {
	case s.State.Running != nil:
		state = "Running"
	case s.State.Waiting != nil:
		state = "Waiting:" + s.State.Waiting.Reason
	case s.State.Terminated != nil:
		state = "Terminated:" + s.State.Terminated.Reason
	}
	if s.LastTerminationState.Terminated != nil {
		lastReason = s.LastTerminationState.Terminated.Reason
	}
	return state, lastReason
}

func primaryOwner(refs []metav1.OwnerReference) *metav1.OwnerReference {
	// Prefer the controller=true ref. If none is marked controller (rare),
	// the first ref is good enough — it's the one kubectl prints in
	// describe output, and there's no semantic for "secondary owner" the
	// UI can usefully surface.
	for i := range refs {
		if refs[i].Controller != nil && *refs[i].Controller {
			return &refs[i]
		}
	}
	if len(refs) > 0 {
		return &refs[0]
	}
	return nil
}

func schedulingReason(p *corev1.Pod) string {
	if p.Status.Phase != corev1.PodPending {
		return ""
	}
	for _, c := range p.Status.Conditions {
		if c.Type == corev1.PodScheduled && c.Status == corev1.ConditionFalse {
			if c.Reason != "" {
				if c.Message != "" {
					return c.Reason + ": " + c.Message
				}
				return c.Reason
			}
		}
	}
	return ""
}

// sumResources aggregates requests/limits across the regular containers
// of a PodSpec. Init containers are excluded — their resources reserve
// during startup but don't count toward the steady-state footprint
// operators reason about.
func sumResources(pod corev1.PodSpec) (cpuReq, cpuLim, memReq, memLim int64) {
	for _, c := range pod.Containers {
		if r := c.Resources.Requests.Cpu(); r != nil {
			cpuReq += r.MilliValue()
		}
		if l := c.Resources.Limits.Cpu(); l != nil {
			cpuLim += l.MilliValue()
		}
		if r := c.Resources.Requests.Memory(); r != nil {
			memReq += r.Value()
		}
		if l := c.Resources.Limits.Memory(); l != nil {
			memLim += l.Value()
		}
	}
	return cpuReq, cpuLim, memReq, memLim
}

func uniqueImages(pod corev1.PodSpec) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, c := range pod.InitContainers {
		if c.Image != "" && !seen[c.Image] {
			seen[c.Image] = true
			out = append(out, c.Image)
		}
	}
	for _, c := range pod.Containers {
		if c.Image != "" && !seen[c.Image] {
			seen[c.Image] = true
			out = append(out, c.Image)
		}
	}
	return out
}

func mapDeploymentConditions(cs []appsv1.DeploymentCondition) []AppCondition {
	out := make([]AppCondition, 0, len(cs))
	for _, c := range cs {
		out = append(out, AppCondition{
			Type:    string(c.Type),
			Status:  string(c.Status),
			Reason:  c.Reason,
			Message: c.Message,
		})
	}
	// Sort so the rendered order is stable across refreshes — the API
	// orders by transition time, which would shuffle visually on every
	// rollout step.
	sort.Slice(out, func(i, j int) bool { return out[i].Type < out[j].Type })
	return out
}

func formatLabelSelector(sel *metav1.LabelSelector) string {
	if sel == nil {
		return ""
	}
	return formatStringMap(sel.MatchLabels)
}

func formatStringMap(m map[string]string) string {
	if len(m) == 0 {
		return ""
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+m[k])
	}
	return strings.Join(parts, ",")
}

// classifyDetailErr maps a k8s API error to a human-friendly message
// the Inspector tile can render without the operator parsing a stack of
// "internal error 500" boilerplate.
func classifyDetailErr(err error, kind, name string) error {
	if apierrors.IsNotFound(err) {
		return fmt.Errorf("%s %q not found", kind, name)
	}
	if apierrors.IsForbidden(err) {
		return fmt.Errorf("forbidden: cannot read %s %q", kind, name)
	}
	return err
}

// _ keep referenced types used so future struct-only callers compile.
var (
	_ = batchv1.CronJob{}
	_ = networkingv1.Ingress{}
)
