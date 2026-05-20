package k8s

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
)

// ── Types ─────────────────────────────────────────────────────────────

// Kind values used as NodeRef.Kind. These are the user-facing kinds the
// Tree section dispatches on.
const (
	KindContextList = "ContextList" // root level — list of kube contexts
	KindContext     = "Context"     // a single cluster — drilling in shows its namespace folders
	KindClusterFold = "ClusterFold" // synthetic row inside a context that drills into NodePools
	KindSystemFold  = "SystemFold"  // synthetic row inside a context that drills into system namespaces
	// KindInfrastructureFold groups everything at the cluster root that
	// isn't a user namespace: Nodes, System namespaces, and the
	// "Configure resources" settings affordance. Tucking them behind a
	// single fold keeps the cluster root view focused on the workload
	// containers (the on-caller's primary navigation surface) without
	// hiding the non-workload affordances from the user — they're one
	// drill away under a clearly labeled category.
	KindInfrastructureFold = "InfrastructureFold"
	KindNamespace          = "Namespace"
	// KindNamespaceFolder is a user namespace surfaced at cluster-root as a
	// drillable folder. Same drill semantics as KindNamespace under SystemFold:
	// pressing Enter lists the workload/Config/Secrets/Network/Storage folds
	// scoped to that namespace. Distinct constant so the renderer can tell
	// "user ns folder at cluster-root" apart from "system ns row under
	// SystemFold" — only the former participates in the cluster-root mix of
	// Cluster + All + ns folders + System.
	KindNamespaceFolder = "NamespaceFolder"
	// KindAllNamespacesFold is the virtual "All" folder at cluster root.
	// Drilling it gives the unified cross-namespace view safina used to show
	// at cluster root (Workloads + Config + Secrets + Network + Storage,
	// spanning every user namespace). On-callers don't have to pick a
	// namespace to see "everything" — this is the fast path.
	KindAllNamespacesFold = "AllNamespacesFold"
	KindApp               = "App" // generic — see AppInfo.Kind for the underlying k8s kind
	KindPod               = "Pod"
	KindContainer         = "Container"
	KindNodePool          = "NodePool"
	KindNode              = "Node"
	KindHPA               = "HPA"
	KindConfigFold        = "ConfigFold"  // synthetic row → ConfigMaps
	KindSecretsFold       = "SecretsFold" // synthetic row → Secrets
	KindNetworkFold       = "NetworkFold" // synthetic row → Services
	KindIngressFold       = "IngressFold" // synthetic row → Ingresses
	KindStorageFold       = "StorageFold" // synthetic row → PVCs
	// KindWorkloadsFold groups Deployments/StatefulSets/DaemonSets/CronJobs/Jobs
	// under a single fold at the cluster root — paired with SystemFold so the
	// root view reads as a uniform set of folds rather than mixing inline rows
	// with synthetic ones. Drilling into it returns the same Apps slice the
	// previous Context-level view rendered directly.
	KindWorkloadsFold = "WorkloadsFold"
	KindConfigMap     = "ConfigMap"
	KindSecret        = "Secret"
	KindService       = "Service"
	KindIngress       = "Ingress"
	KindPVC           = "PVC"
	KindPV            = "PV"
	// KindCustomFold is the per-GVR fold the tree renders for any
	// resource the user enabled in EnabledResources that isn't one of the
	// built-in folds above. Drilling it lists instances via the dynamic
	// client and surfaces them as KindGenericResource rows.
	KindCustomFold = "CustomFold"
	// KindGenericResource is the leaf row for a single instance of a
	// CustomFold's GVR. The renderer shows name + age + best-effort
	// status; the resource's GVR travels on NodeRef.GVR so verbs can
	// route through the dynamic client.
	KindGenericResource = "GenericResource"
)

// Underlying k8s kinds embedded inside an App row.
const (
	AppKindDeployment  = "Deployment"
	AppKindStatefulSet = "StatefulSet"
	AppKindDaemonSet   = "DaemonSet"
	AppKindCronJob     = "CronJob"
	AppKindJob         = "Job"
)

// Health is the worst-of-children health classification surfaced as a glyph
// at every tree level. Lower values are worse so callers can do a min-find.
type Health int

const (
	HealthFailing Health = iota
	HealthDegraded
	HealthInProgress
	HealthHealthy
	HealthUnknown
)

// Glyph returns the single-rune indicator for a Health.
func (h Health) Glyph() string {
	switch h {
	case HealthFailing:
		return "✗"
	case HealthDegraded:
		return "⚠"
	case HealthInProgress:
		// ↻ (clockwise open circle arrow) reads as "spinning/working" in
		// nearly every monospaced font; the previous ⏵ rendered as a tiny
		// triangle that operators kept confusing with random punctuation.
		return "↻"
	case HealthHealthy:
		return "✓"
	default:
		return "…"
	}
}

// Worse returns the worse (smaller) of two healths.
func (h Health) Worse(o Health) Health {
	if o < h {
		return o
	}
	return h
}

// NamespaceInfo carries the classification result for a single namespace.
type NamespaceInfo struct {
	Name   string
	System bool
	Age    string
}

// AppInfo is a row in the Apps level — a unified workload (Deployment,
// StatefulSet, DaemonSet, CronJob, Job) with optional HPA badge.
type AppInfo struct {
	Kind      string // AppKind*
	Name      string
	Namespace string
	UID       types.UID

	Ready  string // "3/3"
	Age    string
	CPU    int64
	Memory int64

	// HPA badge data (zero values when no HPA targets this app).
	HPA *HPABadge

	// CronJob-specific.
	NextSchedule string // human "14m" or empty

	Health Health
}

// HPABadge is the inline HPA summary on an App row.
type HPABadge struct {
	Min         int32
	Max         int32
	Current     int32
	PinnedAtMax bool
	Name        string // for Rollout-history navigation
}

// PodInfo: extended to carry UID and a single owner UID for tree drill.
// (Kept the original public field set so existing consumers compile.)
//
// PodInfo lives in pods.go — we avoid duplicating its struct. To enrich it
// without breaking that file's struct literal at podToInfo, we add fields
// there directly.

// ContainerInfo represents one container within a pod (regular, init, or
// ephemeral). Containers aren't k8s API objects; they're surfaced for the UI
// drill from a multi-container pod.
type ContainerInfo struct {
	PodName    string
	Namespace  string
	Name       string
	Image      string
	Kind       string // "regular" | "init" | "ephemeral"
	State      string // "Running" | "Waiting:Reason" | "Terminated:Reason"
	Ready      bool
	Restarts   int32
	CPU        int64
	Memory     int64
	LastReason string
}

