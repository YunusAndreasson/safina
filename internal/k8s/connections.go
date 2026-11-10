package k8s

import (
	"context"
	"sort"
	"sync"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// ConnectedRef is one entry in the Connected tile — a single reference from
// the focused resource to (or from) another resource. Reason answers "why is
// this connected?" so the operator sees both the target and the relationship
// at a glance: `Service · checkout-api  (selector)`.
type ConnectedRef struct {
	Kind      string // k8s.Kind*
	SubKind   string // for KindApp, the AppKind* the row carries; "" otherwise
	Namespace string
	Name      string
	Reason    string // "selector", "envFrom", "volume", "imagePullSecret",
	// "serviceAccount", "ownerRef", "ingress backend",
	// "scaleTargetRef", "env" — the path that made this a connection
}

// Connections is the kind-erased bag the Connected section renders. Uses are
// forward references (this resource points at these); UsedBy are reverse
// references (these resources point at this).
type Connections struct {
	Uses   []ConnectedRef
	UsedBy []ConnectedRef
}

// ConnectionsForWorkload resolves the connections graph for a top-level
// workload (Deployment / StatefulSet / DaemonSet / Job / CronJob). All
// references except HPA flow from the pod template's spec; HPAs are reverse-
// looked up because they reference the workload.
func ConnectionsForWorkload(ctx context.Context, cs kubernetes.Interface, kind, namespace, name string) (Connections, error) {
	spec, templateLabels, _, err := workloadPodSpec(ctx, cs, kind, namespace, name)
	if err != nil {
		return Connections{}, err
	}

	out := Connections{}
	out.Uses = append(out.Uses, podSpecRefs(spec, namespace)...)

	// Services: any Service in the namespace whose selector is a subset of
	// the workload's pod-template labels (i.e. it would select these pods).
	svcs, _ := cs.CoreV1().Services(namespace).List(ctx, metav1.ListOptions{})
	matchedSvcNames := make(map[string]bool)
	if svcs != nil {
		for _, s := range svcs.Items {
			if labelsMatch(templateLabels, s.Spec.Selector) {
				out.Uses = append(out.Uses, ConnectedRef{
					Kind: KindService, Namespace: s.Namespace, Name: s.Name, Reason: "selector",
				})
				matchedSvcNames[s.Name] = true
			}
		}
	}

	// Ingresses: any Ingress in the namespace whose backend points at one of
	// the Services we just matched. We don't surface unrelated Ingresses.
	if len(matchedSvcNames) > 0 {
		ings, _ := cs.NetworkingV1().Ingresses(namespace).List(ctx, metav1.ListOptions{})
		if ings != nil {
			for _, ing := range ings.Items {
				if ingressTargetsAnyService(ing, matchedSvcNames) {
					out.Uses = append(out.Uses, ConnectedRef{
						Kind: KindIngress, Namespace: ing.Namespace, Name: ing.Name, Reason: "ingress backend",
					})
				}
			}
		}
	}

	// HPA: reverse — find an HPA whose scaleTargetRef points at us.
	out.UsedBy = append(out.UsedBy, hpasTargeting(ctx, cs, namespace, workloadAPIKind(kind), name)...)

	// Selector itself: workloads use their own selector to bind pods. We
	// don't surface "Pods" in Uses because the tree already lists them as
	// children — duplicating that here would be noise.

	sortRefs(out.Uses)
	sortRefs(out.UsedBy)
	return out, nil
}

// ConnectionsForPod resolves a pod's references. Forward: ConfigMaps/Secrets/
// PVCs/SA from spec; reverse: services that select this pod's labels.
func ConnectionsForPod(ctx context.Context, cs kubernetes.Interface, namespace, name string) (Connections, error) {
	p, err := cs.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return Connections{}, classifyDetailErr(err, "pod", name)
	}
	var services []*corev1.Service
	if svcs, _ := cs.CoreV1().Services(namespace).List(ctx, metav1.ListOptions{}); svcs != nil {
		services = make([]*corev1.Service, len(svcs.Items))
		for i := range svcs.Items {
			services[i] = &svcs.Items[i]
		}
	}
	return podConnections(p, services), nil
}

