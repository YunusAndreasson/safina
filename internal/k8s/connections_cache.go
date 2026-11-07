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