// NodeInfo describes one node row.
type NodeInfo struct {
	Name        string
	Pool        string
	UID         types.UID
	Status      string // "Ready" | "NotReady" | "SchedulingDisabled"
	Roles       string // comma-joined
	Age         string
	CPUUsed     int64 // millicores
	CPUAlloc    int64
	MemUsed     int64 // bytes
	MemAlloc    int64
	CPURequest  int64
	MemRequest  int64
	PodsCurrent int32
	PodsMax     int32
	Schedulable bool
	Health      Health
	// Pressures lists the kubelet-asserted pressure conditions that are
	// currently true on this node: MemoryPressure, DiskPressure, PIDPressure,
	// NetworkUnavailable. Empty for healthy nodes. Surfaced inline as small
	// red glyphs so an on-caller can spot "the kubelet itself says it's
	// hurting" before request/usage math catches up.
	Pressures []string
}

// NodePoolInfo aggregates over the nodes in a pool.
type NodePoolInfo struct {
	Name        string
	Provider    string
	Nodes       int
	CPUUsed     int64
	CPUAlloc    int64
	MemUsed     int64
	MemAlloc    int64
	PodsCurrent int32
	PodsMax     int32
	Health      Health
}

// ConfigMapInfo is a row for a single ConfigMap.
type ConfigMapInfo struct {
	Name      string
	Namespace string
	Keys      int
	Age       string
}

// ServiceInfo is a row for a single Service.
type ServiceInfo struct {
	Name      string
	Namespace string
	Type      string // ClusterIP, NodePort, LoadBalancer
	ClusterIP string
	Ports     string // "80/TCP,443/TCP"
	Age       string
}

// IngressInfo is a row for a single Ingress.
type IngressInfo struct {
	Name      string
	Namespace string
	Hosts     string // "foo.example.com,bar.example.com"
	Class     string // e.g. "nginx"
	Age       string
}

// SecretInfo is a row for a single Secret.
type SecretInfo struct {
	Name      string
	Namespace string
	Type      string // Opaque, kubernetes.io/tls, etc.
	Keys      int
	Age       string
}

// PVCInfo is a row for a single PersistentVolumeClaim.
type PVCInfo struct {
	Name         string
	Namespace    string
	Status       string // Bound, Pending, Lost
	Capacity     string // "10Gi"
	VolumeName   string // bound PV name
	StorageClass string
	Age          string
}

// PVInfo is a row for a single PersistentVolume (cluster-scoped).
type PVInfo struct {
	Name         string
	Capacity     string // "10Gi"
	Status       string // Bound, Available, Released, Failed
	Claim        string // "namespace/name" if bound
	StorageClass string
	AccessModes  string // "RWO,ROX,RWX"
	Reclaim      string // Retain, Delete, Recycle
	Age          string
}

// ── Namespace classification ─────────────────────────────────────────

// systemNamespacePrefixes are the well-known control-plane / addon prefixes.
// A namespace whose name starts with any of these is treated as system.
var systemNamespacePrefixes = []string{
	"kube-",
	"calico-",
	"cilium-",
	"istio-",
	"linkerd-",
	"gke-",
	"cert-manager",
	"ingress-nginx",
	"gatekeeper-system",
	"metallb-system",
	"external-secrets",
	"velero",
	"kyverno",
	"tigera-operator",
	"tekton-",
	"monitoring",
	"logging",
	"gmp-system",
}

var systemNamespaceExact = map[string]bool{
	"kube-system":     true,
	"kube-public":     true,
	"kube-node-lease": true,
}

// IsSystemNamespace classifies a single namespace name using the built-in
// prefix list. The TUI usually goes through ClassifyNamespaces (which honors
// AppConfig overrides); IsSystemNamespace is the prefix-only fallback.
func IsSystemNamespace(name string) bool {
	if systemNamespaceExact[name] {
		return true
	}
	for _, p := range systemNamespacePrefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// ClassifyNamespaces returns (user, system) namespace lists from the given
// raw set. cfgUser / cfgSystem are explicit overrides from AppConfig:
//
//   - If cfgUser is non-empty, it acts as the exact allowlist of user
//     namespaces. Everything else is system.
//   - Else cfgSystem entries are forced into the system bucket; remaining
//     names fall through to the prefix-based IsSystemNamespace check.
//
// Inputs are sorted alphabetically in the output.
func ClassifyNamespaces(all []string, cfgUser, cfgSystem []string) (user, system []NamespaceInfo) {
	cfgUserSet := make(map[string]bool, len(cfgUser))
	for _, n := range cfgUser {
		cfgUserSet[n] = true
	}
	cfgSysSet := make(map[string]bool, len(cfgSystem))
	for _, n := range cfgSystem {
		cfgSysSet[n] = true
	}

	for _, name := range all {
		var sys bool
		switch {
		case len(cfgUserSet) > 0:
			sys = !cfgUserSet[name]
		case cfgSysSet[name]:
			sys = true
		default:
			sys = IsSystemNamespace(name)
		}
		ni := NamespaceInfo{Name: name, System: sys}
		if sys {
			system = append(system, ni)
		} else {
			user = append(user, ni)
		}
	}
	sort.Slice(user, func(i, j int) bool { return user[i].Name < user[j].Name })
	sort.Slice(system, func(i, j int) bool { return system[i].Name < system[j].Name })
	return user, system
}

// ListAndClassifyNamespaces is the convenience wrapper used by the Tree
// section: list all namespaces in the cluster and split them.
func ListAndClassifyNamespaces(ctx context.Context, cs kubernetes.Interface, cfgUser, cfgSystem []string) (user, system []NamespaceInfo, err error) {
	list, err := cs.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, nil, err
	}
	names := make([]string, 0, len(list.Items))
	ageBy := make(map[string]string, len(list.Items))
	for _, n := range list.Items {
		names = append(names, n.Name)
		ageBy[n.Name] = HumanAge(n.CreationTimestamp.Time)
	}
	u, s := ClassifyNamespaces(names, cfgUser, cfgSystem)
	for i := range u {
		u[i].Age = ageBy[u[i].Name]
	}
	for i := range s {
		s[i].Age = ageBy[s[i].Name]
	}
	return u, s, nil
}

// ── Apps (workload-kind unification) ─────────────────────────────────