// podConnections builds a pod's references from the pod and the services of
// its namespace, wherever those came from.
func podConnections(p *corev1.Pod, services []*corev1.Service) Connections {
	out := Connections{}
	if owner := primaryOwner(p.OwnerReferences); owner != nil {
		out.Uses = append(out.Uses, ConnectedRef{
			Kind: ownerKindToTreeKind(owner.Kind), SubKind: appKindFromOwner(owner.Kind),
			Namespace: p.Namespace, Name: owner.Name, Reason: "ownerRef",
		})
	}
	out.Uses = append(out.Uses, podSpecRefs(p.Spec, p.Namespace)...)

	// Services selecting this pod's labels — reverse direction.
	for _, s := range services {
		if labelsMatch(p.Labels, s.Spec.Selector) {
			out.UsedBy = append(out.UsedBy, ConnectedRef{
				Kind: KindService, Namespace: s.Namespace, Name: s.Name, Reason: "selector",
			})
		}
	}

	sortRefs(out.Uses)
	sortRefs(out.UsedBy)
	return out
}

// ConnectionsForService resolves Ingresses routing to a service (forward) and
// the workloads it selects (reverse — they "use" the service to be reached).
func ConnectionsForService(ctx context.Context, cs kubernetes.Interface, namespace, name string) (Connections, error) {
	s, err := cs.CoreV1().Services(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return Connections{}, classifyDetailErr(err, "service", name)
	}

	out := Connections{}

	// Ingresses routing to this service: forward direction (this is what we
	// "expose through").
	ings, _ := cs.NetworkingV1().Ingresses(namespace).List(ctx, metav1.ListOptions{})
	if ings != nil {
		nameSet := map[string]bool{name: true}
		for _, ing := range ings.Items {
			if ingressTargetsAnyService(ing, nameSet) {
				out.Uses = append(out.Uses, ConnectedRef{
					Kind: KindIngress, Namespace: ing.Namespace, Name: ing.Name, Reason: "ingress backend",
				})
			}
		}
	}

	// Workloads selected by this service: reverse direction — these are the
	// things that get traffic through the service.
	if len(s.Spec.Selector) > 0 {
		out.UsedBy = append(out.UsedBy, workloadsWithLabels(ctx, cs, namespace, s.Spec.Selector, "selector")...)
	}

	sortRefs(out.Uses)
	sortRefs(out.UsedBy)
	return out, nil
}

// ConnectionsForIngress resolves the backend Services for an ingress
// (forward). No reverse — nothing "owns" or "references" an ingress.
func ConnectionsForIngress(ctx context.Context, cs kubernetes.Interface, namespace, name string) (Connections, error) {
	ing, err := cs.NetworkingV1().Ingresses(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return Connections{}, classifyDetailErr(err, "ingress", name)
	}
	seen := map[string]bool{}
	out := Connections{}
	add := func(svc string) {
		if svc == "" || seen[svc] {
			return
		}
		seen[svc] = true
		out.Uses = append(out.Uses, ConnectedRef{
			Kind: KindService, Namespace: namespace, Name: svc, Reason: "ingress backend",
		})
	}
	if ing.Spec.DefaultBackend != nil && ing.Spec.DefaultBackend.Service != nil {
		add(ing.Spec.DefaultBackend.Service.Name)
	}
	for _, r := range ing.Spec.Rules {
		if r.HTTP == nil {
			continue
		}
		for _, p := range r.HTTP.Paths {
			if p.Backend.Service != nil {
				add(p.Backend.Service.Name)
			}
		}
	}
	sortRefs(out.Uses)
	return out, nil
}

// ConnectionsForConfigMap returns the workloads in the same namespace that
// reference this ConfigMap. Reverse-direction only.
func ConnectionsForConfigMap(ctx context.Context, cs kubernetes.Interface, namespace, name string) (Connections, error) {
	out := Connections{}
	out.UsedBy = scanWorkloadsForRef(ctx, cs, namespace, refMatcherConfigMap(name))
	sortRefs(out.UsedBy)
	return out, nil
}

// ConnectionsForSecret returns the workloads referencing this Secret via
// envFrom, env.valueFrom, volumes, or imagePullSecrets. Reverse-only.
func ConnectionsForSecret(ctx context.Context, cs kubernetes.Interface, namespace, name string) (Connections, error) {
	out := Connections{}
	out.UsedBy = scanWorkloadsForRef(ctx, cs, namespace, refMatcherSecret(name))
	sortRefs(out.UsedBy)
	return out, nil
}

// ConnectionsForServiceAccount returns workloads with
// `serviceAccountName: <name>` in the same namespace.
func ConnectionsForServiceAccount(ctx context.Context, cs kubernetes.Interface, namespace, name string) (Connections, error) {
	out := Connections{}
	out.UsedBy = scanWorkloadsForRef(ctx, cs, namespace, func(p corev1.PodSpec) (string, bool) {
		if p.ServiceAccountName == name {
			return "serviceAccount", true
		}
		return "", false
	})
	sortRefs(out.UsedBy)
	return out, nil
}

