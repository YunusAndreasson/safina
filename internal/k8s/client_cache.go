package k8s

import (
	"cmp"
	"context"
	"fmt"
	"slices"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	corev1listers "k8s.io/client-go/listers/core/v1"
)

// Cache-aware variants of the tree read paths. Every method follows the
// same contract as ListHotPodsByCPU / Client.LoadClusterHealth:
//
//   - a nil lister (context not active, kind not synced yet, RBAC denied
//     the Watch) falls back to the direct-API package function, so the UI
//     renders identically — just off the network instead of memory;
//   - metrics are ALWAYS fetched direct (metrics-server has no Watch);
//   - lister results are SHARED with the informer cache: read-only, copy
//     (`*p`) before handing to converters, never mutate in place.
//
// These are what the 5s auto-refresh tick hits, so the steady-state cost of
// a parked view is one metrics call — not a per-namespace × kind List fan-out.

// ListAndClassifyNamespaces is the cache-aware variant of the package-level
// function.
func (c *Client) ListAndClassifyNamespaces(ctx context.Context, cfgUser, cfgSystem []string) (user, system []NamespaceInfo, err error) {
	nsLister := c.Informers().Namespaces()
	if nsLister == nil {
		return ListAndClassifyNamespaces(ctx, c.Clientset, cfgUser, cfgSystem)
	}
	list, err := nsLister.List(labels.Everything())
	if err != nil {
		return nil, nil, err
	}
	names := make([]string, 0, len(list))
	ageBy := make(map[string]string, len(list))
	for _, n := range list {
		names = append(names, n.Name)
		ageBy[n.Name] = HumanAge(n.CreationTimestamp.Time)
	}
	u, s := classifyNamespacesWithAge(names, ageBy, cfgUser, cfgSystem)
	return u, s, nil
}

// ListAppsInNamespaces is the cache-aware variant of the package-level
// function. The direct path's per-namespace × kind goroutine fan-out (6 List
// calls per namespace per tick) collapses to in-memory lister reads; only
// pod metrics still hit the network.
func (c *Client) ListAppsInNamespaces(ctx context.Context, namespaces []string) ([]AppInfo, error) {
	inf := c.Informers()
	depL := inf.Deployments()
	stsL := inf.StatefulSets()
	dsL := inf.DaemonSets()
	jobL := inf.Jobs()
	cjL := inf.CronJobs()
	podL := inf.Pods()
	if depL == nil || stsL == nil || dsL == nil || jobL == nil || cjL == nil || podL == nil {
		return ListAppsInNamespaces(ctx, c.Clientset, namespaces)
	}
	if len(namespaces) == 0 {
		return nil, nil
	}

	// Metrics aren't watchable — direct call, same as the direct path.
	metrics, _ := ListPodMetrics(ctx, c.Clientset, namespaceScope(namespaces))

	// One cluster-wide pod snapshot for per-app aggregation, dereferenced
	// once and shared across all namespaces (converters take values).
	cachedPods, err := podL.List(labels.Everything())
	if err != nil {
		return nil, err
	}
	podsAll := derefPods(cachedPods)

	var out []AppInfo
	hpaIdx := make(map[string]*HPABadge)
	hpaL := inf.HPAs()
	for _, ns := range namespaces {
		if items, err := depL.Deployments(ns).List(labels.Everything()); err == nil {
			for _, d := range items {
				out = append(out, deploymentToApp(*d, podsAll, metrics))
			}
		}
		if items, err := stsL.StatefulSets(ns).List(labels.Everything()); err == nil {
			for _, s := range items {
				out = append(out, statefulSetToApp(*s, podsAll, metrics))
			}
		}
		if items, err := dsL.DaemonSets(ns).List(labels.Everything()); err == nil {
			for _, d := range items {
				out = append(out, daemonSetToApp(*d, podsAll, metrics))
			}
		}
		if items, err := cjL.CronJobs(ns).List(labels.Everything()); err == nil {
			for _, cj := range items {
				out = append(out, cronJobToApp(*cj))
			}
		}
		if items, err := jobL.Jobs(ns).List(labels.Everything()); err == nil {
			for _, j := range items {
				// Skip jobs owned by a CronJob — they appear when drilling
				// into the CronJob, not as top-level Apps.
				if jobOwnedByCronJob(*j) {
					continue
				}
				out = append(out, jobToApp(*j, podsAll, metrics))
			}
		}
		// HPA badges: lister when synced, direct otherwise (an RBAC-denied
		// HPA watch shouldn't force the whole apps view off the cache — the
		// direct helper already degrades to an empty map on error).
		if hpaL != nil {
			if items, err := hpaL.HorizontalPodAutoscalers(ns).List(labels.Everything()); err == nil {
				for _, h := range items {
					key := h.Namespace + "/" + h.Spec.ScaleTargetRef.Kind + "/" + h.Spec.ScaleTargetRef.Name
					hpaIdx[key] = hpaV2ToBadge(*h)
				}
			}
		} else {
			for k, v := range listHPABadges(ctx, c.Clientset, ns) {
				hpaIdx[k] = v
			}
		}
	}

	return finishApps(out, hpaIdx), nil
}