// ListAppsInNamespaces lists every workload across the given namespaces and
// returns one merged AppInfo slice with HPA badges enriched. Best-effort:
// any individual list error is silently dropped (the surviving lists still
// render). Returns rows sorted by Health (worst first), then by name.
func ListAppsInNamespaces(ctx context.Context, cs kubernetes.Interface, namespaces []string) ([]AppInfo, error) {
	if len(namespaces) == 0 {
		return nil, nil
	}

	var (
		mu      sync.Mutex
		out     []AppInfo
		hpaIdx  = make(map[string]*HPABadge) // key = ns/Kind/Name
		podsAll []corev1.Pod
		metrics map[string]ResourceUsage
	)

	// Pre-pull pods + metrics once cross-namespace ("" namespace lists every
	// namespace the caller can read) so per-app aggregation is a single in-
	// memory join instead of one List per workload.
	var wgEnrich sync.WaitGroup
	wgEnrich.Add(2)
	go func() {
		defer wgEnrich.Done()
		list, err := cs.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
		if err == nil {
			podsAll = list.Items
		}
	}()
	go func() {
		defer wgEnrich.Done()
		metrics, _ = ListPodMetrics(ctx, cs, "")
	}()
	wgEnrich.Wait()

	// Fan out one goroutine per namespace × kind.
	var wg sync.WaitGroup
	for _, ns := range namespaces {
		wg.Add(6)

		go func() {
			defer wg.Done()
			list, err := cs.AppsV1().Deployments(ns).List(ctx, metav1.ListOptions{})
			if err != nil {
				return
			}
			rows := make([]AppInfo, 0, len(list.Items))
			for _, d := range list.Items {
				rows = append(rows, deploymentToApp(d, podsAll, metrics))
			}
			mu.Lock()
			out = append(out, rows...)
			mu.Unlock()
		}()

		go func() {
			defer wg.Done()
			list, err := cs.AppsV1().StatefulSets(ns).List(ctx, metav1.ListOptions{})
			if err != nil {
				return
			}
			rows := make([]AppInfo, 0, len(list.Items))
			for _, s := range list.Items {
				rows = append(rows, statefulSetToApp(s, podsAll, metrics))
			}
			mu.Lock()
			out = append(out, rows...)
			mu.Unlock()
		}()

		go func() {
			defer wg.Done()
			list, err := cs.AppsV1().DaemonSets(ns).List(ctx, metav1.ListOptions{})
			if err != nil {
				return
			}
			rows := make([]AppInfo, 0, len(list.Items))
			for _, d := range list.Items {
				rows = append(rows, daemonSetToApp(d, podsAll, metrics))
			}
			mu.Lock()
			out = append(out, rows...)
			mu.Unlock()
		}()

		go func() {
			defer wg.Done()
			list, err := cs.BatchV1().CronJobs(ns).List(ctx, metav1.ListOptions{})
			if err != nil {
				return
			}
			rows := make([]AppInfo, 0, len(list.Items))
			for _, c := range list.Items {
				rows = append(rows, cronJobToApp(c))
			}
			mu.Lock()
			out = append(out, rows...)
			mu.Unlock()
		}()

		go func() {
			defer wg.Done()
			list, err := cs.BatchV1().Jobs(ns).List(ctx, metav1.ListOptions{})
			if err != nil {
				return
			}
			rows := make([]AppInfo, 0, len(list.Items))
			for _, j := range list.Items {
				// Skip jobs owned by a CronJob — they appear when drilling
				// into the CronJob, not as top-level Apps.
				if jobOwnedByCronJob(j) {
					continue
				}
				rows = append(rows, jobToApp(j, podsAll, metrics))
			}
			mu.Lock()
			out = append(out, rows...)
			mu.Unlock()
		}()

		go func() {
			defer wg.Done()
			badges := listHPABadges(ctx, cs, ns)
			mu.Lock()
			for k, v := range badges {
				hpaIdx[k] = v
			}
			mu.Unlock()
		}()
	}
	wg.Wait()

	// Enrich with HPA badges.
	for i := range out {
		key := out[i].Namespace + "/" + out[i].Kind + "/" + out[i].Name
		if b, ok := hpaIdx[key]; ok {
			out[i].HPA = b
			// Annotate health when pinned at max replicas.
			if b.PinnedAtMax && out[i].Health == HealthHealthy {
				out[i].Health = HealthDegraded
			}
		}
	}

	// Identity-based sort: Namespace, Kind, Name. We deliberately do NOT
	// sort by Health here, even though "failing apps first" is appealing —
	// a row's Health flips frequently during normal cluster operation (pods
	// cycling, deploys rolling), and a Health-first sort would shuffle row
	// positions on every refresh. That moves the selected row out from
	// under the user's cursor on the auto-tick — a real bug they noticed
	// in the Workloads view. Failing rows stay visible via their glyph,
	// foreground color, and the pulse animation; the `F` keybind jumps to
	// the first unhealthy row when the user wants to scan for trouble.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Namespace != out[j].Namespace {
			return out[i].Namespace < out[j].Namespace
		}
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

func deploymentToApp(d appsv1.Deployment, pods []corev1.Pod, metrics map[string]ResourceUsage) AppInfo {
	desired := int32PtrVal(d.Spec.Replicas)
	ready := d.Status.ReadyReplicas
	a := AppInfo{
		Kind:      AppKindDeployment,
		Name:      d.Name,
		Namespace: d.Namespace,
		UID:       d.UID,
		Ready:     fmt.Sprintf("%d/%d", ready, desired),
		Age:       HumanAge(d.CreationTimestamp.Time),
	}
	if d.Spec.Selector != nil {
		a.CPU, a.Memory = aggregateForSelector(d.Namespace, d.Spec.Selector, pods, metrics)
	}
	a.Health = classifyDeploymentHealth(d)
	return a
}