// ConnectionsForHPA returns the workload an HPA scales (forward) — there is
// no useful reverse direction.
func ConnectionsForHPA(ctx context.Context, cs kubernetes.Interface, namespace, name string) (Connections, error) {
	// Prefer v2 (kubectl autoscale defaults here on modern clusters) with v1
	// fallback for older clusters or restricted RBAC.
	out := Connections{}
	if h, err := cs.AutoscalingV2().HorizontalPodAutoscalers(namespace).Get(ctx, name, metav1.GetOptions{}); err == nil {
		ref := h.Spec.ScaleTargetRef
		out.Uses = append(out.Uses, ConnectedRef{
			Kind: KindApp, SubKind: ref.Kind, Namespace: namespace, Name: ref.Name, Reason: "scaleTargetRef",
		})
		return out, nil
	}
	if h, err := cs.AutoscalingV1().HorizontalPodAutoscalers(namespace).Get(ctx, name, metav1.GetOptions{}); err == nil {
		ref := h.Spec.ScaleTargetRef
		out.Uses = append(out.Uses, ConnectedRef{
			Kind: KindApp, SubKind: ref.Kind, Namespace: namespace, Name: ref.Name, Reason: "scaleTargetRef",
		})
		return out, nil
	}
	return out, nil
}

// ── helpers ──────────────────────────────────────────────────────────

// workloadPodSpec fetches the pod-template spec, the pod-template labels,
// and the workload's selector for the given workload kind. Used as the
// forward-reference source for all workload resolvers.
func workloadPodSpec(ctx context.Context, cs kubernetes.Interface, kind, namespace, name string) (corev1.PodSpec, map[string]string, *metav1.LabelSelector, error) {
	switch kind {
	case AppKindDeployment:
		d, err := cs.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return corev1.PodSpec{}, nil, nil, classifyDetailErr(err, "deployment", name)
		}
		return d.Spec.Template.Spec, d.Spec.Template.Labels, d.Spec.Selector, nil
	case AppKindStatefulSet:
		s, err := cs.AppsV1().StatefulSets(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return corev1.PodSpec{}, nil, nil, classifyDetailErr(err, "statefulset", name)
		}
		return s.Spec.Template.Spec, s.Spec.Template.Labels, s.Spec.Selector, nil
	case AppKindDaemonSet:
		ds, err := cs.AppsV1().DaemonSets(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return corev1.PodSpec{}, nil, nil, classifyDetailErr(err, "daemonset", name)
		}
		return ds.Spec.Template.Spec, ds.Spec.Template.Labels, ds.Spec.Selector, nil
	case AppKindJob:
		j, err := cs.BatchV1().Jobs(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return corev1.PodSpec{}, nil, nil, classifyDetailErr(err, "job", name)
		}
		return j.Spec.Template.Spec, j.Spec.Template.Labels, j.Spec.Selector, nil
	case AppKindCronJob:
		cj, err := cs.BatchV1().CronJobs(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return corev1.PodSpec{}, nil, nil, classifyDetailErr(err, "cronjob", name)
		}
		t := cj.Spec.JobTemplate.Spec.Template
		return t.Spec, t.Labels, cj.Spec.JobTemplate.Spec.Selector, nil
	}
	return corev1.PodSpec{}, nil, nil, nil
}

// ingressTargetsAnyService returns true when any path's backend (or the
// default backend) references one of the named services in this namespace.
func ingressTargetsAnyService(ing networkingv1.Ingress, names map[string]bool) bool {
	if ing.Spec.DefaultBackend != nil &&
		ing.Spec.DefaultBackend.Service != nil &&
		names[ing.Spec.DefaultBackend.Service.Name] {
		return true
	}
	for _, r := range ing.Spec.Rules {
		if r.HTTP == nil {
			continue
		}
		for _, p := range r.HTTP.Paths {
			if p.Backend.Service != nil && names[p.Backend.Service.Name] {
				return true
			}
		}
	}
	return false
}

