package sections

import (
	"context"
	"fmt"

	tea "charm.land/bubbletea/v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/YunusAndreasson/safina/internal/debug"
	"github.com/YunusAndreasson/safina/internal/k8s"
)

// LoadTreeChildren fetches the children for a tree focus path. Dispatches
// on the focus kind. Always returns TreeChildrenLoadedMsg — empty payload
// on error so the UI can clear its loading state.
func LoadTreeChildren(ctx context.Context, mgr *k8s.Manager, contextName string, path []NodeRef, userNS, systemNS []string) tea.Cmd {
	return func() tea.Msg {
		focus := path[len(path)-1]
		defer debug.Track("k8s.load_children", "context", contextName, "kind", focus.Kind, "name", focus.Name)()
		client, ok := mgr.GetClient(contextName)
		if !ok {
			return TreeChildrenLoadedMsg{Path: path, Err: errContextNotFound}
		}
		ch, err := loadChildren(ctx, client, path, userNS, systemNS)
		return TreeChildrenLoadedMsg{Path: path, Children: ch, Err: err}
	}
}

// loadChildren dispatches on the focus kind. Reads go through the Client's
// cache-aware methods — informer-backed when the context's cache is warm,
// transparent direct-API fallback otherwise — so the 5s auto-refresh tick
// costs memory reads, not a List fan-out. CRDs (client.Dynamic) and one-off
// PV/PVC drills stay direct.
func loadChildren(ctx context.Context, client *k8s.Client, path []NodeRef, cfgUser, cfgSystem []string) (*Children, error) {
	focus := path[len(path)-1]

	switch focus.Kind {
	case k8s.KindContextList:
		// The context list is pre-seeded into the cache by the root model
		// from the kubeconfig — there's no API call to make here. Returning
		// empty children is a safety net; in practice the cache hits first.
		return &Children{Kind: k8s.KindContextList}, nil

	case k8s.KindContext:
		// Cluster-root now lists namespace folders directly: Cluster + All
		// + every user namespace + System. Apps are still fetched here so
		// (1) the All folder gets an accurate count, and (2) namespace
		// folders inherit the worst-of-apps health pip via nsHealthCache.
		userNs, sysNs, err := client.ListAndClassifyNamespaces(ctx, cfgUser, cfgSystem)
		if err != nil {
			return nil, err
		}
		userNames := make([]string, len(userNs))
		for i, n := range userNs {
			userNames[i] = n.Name
		}
		sysNames := make([]string, len(sysNs))
		for i, n := range sysNs {
			sysNames[i] = n.Name
		}
		apps, err := client.ListAppsInNamespaces(ctx, userNames)
		if err != nil {
			return nil, err
		}
		return &Children{
			Kind:        k8s.KindNamespaceFolder,
			UserNS:      userNs,
			Apps:        apps,
			SystemCount: len(sysNames),
			SystemNS:    sysNames,
		}, nil

	case k8s.KindAllNamespacesFold:
		// "All" folder: the unified cross-namespace view. Returns apps so
		// the renderer can emit the same Workloads/Config/Secrets/Network/
		// Storage fold layout the cluster root used to host inline.
		userNs, _, err := client.ListAndClassifyNamespaces(ctx, cfgUser, cfgSystem)
		if err != nil {
			return nil, err
		}
		userNames := make([]string, len(userNs))
		for i, n := range userNs {
			userNames[i] = n.Name
		}
		apps, err := client.ListAppsInNamespaces(ctx, userNames)
		if err != nil {
			return nil, err
		}
		return &Children{Kind: k8s.KindApp, Apps: apps}, nil

	case k8s.KindClusterFold:
		pools, _, err := client.ListNodesAndPools(ctx)
		if err != nil {
			return nil, err
		}
		return &Children{Kind: k8s.KindNodePool, Pools: pools}, nil

	case k8s.KindInfrastructureFold:
		// Children are three synthetic rows (Nodes, System, Configure
		// resources). The only payload we need to fetch is the System
		// namespace count so the System row can show "(N)" — without it
		// the badge would render "(0)" or get suppressed entirely.
		_, sysNs, err := client.ListAndClassifyNamespaces(ctx, cfgUser, cfgSystem)
		if err != nil {
			return nil, err
		}
		sysNames := make([]string, len(sysNs))
		for i, n := range sysNs {
			sysNames[i] = n.Name
		}
		return &Children{
			Kind:        k8s.KindInfrastructureFold,
			SystemCount: len(sysNs),
			SystemNS:    sysNames,
		}, nil

	case k8s.KindWorkloadsFold:
		// Scope by parent: a Workloads fold inside a NamespaceFolder lists
		// only that namespace's workloads; otherwise (inside the All fold)
		// it spans every user namespace.
		nss, err := workloadNamespaces(ctx, client, path, cfgUser, cfgSystem)
		if err != nil {
			return nil, err
		}
		apps, err := client.ListAppsInNamespaces(ctx, nss)
		if err != nil {
			return nil, err
		}
		return &Children{Kind: k8s.KindApp, Apps: apps}, nil

	case k8s.KindSystemFold:
		_, sysNs, err := client.ListAndClassifyNamespaces(ctx, cfgUser, cfgSystem)
		if err != nil {
			return nil, err
		}
		return &Children{Kind: k8s.KindNamespace, Namespaces: sysNs}, nil

	case k8s.KindNamespace, k8s.KindNamespaceFolder:
		// Drilling into a single namespace folder: render its scoped fold
		// layout. Apps are loaded so the Workloads count badge populates and
		// the namespace's worst-of health pip stays current. The renderer
		// detects the parent kind and emits Workloads/Config/Secrets/Network/
		// Storage fold rows above (or instead of) the apps list.
		apps, err := client.ListAppsInNamespaces(ctx, []string{focus.Name})
		if err != nil {
			return nil, err
		}
		return &Children{Kind: k8s.KindApp, Apps: apps}, nil

	case k8s.KindApp:
		pods, err := client.ListPodsForApp(ctx, k8s.AppRef{
			Kind:      focus.SubKind,
			Namespace: focus.Namespace,
			Name:      focus.Name,
			UID:       focus.UID,
		})
		if err != nil {
			return nil, err
		}
		return &Children{Kind: k8s.KindPod, Pods: pods}, nil

	case k8s.KindPod:
		ctrs, err := client.ListContainersForPod(ctx, focus.Namespace, focus.Name)
		if err != nil {
			return nil, err
		}
		return &Children{Kind: k8s.KindContainer, Containers: ctrs}, nil

	case k8s.KindNodePool:
		_, allNodes, err := client.ListNodesAndPools(ctx)
		if err != nil {
			return nil, err
		}
		filtered := make([]k8s.NodeInfo, 0)
		for _, n := range allNodes {
			if n.Pool == focus.Name {
				filtered = append(filtered, n)
			}
		}
		return &Children{Kind: k8s.KindNode, Nodes: filtered}, nil

	case k8s.KindNode:
		pods, err := client.ListPodsOnNode(ctx, focus.Name)
		if err != nil {
			return nil, err
		}
		return &Children{Kind: k8s.KindPod, Pods: pods}, nil

	case k8s.KindConfigFold:
		nss, err := workloadNamespaces(ctx, client, path, cfgUser, cfgSystem)
		if err != nil {
			return nil, err
		}
		cms, err := client.ListConfigMapsAcrossNamespaces(ctx, nss)
		if err != nil {
			return nil, err
		}
		return &Children{Kind: k8s.KindConfigMap, ConfigMaps: cms}, nil

	case k8s.KindSecretsFold:
		nss, err := workloadNamespaces(ctx, client, path, cfgUser, cfgSystem)
		if err != nil {
			return nil, err
		}
		secrets, err := client.ListSecretsAcrossNamespaces(ctx, nss)
		if err != nil {
			return nil, err
		}
		return &Children{Kind: k8s.KindSecret, Secrets: secrets}, nil

	case k8s.KindNetworkFold:
		nss, err := workloadNamespaces(ctx, client, path, cfgUser, cfgSystem)
		if err != nil {
			return nil, err
		}
		svcs, err := client.ListServicesAcrossNamespaces(ctx, nss)
		if err != nil {
			return nil, err
		}
		return &Children{Kind: k8s.KindService, Services: svcs}, nil

	case k8s.KindIngressFold:
		nss, err := workloadNamespaces(ctx, client, path, cfgUser, cfgSystem)
		if err != nil {
			return nil, err
		}
		ings, err := client.ListIngressesAcrossNamespaces(ctx, nss)
		if err != nil {
			return nil, err
		}
		return &Children{Kind: k8s.KindIngress, Ingresses: ings}, nil

	case k8s.KindService:
		ings, err := client.ListIngressesForService(ctx, focus.Namespace, focus.Name)
		if err != nil {
			return nil, err
		}
		return &Children{Kind: k8s.KindIngress, Ingresses: ings}, nil

	case k8s.KindStorageFold:
		nss, err := workloadNamespaces(ctx, client, path, cfgUser, cfgSystem)
		if err != nil {
			return nil, err
		}
		pvcs, err := client.ListPVCsAcrossNamespaces(ctx, nss)
		if err != nil {
			return nil, err
		}
		return &Children{Kind: k8s.KindPVC, PVCs: pvcs}, nil

	case k8s.KindPVC:
		// Get the PVC to find its bound volume name, then fetch the PV.
		// One-off drill Gets stay direct — the apiserver is authoritative.
		pvc, err := client.Clientset.CoreV1().PersistentVolumeClaims(focus.Namespace).Get(ctx, focus.Name, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		pv, err := k8s.GetPVByName(ctx, client.Clientset, pvc.Spec.VolumeName)
		if err != nil {
			return nil, err
		}
		pvs := []k8s.PVInfo{}
		if pv != nil {
			pvs = append(pvs, *pv)
		}
		return &Children{Kind: k8s.KindPV, PVs: pvs}, nil

	case k8s.KindCustomFold:
		gvr := focus.GVR
		if gvr.Version == "" || gvr.Resource == "" {
			return nil, fmt.Errorf("invalid GVR %q", focus.GVR)
		}
		nsList := []string{""}
		if !focus.ClusterScoped {
			var err error
			nsList, err = workloadNamespaces(ctx, client, path, cfgUser, cfgSystem)
			if err != nil {
				return nil, err
			}
		}
		if gvr.Group == "" && gvr.Version == "v1" && gvr.Resource == "pods" {
			pods, err := client.ListPodsAcrossNamespaces(ctx, nsList)
			return &Children{Kind: k8s.KindPod, Pods: pods}, err
		}
		// Preserve namespace boundaries, including the configured user-namespace
		// set under All. Cluster-scoped kinds explicitly use the empty namespace.
		var items []k8s.GenericResource
		for _, ns := range nsList {
			found, err := k8s.ListGeneric(ctx, client.Dynamic, gvr, ns)
			if err != nil {
				return nil, err
			}
			items = append(items, found...)
		}
		return &Children{Kind: k8s.KindGenericResource, Generic: items}, nil

	}

	return &Children{Kind: focus.Kind}, nil
}

// workloadNamespaces resolves which namespaces a resource-fold (Workloads,
// Config, Secrets, Network, Storage) should list against, by inspecting the
// drill path. When a NamespaceFolder (or system Namespace) parent is in the
// path the fold is scoped to that single namespace — what the user expects
// after drilling "payments/ ▸ Workloads". Otherwise (inside the All folder)
// it spans every user namespace, mirroring safina's pre-folders behavior.
// The user bucket IS the complement of the system bucket, so one classify
// call replaces the old classify-then-relist-and-filter pair.
func workloadNamespaces(ctx context.Context, client *k8s.Client, path []NodeRef, cfgUser, cfgSystem []string) ([]string, error) {
	for i := len(path) - 1; i >= 0; i-- {
		switch path[i].Kind {
		case k8s.KindNamespaceFolder, k8s.KindNamespace:
			return []string{path[i].Name}, nil
		}
	}
	userNs, _, err := client.ListAndClassifyNamespaces(ctx, cfgUser, cfgSystem)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(userNs))
	for i, n := range userNs {
		names[i] = n.Name
	}
	return names, nil
}