// classifyDeploymentHealth uses the full Deployment status — Conditions plus
// the updated/available/ready replica counts — to disambiguate states that
// the simple (ready, desired) view conflates. In particular: a fresh roll
// out where ready=0 and pods are still spinning up is HealthInProgress, not
// HealthFailing; only a real progress-deadline timeout or replica-failure
// condition lifts the state to Failing.
func classifyDeploymentHealth(d appsv1.Deployment) Health {
	desired := int32PtrVal(d.Spec.Replicas)
	if desired == 0 {
		return HealthUnknown
	}
	available := corev1.ConditionUnknown
	for _, c := range d.Status.Conditions {
		if c.Type == appsv1.DeploymentReplicaFailure && c.Status == corev1.ConditionTrue {
			return HealthFailing
		}
		if c.Type == appsv1.DeploymentProgressing && c.Status == corev1.ConditionFalse {
			// ProgressDeadlineExceeded is the canonical "rollout truly stuck"
			// signal — anything else with Progressing=False is uncommon but
			// equally non-recoverable without operator intervention.
			return HealthFailing
		}
		if c.Type == appsv1.DeploymentAvailable {
			available = c.Status
		}
	}
	ready := d.Status.ReadyReplicas
	// Available=False with zero ready replicas means nothing's serving traffic
	// — the controller has reported it explicitly. Don't sit on HealthInProgress
	// for the full progressDeadlineSeconds (default 10 min) while pods crash-
	// loop; promote to Failing so the row glyph matches the "Unavailable"
	// headline the Inspector already shows.
	if ready == 0 && available == corev1.ConditionFalse {
		return HealthFailing
	}
	if ready == desired && d.Status.UpdatedReplicas == desired && d.Status.Replicas == desired {
		return HealthHealthy
	}
	// Some pods previously ready, now unavailable while others still serve —
	// the user sees an availability dip even though the rollout is moving.
	if d.Status.UnavailableReplicas > 0 && ready > 0 {
		return HealthDegraded
	}
	// Either fresh creation or a rollout in motion.
	return HealthInProgress
}

func statefulSetToApp(s appsv1.StatefulSet, pods []corev1.Pod, metrics map[string]ResourceUsage) AppInfo {
	desired := int32PtrVal(s.Spec.Replicas)
	ready := s.Status.ReadyReplicas
	a := AppInfo{
		Kind:      AppKindStatefulSet,
		Name:      s.Name,
		Namespace: s.Namespace,
		UID:       s.UID,
		Ready:     fmt.Sprintf("%d/%d", ready, desired),
		Age:       HumanAge(s.CreationTimestamp.Time),
	}
	if s.Spec.Selector != nil {
		a.CPU, a.Memory = aggregateForSelector(s.Namespace, s.Spec.Selector, pods, metrics)
	}
	a.Health = classifyWorkloadHealth(ready, desired, false)
	return a
}

func daemonSetToApp(d appsv1.DaemonSet, pods []corev1.Pod, metrics map[string]ResourceUsage) AppInfo {
	desired := d.Status.DesiredNumberScheduled
	ready := d.Status.NumberReady
	a := AppInfo{
		Kind:      AppKindDaemonSet,
		Name:      d.Name,
		Namespace: d.Namespace,
		UID:       d.UID,
		Ready:     fmt.Sprintf("%d/%d", ready, desired),
		Age:       HumanAge(d.CreationTimestamp.Time),
	}
	if d.Spec.Selector != nil {
		a.CPU, a.Memory = aggregateForSelector(d.Namespace, d.Spec.Selector, pods, metrics)
	}
	a.Health = classifyWorkloadHealth(ready, desired, d.Status.NumberUnavailable > 0)
	return a
}

func cronJobToApp(c batchv1.CronJob) AppInfo {
	a := AppInfo{
		Kind:      AppKindCronJob,
		Name:      c.Name,
		Namespace: c.Namespace,
		UID:       c.UID,
		Age:       HumanAge(c.CreationTimestamp.Time),
		Health:    HealthHealthy,
	}
	if c.Spec.Suspend != nil && *c.Spec.Suspend {
		a.Health = HealthDegraded
	}
	if !c.Status.LastScheduleTime.IsZero() {
		// Approximate "next" as schedule + (now - last). We don't parse cron
		// here — that needs a robonext library. For now show "last: …".
		a.NextSchedule = "last " + HumanAge(c.Status.LastScheduleTime.Time)
	}
	return a
}

func jobToApp(j batchv1.Job, pods []corev1.Pod, metrics map[string]ResourceUsage) AppInfo {
	desired := int32PtrVal(j.Spec.Completions)
	if desired == 0 {
		desired = int32PtrVal(j.Spec.Parallelism)
	}
	// Kubernetes defaults Completions to 1 for simple one-off jobs.
	// When both Completions and Parallelism are nil, desired is 0, producing
	// a nonsensical "1/0" display. Default to 1 in that case.
	if desired == 0 {
		desired = 1
	}
	ready := j.Status.Succeeded
	if ready == 0 {
		ready = j.Status.Active
	}
	a := AppInfo{
		Kind:      AppKindJob,
		Name:      j.Name,
		Namespace: j.Namespace,
		UID:       j.UID,
		Ready:     fmt.Sprintf("%d/%d", ready, desired),
		Age:       HumanAge(j.CreationTimestamp.Time),
	}
	if j.Spec.Selector != nil {
		a.CPU, a.Memory = aggregateForSelector(j.Namespace, j.Spec.Selector, pods, metrics)
	}
	switch {
	case j.Status.Failed > 0:
		a.Health = HealthFailing
	case j.Status.Succeeded == desired && desired > 0:
		a.Health = HealthHealthy
	case j.Status.Active > 0:
		a.Health = HealthInProgress
	default:
		a.Health = HealthUnknown
	}
	return a
}

func jobOwnedByCronJob(j batchv1.Job) bool {
	for _, o := range j.OwnerReferences {
		if o.Kind == "CronJob" {
			return true
		}
	}
	return false
}

// classifyWorkloadHealth distills (ready/desired) into a single Health.
func classifyWorkloadHealth(ready, desired int32, unavailable bool) Health {
	if desired == 0 {
		return HealthUnknown
	}
	switch {
	case ready == 0:
		return HealthFailing
	case ready < desired:
		if unavailable {
			return HealthDegraded
		}
		return HealthInProgress
	default:
		return HealthHealthy
	}
}

func aggregateForSelector(namespace string, sel *metav1.LabelSelector, pods []corev1.Pod, metrics map[string]ResourceUsage) (cpu, mem int64) {
	if sel == nil || metrics == nil {
		return 0, 0
	}
	matchLabels := sel.MatchLabels
	for _, p := range pods {
		if p.Namespace != namespace {
			continue
		}
		if !labelsMatch(p.Labels, matchLabels) {
			continue
		}
		if u, ok := metrics[p.Namespace+"/"+p.Name]; ok {
			cpu += u.CPU
			mem += u.Memory
		}
	}
	return cpu, mem
}

func labelsMatch(podLabels, selector map[string]string) bool {
	if len(selector) == 0 {
		return false
	}
	for k, v := range selector {
		if podLabels[k] != v {
			return false
		}
	}
	return true
}

// ── HPAs (badge enrichment) ──────────────────────────────────────────