// podSpecRefs extracts the forward references from a pod-template's spec —
// ConfigMaps (envFrom + env.valueFrom + volumes), Secrets (envFrom +
// env.valueFrom + volumes + imagePullSecrets), PVCs (volumes), ServiceAccount.
// Deduplicated per (kind,name) — referencing the same ConfigMap from envFrom
// AND a volume should only emit one row, with the first reason found.
func podSpecRefs(p corev1.PodSpec, namespace string) []ConnectedRef {
	var refs []ConnectedRef
	seen := make(map[string]bool) // key = kind|name → dedupes

	add := func(kind, name, reason string) {
		if name == "" {
			return
		}
		key := kind + "|" + name
		if seen[key] {
			return
		}
		seen[key] = true
		refs = append(refs, ConnectedRef{Kind: kind, Namespace: namespace, Name: name, Reason: reason})
	}

	// ServiceAccount
	if p.ServiceAccountName != "" && p.ServiceAccountName != "default" {
		// We surface the default SA too only when it's the only thing we'd
		// show? No — the user expects to see "what SA am I using" regardless,
		// but `default` is the implicit value for every pod that didn't
		// override, so showing it as a connection is noise. Skip "default".
		add(KindServiceAccount, p.ServiceAccountName, "serviceAccount")
	}

	// imagePullSecrets → Secrets
	for _, ips := range p.ImagePullSecrets {
		add(KindSecret, ips.Name, "imagePullSecret")
	}

	scan := func(containers []corev1.Container) {
		for _, c := range containers {
			for _, ef := range c.EnvFrom {
				if ef.ConfigMapRef != nil {
					add(KindConfigMap, ef.ConfigMapRef.Name, "envFrom")
				}
				if ef.SecretRef != nil {
					add(KindSecret, ef.SecretRef.Name, "envFrom")
				}
			}
			for _, e := range c.Env {
				if e.ValueFrom == nil {
					continue
				}
				if e.ValueFrom.ConfigMapKeyRef != nil {
					add(KindConfigMap, e.ValueFrom.ConfigMapKeyRef.Name, "env")
				}
				if e.ValueFrom.SecretKeyRef != nil {
					add(KindSecret, e.ValueFrom.SecretKeyRef.Name, "env")
				}
			}
		}
	}
	scan(p.InitContainers)
	scan(p.Containers)

	// Volumes → ConfigMap, Secret, PVC
	for _, v := range p.Volumes {
		switch {
		case v.ConfigMap != nil:
			add(KindConfigMap, v.ConfigMap.Name, "volume")
		case v.Secret != nil:
			add(KindSecret, v.Secret.SecretName, "volume")
		case v.PersistentVolumeClaim != nil:
			add(KindPVC, v.PersistentVolumeClaim.ClaimName, "volume")
		case v.Projected != nil:
			for _, src := range v.Projected.Sources {
				if src.ConfigMap != nil {
					add(KindConfigMap, src.ConfigMap.Name, "projected volume")
				}
				if src.Secret != nil {
					add(KindSecret, src.Secret.Name, "projected volume")
				}
			}
		}
	}
	return refs
}

// refMatcher returns ("reason", true) when the spec references the target;
// scanWorkloadsForRef invokes it once per workload and collects matches.
type refMatcher func(p corev1.PodSpec) (string, bool)

// refMatcherConfigMap returns a matcher that fires when any container's
// envFrom/env or any volume references the named ConfigMap.
func refMatcherConfigMap(name string) refMatcher {
	return func(p corev1.PodSpec) (string, bool) {
		check := func(containers []corev1.Container) (string, bool) {
			for _, c := range containers {
				for _, ef := range c.EnvFrom {
					if ef.ConfigMapRef != nil && ef.ConfigMapRef.Name == name {
						return "envFrom", true
					}
				}
				for _, e := range c.Env {
					if e.ValueFrom != nil && e.ValueFrom.ConfigMapKeyRef != nil &&
						e.ValueFrom.ConfigMapKeyRef.Name == name {
						return "env", true
					}
				}
			}
			return "", false
		}
		if reason, ok := check(p.InitContainers); ok {
			return reason, true
		}
		if reason, ok := check(p.Containers); ok {
			return reason, true
		}
		for _, v := range p.Volumes {
			if v.ConfigMap != nil && v.ConfigMap.Name == name {
				return "volume", true
			}
			if v.Projected != nil {
				for _, src := range v.Projected.Sources {
					if src.ConfigMap != nil && src.ConfigMap.Name == name {
						return "projected volume", true
					}
				}
			}
		}
		return "", false
	}
}