// ListNodesAndPools is the cache-aware variant of the package-level function.
func (c *Client) ListNodesAndPools(ctx context.Context) ([]NodePoolInfo, []NodeInfo, error) {
	nodeL := c.Informers().Nodes()
	podL := c.Informers().Pods()
	if nodeL == nil || podL == nil {
		return ListNodesAndPools(ctx, c.Clientset)
	}
	nodeMetrics, _ := ListNodeMetrics(ctx, c.Clientset)
	cachedNodes, err := nodeL.List(labels.Everything())
	if err != nil {
		return nil, nil, err
	}
	cachedPods, err := podL.List(labels.Everything())
	if err != nil {
		return nil, nil, err
	}
	nodes := make([]corev1.Node, len(cachedNodes))
	for i, n := range cachedNodes {
		nodes[i] = *n
	}
	pools, infos := buildNodesAndPools(nodes, nodeMetrics, derefPods(cachedPods))
	return pools, infos, nil
}

// ListPodsForApp is the cache-aware variant of the package-level function,
// walking the same owner-reference chains (Deployment → ReplicaSet → Pod,
// CronJob → Job → Pod) over listers instead of List calls.
func (c *Client) ListPodsForApp(ctx context.Context, app AppRef) ([]PodInfo, error) {
	podL := c.Informers().Pods()
	if podL == nil {
		return ListPodsForApp(ctx, c.Clientset, app)
	}
	switch app.Kind {
	case AppKindDeployment:
		rsL := c.Informers().ReplicaSets()
		if rsL == nil {
			return ListPodsForApp(ctx, c.Clientset, app)
		}
		rss, err := rsL.ReplicaSets(app.Namespace).List(labels.Everything())
		if err != nil {
			return nil, err
		}
		rsUIDs := make([]types.UID, 0)
		for _, rs := range rss {
			for _, o := range rs.OwnerReferences {
				if o.UID == app.UID {
					rsUIDs = append(rsUIDs, rs.UID)
					break
				}
			}
		}
		return c.cachedPodsByOwnerUID(ctx, app.Namespace, rsUIDs, podL)
	case AppKindStatefulSet, AppKindDaemonSet, AppKindJob:
		return c.cachedPodsByOwnerUID(ctx, app.Namespace, []types.UID{app.UID}, podL)
	case AppKindCronJob:
		jobL := c.Informers().Jobs()
		if jobL == nil {
			return ListPodsForApp(ctx, c.Clientset, app)
		}
		jobs, err := jobL.Jobs(app.Namespace).List(labels.Everything())
		if err != nil {
			return nil, err
		}
		jobUIDs := make([]types.UID, 0)
		for _, j := range jobs {
			for _, o := range j.OwnerReferences {
				if o.UID == app.UID {
					jobUIDs = append(jobUIDs, j.UID)
					break
				}
			}
		}
		return c.cachedPodsByOwnerUID(ctx, app.Namespace, jobUIDs, podL)
	}
	return nil, fmt.Errorf("unknown app kind %q", app.Kind)
}

