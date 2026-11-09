package k8s

import (
	"cmp"
	"context"
	"fmt"
	"slices"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
//     when a converter requires a value, or pass a read-only iterator.
//     Never mutate informer objects in place.
//
// These are what the 5s auto-refresh tick hits, so the steady-state cost of
// a parked view is one metrics call — not a per-namespace × kind List fan-out.

// ListAndClassifyNamespaces is the cache-aware variant of the package-level
// function.
func (c *Client) ListAndClassifyNamespaces(ctx context.Context, cfgUser, cfgSystem []string) (user, system []NamespaceInfo, err error) {
	if ns := c.scopedNamespace(); ns != "" {
		return c.scopedNamespaceRow(ctx, ns, cfgUser, cfgSystem)
	}
	nsLister := c.Informers().Namespaces()
	if nsLister == nil {
		user, system, err = ListAndClassifyNamespaces(ctx, c.Clientset, cfgUser, cfgSystem)
		if ns := c.Namespace(); ns != "" && apierrors.IsForbidden(err) {
			// From here on, reads and watches stay inside the namespace.
			if c.namespacesForbidden.CompareAndSwap(false, true) {
				c.rescopeInformers()
			}
			return c.scopedNamespaceRow(ctx, ns, cfgUser, cfgSystem)
		}
		return user, system, err
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

// scopedNamespaceRow stands in for the cluster-wide list on an account that may
// read only its own namespace: that namespace is the cluster's one row, and
// every read below it is namespaced. Its age needs one Get, asked once: the
// account may not have that either, and then the age stays blank.
func (c *Client) scopedNamespaceRow(ctx context.Context, ns string, cfgUser, cfgSystem []string) (user, system []NamespaceInfo, err error) {
	if c.namespaceAgeAsked.CompareAndSwap(false, true) {
		if n, getErr := c.Clientset.CoreV1().Namespaces().Get(ctx, ns, metav1.GetOptions{}); getErr == nil {
			created := n.CreationTimestamp.Time
			c.namespaceCreated.Store(&created)
		}
	}
	ageBy := map[string]string{}
	if created := c.namespaceCreated.Load(); created != nil {
		ageBy[ns] = HumanAge(*created)
	}
	u, s := classifyNamespacesWithAge([]string{ns}, ageBy, cfgUser, cfgSystem)
	return u, s, nil
}

// ListAppsInNamespaces is the cache-aware variant of the package-level
// function. Each kind with a synced cache is read from memory; the rest go
// direct, and a kind this account can't list (or the server doesn't serve)
// stops being asked for on every tick. Pod metrics always hit the network.
func (c *Client) ListAppsInNamespaces(ctx context.Context, namespaces []string) ([]AppInfo, error) {
	inf := c.Informers()
	return listApps(ctx, c.Clientset, namespaces, appSources{
		pods:         inf.Pods(),
		deployments:  inf.Deployments(),
		statefulSets: inf.StatefulSets(),
		daemonSets:   inf.DaemonSets(),
		jobs:         inf.Jobs(),
		cronJobs:     inf.CronJobs(),
		hpas:         inf.HPAs(),
		gate:         &c.reads,
	})
}

// ListNodesAndPools is the cache-aware variant of the package-level function.
func (c *Client) ListNodesAndPools(ctx context.Context) ([]NodePoolInfo, []NodeInfo, error) {
	nodeL := c.Informers().Nodes()
	podL := c.Informers().Pods()
	if nodeL == nil || podL == nil {
		return ListNodesAndPools(ctx, c.Clientset)
	}
	nodeMetrics, metricsErr := ListNodeMetrics(ctx, c.Clientset)
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
	pools, infos := buildNodesAndPools(nodes, nodeMetrics, slices.Values(cachedPods), metricsErr)
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
	metrics, metricsErr := ListPodMetrics(ctx, c.Clientset, namespace)
	return filterPodsByOwner(slices.Values(cached), ownerUIDs, metrics, metricsErr), nil
}

// ListPodsOnNode uses the informer's maintained node-name index, avoiding
// a cluster-wide pod scan on every node drill-down.
func (c *Client) ListPodsOnNode(ctx context.Context, nodeName string) ([]PodInfo, error) {
	index := c.Informers().PodIndex()
	if index == nil {
		return ListPodsOnNode(ctx, c.Clientset, nodeName)
	}
	onNode, err := index.ByTypedIndex(podNodeIndex, nodeName)
	if err != nil {
		return nil, err
	}
	metrics, metricsErr := ListPodMetrics(ctx, c.Clientset, "")
	return podInfosSortedNSName(derefPods(onNode), metrics, metricsErr), nil
}

func (c *Client) ListContainersForPod(ctx context.Context, namespace, name string) ([]ContainerInfo, error) {
	podL := c.Informers().Pods()
	if podL == nil {
		return ListContainersForPod(ctx, c.Clientset, namespace, name)
	}
	p, err := podL.Pods(namespace).Get(name)
	if err != nil {
		return ListContainersForPod(ctx, c.Clientset, namespace, name)
	}
	byCtr, metricsErr := ListPodMetricsByContainer(ctx, c.Clientset, namespace)
	return containersForPod(p, byCtr[types.NamespacedName{Namespace: namespace, Name: name}], metricsErr), nil
}

// PodContainers lists a pod's containers with states and restarts but no
// metrics. The log target picker reads every pod of a workload in turn, and a
// namespace-wide metrics request for each kept a 50-replica Deployment's logs
// waiting on 50 serial calls.
func (c *Client) PodContainers(ctx context.Context, namespace, name string) ([]ContainerInfo, error) {
	if podL := c.Informers().Pods(); podL != nil {
		if p, err := podL.Pods(namespace).Get(name); err == nil {
			return containersForPod(p, nil), nil
		}
	}
	p, err := c.Clientset.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return containersForPod(p, nil), nil
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

// ListPodsAcrossNamespaces includes standalone pods as well as managed workloads.
func (c *Client) ListPodsAcrossNamespaces(ctx context.Context, namespaces []string) ([]PodInfo, error) {
	var out []PodInfo
	lister := c.Informers().Pods()
	for _, ns := range namespaces {
		var pods []corev1.Pod
		if lister != nil {
			cached, err := lister.Pods(ns).List(labels.Everything())
			if err != nil {
				return nil, err
			}
			pods = derefPods(cached)
		} else {
			list, err := c.Clientset.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{})
			if err != nil {
				return nil, err
			}
			pods = list.Items
		}
		metrics, metricsErr := ListPodMetrics(ctx, c.Clientset, ns)
		out = append(out, podInfosSortedNSName(pods, metrics, metricsErr)...)
	}
	slices.SortFunc(out, func(a, b PodInfo) int {
		return cmp.Or(cmp.Compare(a.Namespace, b.Namespace), cmp.Compare(a.Name, b.Name))
	})
	return out, nil
}