// refMatcherSecret returns a matcher that fires on any of the four Secret-
// reference paths in a pod spec.
func refMatcherSecret(name string) refMatcher {
	return func(p corev1.PodSpec) (string, bool) {
		for _, ips := range p.ImagePullSecrets {
			if ips.Name == name {
				return "imagePullSecret", true
			}
		}
		check := func(containers []corev1.Container) (string, bool) {
			for _, c := range containers {
				for _, ef := range c.EnvFrom {
					if ef.SecretRef != nil && ef.SecretRef.Name == name {
						return "envFrom", true
					}
				}
				for _, e := range c.Env {
					if e.ValueFrom != nil && e.ValueFrom.SecretKeyRef != nil &&
						e.ValueFrom.SecretKeyRef.Name == name {
						return "env", true
					}
				}
			}
			return "", false
		}
		if reason, ok := check(p.InitContainers); ok {
			return reason, true
		}
		if reason, ok := check(p.Containers); ok {
			return reason, true
		}
		for _, v := range p.Volumes {
			if v.Secret != nil && v.Secret.SecretName == name {
				return "volume", true
			}
			if v.Projected != nil {
				for _, src := range v.Projected.Sources {
					if src.Secret != nil && src.Secret.Name == name {
						return "projected volume", true
					}
				}
			}
		}
		return "", false
	}
}

// scanWorkloadsForRef walks every workload kind in the namespace in parallel
// and returns ConnectedRefs for those whose pod template the matcher matches.
// Used by reverse-direction resolvers (ConfigMap/Secret/SA → who uses me).
func scanWorkloadsForRef(ctx context.Context, cs kubernetes.Interface, namespace string, match refMatcher) []ConnectedRef {
	var (
		mu  sync.Mutex
		out []ConnectedRef
		wg  sync.WaitGroup
	)
	add := func(kind, name, reason string) {
		mu.Lock()
		defer mu.Unlock()
		out = append(out, ConnectedRef{
			Kind: KindApp, SubKind: kind, Namespace: namespace, Name: name, Reason: reason,
		})
	}

	wg.Add(5)
	go func() {
		defer wg.Done()
		list, err := cs.AppsV1().Deployments(namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			return
		}
		for _, d := range list.Items {
			if reason, ok := match(d.Spec.Template.Spec); ok {
				add(AppKindDeployment, d.Name, reason)
			}
		}
	}()
	go func() {
		defer wg.Done()
		list, err := cs.AppsV1().StatefulSets(namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			return
		}
		for _, s := range list.Items {
			if reason, ok := match(s.Spec.Template.Spec); ok {
				add(AppKindStatefulSet, s.Name, reason)
			}
		}
	}()
	go func() {
		defer wg.Done()
		list, err := cs.AppsV1().DaemonSets(namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			return
		}
		for _, d := range list.Items {
			if reason, ok := match(d.Spec.Template.Spec); ok {
				add(AppKindDaemonSet, d.Name, reason)
			}
		}
	}()
	go func() {
		defer wg.Done()
		list, err := cs.BatchV1().CronJobs(namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			return
		}
		for _, cj := range list.Items {
			if reason, ok := match(cj.Spec.JobTemplate.Spec.Template.Spec); ok {
				add(AppKindCronJob, cj.Name, reason)
			}
		}
	}()
	go func() {
		defer wg.Done()
		list, err := cs.BatchV1().Jobs(namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			return
		}
		for _, j := range list.Items {
			// Skip Jobs owned by a CronJob — they appear under the CronJob,
			// not as independent workloads. Matches the tree's own filtering.
			if jobOwnedByCronJob(j) {
				continue
			}
			if reason, ok := match(j.Spec.Template.Spec); ok {
				add(AppKindJob, j.Name, reason)
			}
		}
	}()
	wg.Wait()
	return out
}