// cachedPodsByOwnerUID mirrors podsByOwnerUID over the pod lister.
func (c *Client) cachedPodsByOwnerUID(ctx context.Context, namespace string, ownerUIDs []types.UID, podL corev1listers.PodLister) ([]PodInfo, error) {
	if len(ownerUIDs) == 0 {
		return nil, nil
	}
	cached, err := podL.Pods(namespace).List(labels.Everything())
	if err != nil {
		return nil, err
	}
	metrics, _ := ListPodMetrics(ctx, c.Clientset, namespace)
	return filterPodsByOwner(derefPods(cached), ownerUIDs, metrics), nil
}

// ListPodsOnNode is the cache-aware variant of the package-level function.
// The direct path's server-side `spec.nodeName` field selector becomes an
// in-memory filter — same trade the HotPods phase filter already makes.
func (c *Client) ListPodsOnNode(ctx context.Context, nodeName string) ([]PodInfo, error) {
	podL := c.Informers().Pods()
	if podL == nil {
		return ListPodsOnNode(ctx, c.Clientset, nodeName)
	}
	cached, err := podL.List(labels.Everything())
	if err != nil {
		return nil, err
	}
	onNode := make([]corev1.Pod, 0)
	for _, p := range cached {
		if p.Spec.NodeName == nodeName {
			onNode = append(onNode, *p)
		}
	}
	metrics, _ := ListPodMetrics(ctx, c.Clientset, "")
	return podInfosSortedNSName(onNode, metrics), nil
}

// ListContainersForPod is the cache-aware variant of the package-level
// function. Falls back to a direct Get when the pod isn't in the cache yet
// (e.g. drilling into a pod created milliseconds ago) — the apiserver is
// authoritative for misses.
func (c *Client) ListContainersForPod(ctx context.Context, namespace, name string) ([]ContainerInfo, error) {
	podL := c.Informers().Pods()
	if podL == nil {
		return ListContainersForPod(ctx, c.Clientset, namespace, name)
	}
	p, err := podL.Pods(namespace).Get(name)
	if err != nil {
		return ListContainersForPod(ctx, c.Clientset, namespace, name)
	}
	byCtr, _ := ListPodMetricsByContainer(ctx, c.Clientset, namespace)
	return containersForPod(p, byCtr[namespace+"/"+name]), nil
}

// ListConfigMapsAcrossNamespaces is the cache-aware variant of the
// package-level function.
func (c *Client) ListConfigMapsAcrossNamespaces(ctx context.Context, namespaces []string) ([]ConfigMapInfo, error) {
	lister := c.Informers().ConfigMaps()
	if lister == nil {
		return ListConfigMapsAcrossNamespaces(ctx, c.Clientset, namespaces)
	}
	out := cachedRows(namespaces, func(ns string) ([]*corev1.ConfigMap, error) {
		return lister.ConfigMaps(ns).List(labels.Everything())
	}, configMapToInfo)
	sortByNSName(out, func(r ConfigMapInfo) (string, string) { return r.Namespace, r.Name })
	return out, nil
}

// ListSecretsAcrossNamespaces is the cache-aware variant of the package-level
// function. Cached secrets carry key names and Type only — values are zeroed
// by the informer transform, which is all SecretInfo needs.
func (c *Client) ListSecretsAcrossNamespaces(ctx context.Context, namespaces []string) ([]SecretInfo, error) {
	lister := c.Informers().Secrets()
	if lister == nil {
		return ListSecretsAcrossNamespaces(ctx, c.Clientset, namespaces)
	}
	out := cachedRows(namespaces, func(ns string) ([]*corev1.Secret, error) {
		return lister.Secrets(ns).List(labels.Everything())
	}, secretToInfo)
	sortByNSName(out, func(r SecretInfo) (string, string) { return r.Namespace, r.Name })
	return out, nil
}