// listHPABadges returns "ns/Kind/Name" → HPABadge for a namespace, trying v2
// and falling back to v1 silently. Returns empty map on error.
func listHPABadges(ctx context.Context, cs kubernetes.Interface, ns string) map[string]*HPABadge {
	out := make(map[string]*HPABadge)
	v2list, err := cs.AutoscalingV2().HorizontalPodAutoscalers(ns).List(ctx, metav1.ListOptions{})
	if err == nil {
		for _, h := range v2list.Items {
			b := hpaV2ToBadge(h)
			key := h.Namespace + "/" + h.Spec.ScaleTargetRef.Kind + "/" + h.Spec.ScaleTargetRef.Name
			out[key] = b
		}
		return out
	}
	v1list, err := cs.AutoscalingV1().HorizontalPodAutoscalers(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return out
	}
	for _, h := range v1list.Items {
		b := hpaV1ToBadge(h)
		key := h.Namespace + "/" + h.Spec.ScaleTargetRef.Kind + "/" + h.Spec.ScaleTargetRef.Name
		out[key] = b
	}
	return out
}

func hpaV2ToBadge(h autoscalingv2.HorizontalPodAutoscaler) *HPABadge {
	minReplicas := int32(0)
	if h.Spec.MinReplicas != nil {
		minReplicas = *h.Spec.MinReplicas
	}
	return &HPABadge{
		Min:         minReplicas,
		Max:         h.Spec.MaxReplicas,
		Current:     h.Status.CurrentReplicas,
		PinnedAtMax: h.Status.CurrentReplicas == h.Spec.MaxReplicas && h.Spec.MaxReplicas > 0,
		Name:        h.Name,
	}
}

func hpaV1ToBadge(h autoscalingv1.HorizontalPodAutoscaler) *HPABadge {
	minReplicas := int32(0)
	if h.Spec.MinReplicas != nil {
		minReplicas = *h.Spec.MinReplicas
	}
	return &HPABadge{
		Min:         minReplicas,
		Max:         h.Spec.MaxReplicas,
		Current:     h.Status.CurrentReplicas,
		PinnedAtMax: h.Status.CurrentReplicas == h.Spec.MaxReplicas && h.Spec.MaxReplicas > 0,
		Name:        h.Name,
	}
}

// ── Pods for an App (owner-ref by UID) ───────────────────────────────

// ListPodsForApp returns the pods belonging to the given App, traversing
// owner references by UID. For Deployments this is Deploy → ReplicaSet → Pod;
// for StatefulSet/DaemonSet/Job direct; for CronJob: CronJob → Job → Pod.
func ListPodsForApp(ctx context.Context, cs kubernetes.Interface, app AppRef) ([]PodInfo, error) {
	switch app.Kind {
	case AppKindDeployment:
		return podsForDeployment(ctx, cs, app)
	case AppKindStatefulSet, AppKindDaemonSet, AppKindJob:
		return podsByOwnerUID(ctx, cs, app.Namespace, []types.UID{app.UID})
	case AppKindCronJob:
		jobUIDs, err := jobUIDsForCronJob(ctx, cs, app.Namespace, app.UID)
		if err != nil {
			return nil, err
		}
		return podsByOwnerUID(ctx, cs, app.Namespace, jobUIDs)
	}
	return nil, fmt.Errorf("unknown app kind %q", app.Kind)
}

// AppRef is the reference shape ListPodsForApp accepts. Sections build it
// from their AppInfo or NodeRef.
type AppRef struct {
	Kind      string
	Namespace string
	Name      string
	UID       types.UID
}