// workloadsWithLabels returns workloads in the namespace whose pod-template
// labels are a superset of the given selector. Used by Service connection
// resolution to surface "what does this Service select?"
func workloadsWithLabels(ctx context.Context, cs kubernetes.Interface, namespace string, sel map[string]string, reason string) []ConnectedRef {
	if len(sel) == 0 {
		return nil
	}
	var (
		mu  sync.Mutex
		out []ConnectedRef
		wg  sync.WaitGroup
	)
	add := func(kind, name string) {
		mu.Lock()
		defer mu.Unlock()
		out = append(out, ConnectedRef{
			Kind: KindApp, SubKind: kind, Namespace: namespace, Name: name, Reason: reason,
		})
	}

	wg.Add(3) // Services typically front Deployments / StatefulSets / DaemonSets
	go func() {
		defer wg.Done()
		list, err := cs.AppsV1().Deployments(namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			return
		}
		for _, d := range list.Items {
			if labelsMatch(d.Spec.Template.Labels, sel) {
				add(AppKindDeployment, d.Name)
			}
		}
	}()
	go func() {
		defer wg.Done()
		list, err := cs.AppsV1().StatefulSets(namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			return
		}
		for _, s := range list.Items {
			if labelsMatch(s.Spec.Template.Labels, sel) {
				add(AppKindStatefulSet, s.Name)
			}
		}
	}()
	go func() {
		defer wg.Done()
		list, err := cs.AppsV1().DaemonSets(namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			return
		}
		for _, ds := range list.Items {
			if labelsMatch(ds.Spec.Template.Labels, sel) {
				add(AppKindDaemonSet, ds.Name)
			}
		}
	}()
	wg.Wait()
	return out
}

// hpasTargeting returns the HPA(s) whose scaleTargetRef points at the named
// workload. Almost always 0 or 1 — HPAs are 1-to-1 in practice — but the
// shape is a list so a misconfigured 2-HPAs-on-one-Deployment cluster
// surfaces both rather than silently hiding the duplicate.
func hpasTargeting(ctx context.Context, cs kubernetes.Interface, namespace, kind, name string) []ConnectedRef {
	out := []ConnectedRef{}
	if list, err := cs.AutoscalingV2().HorizontalPodAutoscalers(namespace).List(ctx, metav1.ListOptions{}); err == nil {
		for _, h := range list.Items {
			if h.Spec.ScaleTargetRef.Kind == kind && h.Spec.ScaleTargetRef.Name == name {
				out = append(out, ConnectedRef{
					Kind: KindHPA, Namespace: h.Namespace, Name: h.Name, Reason: "scaleTargetRef",
				})
			}
		}
		return out
	}
	if list, err := cs.AutoscalingV1().HorizontalPodAutoscalers(namespace).List(ctx, metav1.ListOptions{}); err == nil {
		for _, h := range list.Items {
			if h.Spec.ScaleTargetRef.Kind == kind && h.Spec.ScaleTargetRef.Name == name {
				out = append(out, ConnectedRef{
					Kind: KindHPA, Namespace: h.Namespace, Name: h.Name, Reason: "scaleTargetRef",
				})
			}
		}
	}
	return out
}

// workloadAPIKind translates the safina internal AppKind* (which matches the
// k8s API Kind string already) into what HPA.scaleTargetRef expects. Today
// they're the same; the wrapper exists so a future divergence doesn't ripple.
func workloadAPIKind(appKind string) string { return appKind }

// ownerKindToTreeKind maps a k8s OwnerReference.Kind to the tree's NodeRef
// kind. Most workloads land on KindApp; ReplicaSet is its own thing in k8s
// but operators care about the parent Deployment — we still surface the
// ReplicaSet here (Pod → ReplicaSet → Deployment is a real chain) so the
// user can see the intermediary.
func ownerKindToTreeKind(k string) string {
	switch k {
	case "ReplicaSet":
		return "ReplicaSet"
	case "Deployment", "StatefulSet", "DaemonSet", "Job", "CronJob":
		return KindApp
	case "Node":
		return KindNode
	}
	return k
}

// appKindFromOwner returns the AppKind* string for an owner kind that maps
// to a tree App row; "" for anything else.
func appKindFromOwner(k string) string {
	switch k {
	case "Deployment":
		return AppKindDeployment
	case "StatefulSet":
		return AppKindStatefulSet
	case "DaemonSet":
		return AppKindDaemonSet
	case "Job":
		return AppKindJob
	case "CronJob":
		return AppKindCronJob
	}
	return ""
}

// sortRefs orders rows so the same kind clusters, with stable name order
// inside. Inspector consumers can render straight from the list.
func sortRefs(refs []ConnectedRef) {
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].Kind != refs[j].Kind {
			return refs[i].Kind < refs[j].Kind
		}
		if refs[i].Namespace != refs[j].Namespace {
			return refs[i].Namespace < refs[j].Namespace
		}
		return refs[i].Name < refs[j].Name
	})
}

// KindServiceAccount is the user-facing kind for a ServiceAccount row. We
// don't surface SA rows in the tree today, but the Connected tile names them
// so the user can see "this workload runs as <SA>". Sits alongside the
// existing Kind* constants in tree.go.
const KindServiceAccount = "ServiceAccount"