// ListServicesAcrossNamespaces is the cache-aware variant of the
// package-level function.
func (c *Client) ListServicesAcrossNamespaces(ctx context.Context, namespaces []string) ([]ServiceInfo, error) {
	lister := c.Informers().Services()
	if lister == nil {
		return ListServicesAcrossNamespaces(ctx, c.Clientset, namespaces)
	}
	out := cachedRows(namespaces, func(ns string) ([]*corev1.Service, error) {
		return lister.Services(ns).List(labels.Everything())
	}, serviceToInfo)
	sortByNSName(out, func(r ServiceInfo) (string, string) { return r.Namespace, r.Name })
	return out, nil
}

// ListIngressesAcrossNamespaces is the cache-aware variant of the
// package-level function.
func (c *Client) ListIngressesAcrossNamespaces(ctx context.Context, namespaces []string) ([]IngressInfo, error) {
	lister := c.Informers().Ingresses()
	if lister == nil {
		return ListIngressesAcrossNamespaces(ctx, c.Clientset, namespaces)
	}
	out := cachedRows(namespaces, func(ns string) ([]*networkingv1.Ingress, error) {
		return lister.Ingresses(ns).List(labels.Everything())
	}, ingressToInfo)
	sortByNSName(out, func(r IngressInfo) (string, string) { return r.Namespace, r.Name })
	return out, nil
}

// ListPVCsAcrossNamespaces is the cache-aware variant of the package-level
// function.
func (c *Client) ListPVCsAcrossNamespaces(ctx context.Context, namespaces []string) ([]PVCInfo, error) {
	lister := c.Informers().PVCs()
	if lister == nil {
		return ListPVCsAcrossNamespaces(ctx, c.Clientset, namespaces)
	}
	out := cachedRows(namespaces, func(ns string) ([]*corev1.PersistentVolumeClaim, error) {
		return lister.PersistentVolumeClaims(ns).List(labels.Everything())
	}, pvcToInfo)
	sortByNSName(out, func(r PVCInfo) (string, string) { return r.Namespace, r.Name })
	return out, nil
}

// ListIngressesForService is the cache-aware variant of the package-level
// function.
func (c *Client) ListIngressesForService(ctx context.Context, namespace, serviceName string) ([]IngressInfo, error) {
	lister := c.Informers().Ingresses()
	if lister == nil {
		return ListIngressesForService(ctx, c.Clientset, namespace, serviceName)
	}
	items, err := lister.Ingresses(namespace).List(labels.Everything())
	if err != nil {
		return nil, err
	}
	var out []IngressInfo
	for _, ing := range items {
		if !ingressReferencesService(*ing, serviceName) {
			continue
		}
		out = append(out, ingressToInfo(*ing))
	}
	return out, nil
}

// derefPods copies lister pointers into a value slice for the converters,
// which take []corev1.Pod (the direct path hands them list.Items). Shallow
// copies — fine, converters are read-only.
func derefPods(cached []*corev1.Pod) []corev1.Pod {
	out := make([]corev1.Pod, len(cached))
	for i, p := range cached {
		out[i] = *p
	}
	return out
}

// cachedRows flattens per-namespace lister reads through a row converter.
// Mirrors the direct fan-outs' partial-failure tolerance: a namespace whose
// read fails is dropped, not fatal (in practice lister reads don't fail).
func cachedRows[T any, R any](namespaces []string, listNS func(string) ([]*T, error), conv func(T) R) []R {
	var out []R
	for _, ns := range namespaces {
		items, err := listNS(ns)
		if err != nil {
			continue
		}
		for _, it := range items {
			out = append(out, conv(*it))
		}
	}
	return out
}

// sortByNSName sorts rows by (namespace, name) — the stable order every
// across-namespaces view renders in.
func sortByNSName[R any](rows []R, key func(R) (string, string)) {
	slices.SortFunc(rows, func(a, b R) int {
		nsI, nameI := key(a)
		nsJ, nameJ := key(b)
		if nsI != nsJ {
			return cmp.Compare(nsI, nsJ)
		}
		return cmp.Compare(nameI, nameJ)
	})
}
