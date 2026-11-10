package k8s

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
)

// ConnectionsForConfigMap reuses the active cluster's workload caches for
// reverse references, falling back to the API until all required kinds sync.
func (c *Client) ConnectionsForConfigMap(ctx context.Context, namespace, name string) (Connections, error) {
	return c.connectionsForRef(ctx, namespace, refMatcherConfigMap(name)), nil
}

// ConnectionsForSecret resolves reverse references without fetching secret data.
func (c *Client) ConnectionsForSecret(ctx context.Context, namespace, name string) (Connections, error) {
	return c.connectionsForRef(ctx, namespace, refMatcherSecret(name)), nil
}

// ConnectionsForServiceAccount finds workloads using this service account.
func (c *Client) ConnectionsForServiceAccount(ctx context.Context, namespace, name string) (Connections, error) {
	return c.connectionsForRef(ctx, namespace, func(p corev1.PodSpec) (string, bool) {
		if p.ServiceAccountName == name {
			return "serviceAccount", true
		}
		return "", false
	}), nil
}

func (c *Client) connectionsForRef(ctx context.Context, namespace string, match refMatcher) Connections {
	refs, ok := c.cachedWorkloadRefs(namespace, match)
	if !ok {
		refs = scanWorkloadsForRef(ctx, c.Clientset, namespace, match)
	}
	sortRefs(refs)
	return Connections{UsedBy: refs}
}

func (c *Client) cachedWorkloadRefs(namespace string, match refMatcher) ([]ConnectedRef, bool) {
	// These panels identify a namespaced resource. Keep the direct helper's
	// all-namespace behavior for callers without a namespace.
	if namespace == "" {
		return nil, false
	}
	inf := c.Informers()
	dep, sts, ds := inf.Deployments(), inf.StatefulSets(), inf.DaemonSets()
	cj, jobs := inf.CronJobs(), inf.Jobs()
	if dep == nil || sts == nil || ds == nil || cj == nil || jobs == nil {
		return nil, false
	}
	selector := labels.Everything()
	deployments, err := dep.Deployments(namespace).List(selector)
	if err != nil {
		return nil, false
	}
	statefulsets, err := sts.StatefulSets(namespace).List(selector)
	if err != nil {
		return nil, false
	}
	daemonsets, err := ds.DaemonSets(namespace).List(selector)
	if err != nil {
		return nil, false
	}
	cronjobs, err := cj.CronJobs(namespace).List(selector)
	if err != nil {
		return nil, false
	}
	jobList, err := jobs.Jobs(namespace).List(selector)
	if err != nil {
		return nil, false
	}

	var out []ConnectedRef
	add := func(kind, name string, spec corev1.PodSpec) {
		if reason, ok := match(spec); ok {
			out = append(out, ConnectedRef{Kind: KindApp, SubKind: kind, Namespace: namespace, Name: name, Reason: reason})
		}
	}
	// Informer objects are shared and read-only. Matchers inspect pod specs;
	// neither they nor this projection mutate templates or their nested slices.
	for _, d := range deployments {
		add(AppKindDeployment, d.Name, d.Spec.Template.Spec)
	}
	for _, s := range statefulsets {
		add(AppKindStatefulSet, s.Name, s.Spec.Template.Spec)
	}
	for _, d := range daemonsets {
		add(AppKindDaemonSet, d.Name, d.Spec.Template.Spec)
	}
	for _, j := range cronjobs {
		add(AppKindCronJob, j.Name, j.Spec.JobTemplate.Spec.Template.Spec)
	}
	for _, j := range jobList {
		if !jobOwnedByCronJob(*j) {
			add(AppKindJob, j.Name, j.Spec.Template.Spec)
		}
	}
	return out, true
}

// cachedPod is the pod from the informer cache, if that cache is synced and
// holds it. Callers fall back to a direct Get otherwise.
func (c *Client) cachedPod(namespace, name string) (*corev1.Pod, bool) {
	podL := c.Informers().Pods()
	if podL == nil {
		return nil, false
	}
	p, err := podL.Pods(namespace).Get(name)
	return p, err == nil
}

// InspectPod is InspectPod served from the pod cache when it can be: the
// pod list on screen comes from the same cache, so moving the cursor across
// pods costs no API requests.
func (c *Client) InspectPod(ctx context.Context, namespace, name string) (PodDetail, error) {
	if p, ok := c.cachedPod(namespace, name); ok {
		return podDetail(p), nil
	}
	return InspectPod(ctx, c.Clientset, namespace, name)
}

// ConnectionsForPod is ConnectionsForPod served from the pod and service
// caches when both are synced.
func (c *Client) ConnectionsForPod(ctx context.Context, namespace, name string) (Connections, error) {
	p, ok := c.cachedPod(namespace, name)
	svcL := c.Informers().Services()
	if !ok || svcL == nil {
		return ConnectionsForPod(ctx, c.Clientset, namespace, name)
	}
	services, err := svcL.Services(namespace).List(labels.Everything())
	if err != nil {
		return ConnectionsForPod(ctx, c.Clientset, namespace, name)
	}
	return podConnections(p, services), nil
}