func podsForDeployment(ctx context.Context, cs kubernetes.Interface, app AppRef) ([]PodInfo, error) {
	rsList, err := cs.AppsV1().ReplicaSets(app.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	rsUIDs := make([]types.UID, 0)
	for _, rs := range rsList.Items {
		for _, o := range rs.OwnerReferences {
			if o.UID == app.UID {
				rsUIDs = append(rsUIDs, rs.UID)
				break
			}
		}
	}
	if len(rsUIDs) == 0 {
		return nil, nil
	}
	return podsByOwnerUID(ctx, cs, app.Namespace, rsUIDs)
}

func jobUIDsForCronJob(ctx context.Context, cs kubernetes.Interface, namespace string, cronJobUID types.UID) ([]types.UID, error) {
	list, err := cs.BatchV1().Jobs(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	out := make([]types.UID, 0)
	for _, j := range list.Items {
		for _, o := range j.OwnerReferences {
			if o.UID == cronJobUID {
				out = append(out, j.UID)
				break
			}
		}
	}
	return out, nil
}

func podsByOwnerUID(ctx context.Context, cs kubernetes.Interface, namespace string, ownerUIDs []types.UID) ([]PodInfo, error) {
	if len(ownerUIDs) == 0 {
		return nil, nil
	}
	allow := make(map[types.UID]bool, len(ownerUIDs))
	for _, u := range ownerUIDs {
		allow[u] = true
	}
	list, err := cs.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	metrics, _ := ListPodMetrics(ctx, cs, namespace)
	out := make([]PodInfo, 0, len(list.Items))
	for _, p := range list.Items {
		matched := false
		for _, o := range p.OwnerReferences {
			if allow[o.UID] {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		info := podToInfo(p)
		if metrics != nil {
			if u, ok := metrics[p.Namespace+"/"+p.Name]; ok {
				info.CPU = u.CPU
				info.Memory = u.Memory
			}
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool {
		if PodHealth(out[i]) != PodHealth(out[j]) {
			return PodHealth(out[i]) < PodHealth(out[j])
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// ── Containers ──────────────────────────────────────────────────────

// ListContainersForPod fetches a single pod and returns its containers
// (regular + init + ephemeral) with state and per-container metrics.
func ListContainersForPod(ctx context.Context, cs kubernetes.Interface, namespace, name string) ([]ContainerInfo, error) {
	p, err := cs.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	byCtr, _ := ListPodMetricsByContainer(ctx, cs, namespace)
	usage := byCtr[namespace+"/"+name]

	out := make([]ContainerInfo, 0, len(p.Spec.Containers)+len(p.Spec.InitContainers)+len(p.Spec.EphemeralContainers))
	statusByName := func(statuses []corev1.ContainerStatus) map[string]corev1.ContainerStatus {
		m := make(map[string]corev1.ContainerStatus, len(statuses))
		for _, s := range statuses {
			m[s.Name] = s
		}
		return m
	}
	regStatus := statusByName(p.Status.ContainerStatuses)
	initStatus := statusByName(p.Status.InitContainerStatuses)

	for _, c := range p.Spec.Containers {
		out = append(out, makeContainerInfo(p, c, "regular", regStatus[c.Name], usage))
	}
	for _, c := range p.Spec.InitContainers {
		out = append(out, makeContainerInfo(p, c, "init", initStatus[c.Name], usage))
	}
	for _, ec := range p.Spec.EphemeralContainers {
		c := corev1.Container{Name: ec.Name, Image: ec.Image}
		out = append(out, makeContainerInfo(p, c, "ephemeral", corev1.ContainerStatus{}, usage))
	}
	return out, nil
}

func makeContainerInfo(p *corev1.Pod, c corev1.Container, kind string, st corev1.ContainerStatus, usage map[string]ResourceUsage) ContainerInfo {
	ci := ContainerInfo{
		PodName:   p.Name,
		Namespace: p.Namespace,
		Name:      c.Name,
		Image:     c.Image,
		Kind:      kind,
	}
	if u, ok := usage[c.Name]; ok {
		ci.CPU = u.CPU
		ci.Memory = u.Memory
	}
	switch {
	case st.State.Running != nil:
		ci.State = "Running"
	case st.State.Waiting != nil:
		ci.State = "Waiting:" + st.State.Waiting.Reason
		ci.LastReason = st.State.Waiting.Reason
	case st.State.Terminated != nil:
		ci.State = "Terminated:" + st.State.Terminated.Reason
		ci.LastReason = st.State.Terminated.Reason
	}
	ci.Ready = st.Ready
	ci.Restarts = st.RestartCount
	if ci.LastReason == "" && st.LastTerminationState.Terminated != nil {
		ci.LastReason = st.LastTerminationState.Terminated.Reason
	}
	return ci
}

// ── Nodes / NodePools ────────────────────────────────────────────────

// nodePoolPriorityLabels is the auto-detection list, in priority order.
var nodePoolPriorityLabels = []string{
	"karpenter.sh/nodepool",
	"cloud.google.com/gke-nodepool",
	"eks.amazonaws.com/nodegroup",
	"agentpool",
}

// ListNodesAndPools returns the cluster's nodes grouped into pools, plus the
// flat node list. Best-effort metrics enrichment.
func ListNodesAndPools(ctx context.Context, cs kubernetes.Interface) ([]NodePoolInfo, []NodeInfo, error) {
	nodes, err := cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, nil, err
	}

	var (
		nodeMetrics map[string]ResourceUsage
		allPods     *corev1.PodList
	)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		nodeMetrics, _ = ListNodeMetrics(ctx, cs)
	}()
	go func() {
		defer wg.Done()
		allPods, _ = cs.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	}()
	wg.Wait()

	// Bucket pods by node for request-aggregation.
	podsByNode := make(map[string][]corev1.Pod)
	if allPods != nil {
		for _, p := range allPods.Items {
			if p.Spec.NodeName == "" {
				continue
			}
			podsByNode[p.Spec.NodeName] = append(podsByNode[p.Spec.NodeName], p)
		}
	}

	infos := make([]NodeInfo, 0, len(nodes.Items))
	for _, n := range nodes.Items {
		ni := nodeToInfo(n, nodeMetrics, podsByNode[n.Name])
		infos = append(infos, ni)
	}

	sort.Slice(infos, func(i, j int) bool {
		if infos[i].Pool != infos[j].Pool {
			return infos[i].Pool < infos[j].Pool
		}
		return infos[i].Name < infos[j].Name
	})

	pools := groupIntoPools(infos)
	return pools, infos, nil
}

func nodeToInfo(n corev1.Node, metrics map[string]ResourceUsage, pods []corev1.Pod) NodeInfo {
	ni := NodeInfo{
		Name:  n.Name,
		UID:   n.UID,
		Pool:  detectNodePool(n),
		Age:   HumanAge(n.CreationTimestamp.Time),
		Roles: nodeRoles(n),
	}

	ni.CPUAlloc = quantityMilli(n.Status.Allocatable, corev1.ResourceCPU)
	ni.MemAlloc = quantityBytes(n.Status.Allocatable, corev1.ResourceMemory)
	if maxPods := quantityValue(n.Status.Allocatable, corev1.ResourcePods); maxPods > 0 {
		ni.PodsMax = int32(maxPods)
	}
	if metrics != nil {
		if u, ok := metrics[n.Name]; ok {
			ni.CPUUsed = u.CPU
			ni.MemUsed = u.Memory
		}
	}

	cpuReq, memReq, podCount := sumPodRequests(pods)
	ni.CPURequest = cpuReq
	ni.MemRequest = memReq
	ni.PodsCurrent = podCount

	ni.Schedulable = !n.Spec.Unschedulable
	ni.Status = nodeReadyStatus(n)
	if !ni.Schedulable {
		ni.Status = "SchedulingDisabled"
	}
	ni.Pressures = nodePressures(n)
	ni.Health = nodeHealth(ni)
	return ni
}

// nodePressures collects the kubelet-asserted pressure conditions that
// are currently True on the node. Order matches the kubelet's own
// reporting (Memory, Disk, PID, Network) so the rendered glyph string
// stays stable across refreshes.
func nodePressures(n corev1.Node) []string {
	if len(n.Status.Conditions) == 0 {
		return nil
	}
	want := []corev1.NodeConditionType{
		corev1.NodeMemoryPressure,
		corev1.NodeDiskPressure,
		corev1.NodePIDPressure,
		corev1.NodeNetworkUnavailable,
	}
	out := make([]string, 0, len(want))
	for _, t := range want {
		for _, c := range n.Status.Conditions {
			if c.Type == t && c.Status == corev1.ConditionTrue {
				out = append(out, string(t))
				break
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func nodeReadyStatus(n corev1.Node) string {
	for _, c := range n.Status.Conditions {
		if c.Type == corev1.NodeReady {
			if c.Status == corev1.ConditionTrue {
				return "Ready"
			}
			return "NotReady"
		}
	}
	return "Unknown"
}

func nodeHealth(n NodeInfo) Health {
	switch {
	case n.Status == "NotReady":
		return HealthFailing
	case len(n.Pressures) > 0:
		// kubelet shouting "I'm in trouble right now" outranks the
		// request-vs-alloc heuristic — if MemoryPressure or DiskPressure
		// is firing, the node is actively evicting or about to.
		return HealthDegraded
	case !n.Schedulable:
		return HealthDegraded
	case n.CPUAlloc > 0 && n.CPURequest > n.CPUAlloc*9/10:
		return HealthDegraded
	case n.MemAlloc > 0 && n.MemRequest > n.MemAlloc*9/10:
		return HealthDegraded
	default:
		return HealthHealthy
	}
}

func nodeRoles(n corev1.Node) string {
	const prefix = "node-role.kubernetes.io/"
	roles := make([]string, 0, 2)
	for k := range n.Labels {
		if strings.HasPrefix(k, prefix) {
			r := strings.TrimPrefix(k, prefix)
			if r != "" {
				roles = append(roles, r)
			}
		}
	}
	sort.Strings(roles)
	return strings.Join(roles, ",")
}

func detectNodePool(n corev1.Node) string {
	for _, lbl := range nodePoolPriorityLabels {
		if v := n.Labels[lbl]; v != "" {
			return v
		}
	}
	if pid := n.Spec.ProviderID; pid != "" {
		switch {
		case strings.HasPrefix(pid, "gce://"):
			return "gce"
		case strings.HasPrefix(pid, "aws://"):
			return "aws"
		case strings.HasPrefix(pid, "azure://"):
			return "azure"
		}
	}
	return "self-managed"
}

func groupIntoPools(nodes []NodeInfo) []NodePoolInfo {
	idx := make(map[string]*NodePoolInfo)
	order := []string{}
	for _, n := range nodes {
		p, ok := idx[n.Pool]
		if !ok {
			p = &NodePoolInfo{Name: n.Pool, Health: HealthHealthy}
			idx[n.Pool] = p
			order = append(order, n.Pool)
		}
		p.Nodes++
		p.CPUUsed += n.CPUUsed
		p.CPUAlloc += n.CPUAlloc
		p.MemUsed += n.MemUsed
		p.MemAlloc += n.MemAlloc
		p.PodsCurrent += n.PodsCurrent
		p.PodsMax += n.PodsMax
		p.Health = p.Health.Worse(n.Health)
	}
	sort.Strings(order)
	out := make([]NodePoolInfo, 0, len(order))
	for _, name := range order {
		out = append(out, *idx[name])
	}
	return out
}

// ClusterHealth is the at-a-glance state of a whole cluster, sized to fit
// in a topbar pill. Populated once per cluster-health tick; consumers read
// the zero value as "no data yet" and hide the module.
type ClusterHealth struct {
	NodesReady    int
	NodesTotal    int
	PodsRunning   int
	PodsPending   int
	PodsCrashing  int
	PodsTotal     int
	CPUUsed       int64 // sum of node CPUUsed, millicores
	CPUAlloc      int64
	MemUsed       int64 // bytes
	MemAlloc      int64
	PressureCount int // total active pressure conditions across all nodes
}

// HasData reports whether the snapshot carries enough to render the pill.
// Used by the topbar to suppress an empty pill on the very first tick.
func (c ClusterHealth) HasData() bool {
	return c.NodesTotal > 0
}

// LoadClusterHealth produces a ClusterHealth snapshot of the cluster: node
// readiness, pod counts by state, summed resource usage vs allocatable,
// and active pressure conditions. Best-effort — metrics-server failure
// degrades gracefully (usage stays zero, the rest of the fields populate).
//
// Sized for a single periodic call from the topbar refresh ticker, NOT
// per-row rendering. The two list calls (nodes + pods, both cluster-scoped)
// are the same the tree itself runs when the user drills into Nodes; we
// pay the extra round-trip once every cluster-health tick instead of every
// 5s tree refresh so users not on the Nodes view still get the pill.
func LoadClusterHealth(ctx context.Context, cs kubernetes.Interface) (ClusterHealth, error) {
	var ch ClusterHealth

	var (
		nodes       *corev1.NodeList
		pods        *corev1.PodList
		nodeMetrics map[string]ResourceUsage
		errNodes    error
		errPods     error
	)
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		nodes, errNodes = cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	}()
	go func() {
		defer wg.Done()
		pods, errPods = cs.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	}()
	go func() {
		defer wg.Done()
		nodeMetrics, _ = ListNodeMetrics(ctx, cs)
	}()
	wg.Wait()

	// Nodes are load-bearing for the cluster pill; pods are nice-to-have.
	// If the node list fails we return the error so the model can keep the
	// previous snapshot rather than reset to all-zero.
	if errNodes != nil {
		return ch, errNodes
	}
	if errPods != nil {
		pods = &corev1.PodList{}
	}

	for i := range nodes.Items {
		ch.addNode(nodes.Items[i], nodeMetrics)
	}
	for i := range pods.Items {
		ch.addPod(pods.Items[i])
	}

	return ch, nil
}

// addNode accumulates one node's contribution to ch. Shared between the
// direct-API path (LoadClusterHealth) and the cache-aware Client method.
func (ch *ClusterHealth) addNode(n corev1.Node, nodeMetrics map[string]ResourceUsage) {
	ch.NodesTotal++
	if nodeReadyStatus(n) == "Ready" && !n.Spec.Unschedulable {
		ch.NodesReady++
	}
	ch.CPUAlloc += quantityMilli(n.Status.Allocatable, corev1.ResourceCPU)
	ch.MemAlloc += quantityBytes(n.Status.Allocatable, corev1.ResourceMemory)
	if nodeMetrics != nil {
		if u, ok := nodeMetrics[n.Name]; ok {
			ch.CPUUsed += u.CPU
			ch.MemUsed += u.Memory
		}
	}
	ch.PressureCount += len(nodePressures(n))
}

// addPod accumulates one pod's contribution to ch. Mirrors addNode's split:
// shared body between direct-API and cache-aware paths.
func (ch *ClusterHealth) addPod(p corev1.Pod) {
	ch.PodsTotal++
	switch p.Status.Phase {
	case corev1.PodRunning:
		ch.PodsRunning++
	case corev1.PodPending:
		ch.PodsPending++
	default:
		// PodSucceeded/PodFailed/PodUnknown — counted as Total but not Running/Pending.
	}
	// CrashLoopBackOff lives at the container-status level, not pod phase.
	// A pod that is Running but has a container in CLB is the classic
	// "looks healthy from the outside, screaming on the inside" case the
	// on-caller wants to see.
	for _, cs := range p.Status.ContainerStatuses {
		if cs.State.Waiting != nil && cs.State.Waiting.Reason == "CrashLoopBackOff" {
			ch.PodsCrashing++
			break
		}
	}
}

// LoadClusterHealth is the cache-aware variant of the package-level function.
// When the informer cache is available the pod + node lists are served from
// local memory; metrics-server is always queried directly. Falls back to the
// direct-API path when informers failed to sync (timeout, RBAC, or no set
// was built).
func (c *Client) LoadClusterHealth(ctx context.Context) (ClusterHealth, error) {
	if c.informers == nil || c.informers.podLister == nil || c.informers.nodeLister == nil {
		return LoadClusterHealth(ctx, c.Clientset)
	}
	var ch ClusterHealth

	// Metrics-server stays on the direct path (no Watch support); fire it in
	// parallel with the cache lookups so its latency doesn't add to ours.
	var (
		nodeMetrics map[string]ResourceUsage
		wg          sync.WaitGroup
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		nodeMetrics, _ = ListNodeMetrics(ctx, c.Clientset)
	}()

	nodes, err := c.informers.nodeLister.List(labels.Everything())
	if err != nil {
		wg.Wait()
		return ch, err
	}
	pods, err := c.informers.podLister.List(labels.Everything())
	if err != nil {
		wg.Wait()
		return ch, err
	}
	wg.Wait()

	for _, n := range nodes {
		ch.addNode(*n, nodeMetrics)
	}
	for _, p := range pods {
		ch.addPod(*p)
	}
	return ch, nil
}

// ListPodsOnNode fetches pods scheduled on the given node, cross-namespace.
// Uses the indexed `spec.nodeName` field selector.
func ListPodsOnNode(ctx context.Context, cs kubernetes.Interface, nodeName string) ([]PodInfo, error) {
	list, err := cs.CoreV1().Pods("").List(ctx, metav1.ListOptions{
		FieldSelector: "spec.nodeName=" + nodeName,
	})
	if err != nil {
		return nil, err
	}
	metrics, _ := ListPodMetrics(ctx, cs, "")
	out := make([]PodInfo, 0, len(list.Items))
	for _, p := range list.Items {
		info := podToInfo(p)
		if metrics != nil {
			if u, ok := metrics[p.Namespace+"/"+p.Name]; ok {
				info.CPU = u.CPU
				info.Memory = u.Memory
			}
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Namespace != out[j].Namespace {
			return out[i].Namespace < out[j].Namespace
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// ── Node "Available" math ────────────────────────────────────────────

// sumPodRequests computes the requested CPU/memory across the given pods,
// honoring init-container/sidecar semantics and pod overhead. Pods that are
// truly terminating (DeletionTimestamp set AND phase Failed/Succeeded) are
// skipped — they no longer hold their slot.
func sumPodRequests(pods []corev1.Pod) (cpu, mem int64, count int32) {
	for _, p := range pods {
		if p.DeletionTimestamp != nil &&
			(p.Status.Phase == corev1.PodFailed || p.Status.Phase == corev1.PodSucceeded) {
			continue
		}
		c, m := podEffectiveRequests(p)
		cpu += c
		mem += m
		count++
	}
	return cpu, mem, count
}

// podEffectiveRequests returns the request resources the scheduler accounts
// for. Computed as max(sum(containers), max(initContainers)) per kubelet
// scheduling rules, plus pod.spec.overhead if set, plus all native sidecar
// init containers (k8s ≥1.29 with restartPolicy=Always count as regular).
func podEffectiveRequests(p corev1.Pod) (cpu, mem int64) {
	var regC, regM int64
	for _, c := range p.Spec.Containers {
		regC += quantityMilli(c.Resources.Requests, corev1.ResourceCPU)
		regM += quantityBytes(c.Resources.Requests, corev1.ResourceMemory)
	}
	// Native sidecars (initContainers with restartPolicy=Always) count as
	// regular containers for resource accounting.
	var maxInitC, maxInitM int64
	for _, c := range p.Spec.InitContainers {
		isSidecar := c.RestartPolicy != nil && *c.RestartPolicy == corev1.ContainerRestartPolicyAlways
		ic := quantityMilli(c.Resources.Requests, corev1.ResourceCPU)
		im := quantityBytes(c.Resources.Requests, corev1.ResourceMemory)
		if isSidecar {
			regC += ic
			regM += im
			continue
		}
		if ic > maxInitC {
			maxInitC = ic
		}
		if im > maxInitM {
			maxInitM = im
		}
	}
	cpu = regC
	mem = regM
	if maxInitC > cpu {
		cpu = maxInitC
	}
	if maxInitM > mem {
		mem = maxInitM
	}
	if p.Spec.Overhead != nil {
		cpu += quantityMilli(p.Spec.Overhead, corev1.ResourceCPU)
		mem += quantityBytes(p.Spec.Overhead, corev1.ResourceMemory)
	}
	return cpu, mem
}

// quantityMilli reads a resource quantity as millicores.
func quantityMilli(rl corev1.ResourceList, name corev1.ResourceName) int64 {
	if q, ok := rl[name]; ok {
		return q.MilliValue()
	}
	return 0
}

// quantityBytes reads a resource quantity as bytes.
func quantityBytes(rl corev1.ResourceList, name corev1.ResourceName) int64 {
	if q, ok := rl[name]; ok {
		return q.Value()
	}
	return 0
}

func quantityValue(rl corev1.ResourceList, name corev1.ResourceName) int64 {
	if q, ok := rl[name]; ok {
		return q.Value()
	}
	return 0
}

// ── Pod-level helpers needed by the new tree section ─────────────────

// ContainerHealth classifies a container by its current State string.
func ContainerHealth(ci ContainerInfo) Health {
	switch {
	case ci.State == "Running":
		return HealthHealthy
	case ci.State == "Terminated:Completed":
		return HealthHealthy
	case strings.HasPrefix(ci.State, "Waiting:CrashLoopBackOff"),
		strings.HasPrefix(ci.State, "Waiting:ImagePullBackOff"),
		strings.HasPrefix(ci.State, "Waiting:ErrImagePull"),
		strings.HasPrefix(ci.State, "Terminated:OOMKilled"),
		strings.HasPrefix(ci.State, "Terminated:Error"):
		return HealthFailing
	case strings.HasPrefix(ci.State, "Waiting:ContainerCreating"),
		strings.HasPrefix(ci.State, "Waiting:PodInitializing"):
		return HealthInProgress
	}
	return HealthUnknown
}

// PodHealth classifies a pod (using its info) into a Health bucket.
func PodHealth(p PodInfo) Health {
	switch p.Status {
	case "Running":
		if p.Restarts >= 10 {
			return HealthDegraded
		}
		return HealthHealthy
	case "Succeeded":
		return HealthHealthy
	case "Pending", "ContainerCreating":
		return HealthInProgress
	case "Terminating":
		return HealthInProgress
	case "Failed", "CrashLoopBackOff", "ImagePullBackOff", "ErrImagePull", "Error":
		return HealthFailing
	}
	return HealthUnknown
}
