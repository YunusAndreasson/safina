package sections

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"github.com/yunus/safina/internal/debug"
	"github.com/yunus/safina/internal/k8s"
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
		ch, err := loadChildren(ctx, client.Clientset, client.Dynamic, path, userNS, systemNS)
		return TreeChildrenLoadedMsg{Path: path, Children: ch, Err: err}
	}
}

func loadChildren(ctx context.Context, cs kubernetes.Interface, dyn dynamic.Interface, path []NodeRef, cfgUser, cfgSystem []string) (*Children, error) {
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
		userNs, sysNs, err := k8s.ListAndClassifyNamespaces(ctx, cs, cfgUser, cfgSystem)
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
		apps, err := k8s.ListAppsInNamespaces(ctx, cs, userNames)
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
		userNs, _, err := k8s.ListAndClassifyNamespaces(ctx, cs, cfgUser, cfgSystem)
		if err != nil {
			return nil, err
		}
		userNames := make([]string, len(userNs))
		for i, n := range userNs {
			userNames[i] = n.Name
		}
		apps, err := k8s.ListAppsInNamespaces(ctx, cs, userNames)
		if err != nil {
			return nil, err
		}
		return &Children{Kind: k8s.KindApp, Apps: apps}, nil

	case k8s.KindClusterFold:
		pools, _, err := k8s.ListNodesAndPools(ctx, cs)
		if err != nil {
			return nil, err
		}
		return &Children{Kind: k8s.KindNodePool, Pools: pools}, nil

	case k8s.KindInfrastructureFold:
		// Children are three synthetic rows (Nodes, System, Configure
		// resources). The only payload we need to fetch is the System
		// namespace count so the System row can show "(N)" — without it
		// the badge would render "(0)" or get suppressed entirely.
		_, sysNs, err := k8s.ListAndClassifyNamespaces(ctx, cs, cfgUser, cfgSystem)
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
		nss, err := workloadNamespaces(ctx, cs, path, cfgUser, cfgSystem)
		if err != nil {
			return nil, err
		}
		apps, err := k8s.ListAppsInNamespaces(ctx, cs, nss)
		if err != nil {
			return nil, err
		}
		return &Children{Kind: k8s.KindApp, Apps: apps}, nil

	case k8s.KindSystemFold:
		_, sysNs, err := k8s.ListAndClassifyNamespaces(ctx, cs, cfgUser, cfgSystem)
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
		apps, err := k8s.ListAppsInNamespaces(ctx, cs, []string{focus.Name})
		if err != nil {
			return nil, err
		}
		return &Children{Kind: k8s.KindApp, Apps: apps}, nil

	case k8s.KindApp:
		pods, err := k8s.ListPodsForApp(ctx, cs, k8s.AppRef{
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
		ctrs, err := k8s.ListContainersForPod(ctx, cs, focus.Namespace, focus.Name)
		if err != nil {
			return nil, err
		}
		return &Children{Kind: k8s.KindContainer, Containers: ctrs}, nil

	case k8s.KindNodePool:
		_, allNodes, err := k8s.ListNodesAndPools(ctx, cs)
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
		pods, err := k8s.ListPodsOnNode(ctx, cs, focus.Name)
		if err != nil {
			return nil, err
		}
		return &Children{Kind: k8s.KindPod, Pods: pods}, nil

	case k8s.KindConfigFold:
		nss, err := workloadNamespaces(ctx, cs, path, cfgUser, cfgSystem)
		if err != nil {
			return nil, err
		}
		cms, err := k8s.ListConfigMapsAcrossNamespaces(ctx, cs, nss)
		if err != nil {
			return nil, err
		}
		return &Children{Kind: k8s.KindConfigMap, ConfigMaps: cms}, nil

	case k8s.KindSecretsFold:
		nss, err := workloadNamespaces(ctx, cs, path, cfgUser, cfgSystem)
		if err != nil {
			return nil, err
		}
		secrets, err := k8s.ListSecretsAcrossNamespaces(ctx, cs, nss)
		if err != nil {
			return nil, err
		}
		return &Children{Kind: k8s.KindSecret, Secrets: secrets}, nil

	case k8s.KindNetworkFold:
		nss, err := workloadNamespaces(ctx, cs, path, cfgUser, cfgSystem)
		if err != nil {
			return nil, err
		}
		svcs, err := k8s.ListServicesAcrossNamespaces(ctx, cs, nss)
		if err != nil {
			return nil, err
		}
		return &Children{Kind: k8s.KindService, Services: svcs}, nil

	case k8s.KindIngressFold:
		nss, err := workloadNamespaces(ctx, cs, path, cfgUser, cfgSystem)
		if err != nil {
			return nil, err
		}
		ings, err := k8s.ListIngressesAcrossNamespaces(ctx, cs, nss)
		if err != nil {
			return nil, err
		}
		return &Children{Kind: k8s.KindIngress, Ingresses: ings}, nil

	case k8s.KindService:
		ings, err := k8s.ListIngressesForService(ctx, cs, focus.Namespace, focus.Name)
		if err != nil {
			return nil, err
		}
		return &Children{Kind: k8s.KindIngress, Ingresses: ings}, nil

	case k8s.KindStorageFold:
		nss, err := workloadNamespaces(ctx, cs, path, cfgUser, cfgSystem)
		if err != nil {
			return nil, err
		}
		pvcs, err := k8s.ListPVCsAcrossNamespaces(ctx, cs, nss)
		if err != nil {
			return nil, err
		}
		return &Children{Kind: k8s.KindPVC, PVCs: pvcs}, nil

	case k8s.KindPVC:
		// Get the PVC to find its bound volume name, then fetch the PV.
		pvc, err := cs.CoreV1().PersistentVolumeClaims(focus.Namespace).Get(ctx, focus.Name, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		pv, err := k8s.GetPVByName(ctx, cs, pvc.Spec.VolumeName)
		if err != nil {
			return nil, err
		}
		pvs := []k8s.PVInfo{}
		if pv != nil {
			pvs = append(pvs, *pv)
		}
		return &Children{Kind: k8s.KindPV, PVs: pvs}, nil

	case k8s.KindCustomFold:
		gvr, ok := parseGVR(focus.GVR)
		if !ok {
			return nil, fmt.Errorf("invalid GVR %q", focus.GVR)
		}
		nsList, err := workloadNamespaces(ctx, cs, path, cfgUser, cfgSystem)
		if err != nil {
			return nil, err
		}
		// Cluster-scope path: list everything (empty namespace). When a
		// namespace folder is in the breadcrumb we got exactly one ns
		// back; pass it through so the dynamic List call is namespaced.
		// "All folder" returns multiple namespaces; we run one list call
		// per namespace and merge. The dynamic client doesn't have an
		// "any namespace" shortcut beyond cluster-scope listing — but
		// listing across the whole cluster from inside the All folder is
		// also fine for user GVRs (RBAC permitting), so we take that
		// fast path when nsList covers every user namespace.
		var items []k8s.GenericResource
		if len(nsList) == 1 {
			items, err = k8s.ListGeneric(ctx, dyn, gvr, nsList[0])
		} else {
			items, err = k8s.ListGeneric(ctx, dyn, gvr, "")
		}
		if err != nil {
			return nil, err
		}
		return &Children{Kind: k8s.KindGenericResource, Generic: items}, nil

	}

	return &Children{Kind: focus.Kind}, nil
}

// parseGVR splits "group/version/resource" back into a schema.GVR. Group
// is allowed to be empty (core). Returns ok=false when the format isn't
// the expected three-part shape — we never want to silently list against
// a bogus GVR.
func parseGVR(s string) (schema.GroupVersionResource, bool) {
	parts := strings.SplitN(s, "/", 3)
	if len(parts) != 3 {
		return schema.GroupVersionResource{}, false
	}
	if parts[1] == "" || parts[2] == "" {
		return schema.GroupVersionResource{}, false
	}
	return schema.GroupVersionResource{Group: parts[0], Version: parts[1], Resource: parts[2]}, true
}

// allNamespaces returns user namespace names — every namespace whose name
// is NOT in sysSet. Used to scope resource listing (ConfigMaps, Secrets,
// Services, PVCs) to the user-visible bucket.
func allNamespaces(ctx context.Context, cs kubernetes.Interface, sysSet map[string]bool) []string {
	list, err := cs.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(list.Items))
	for _, ns := range list.Items {
		if !sysSet[ns.Name] {
			names = append(names, ns.Name)
		}
	}
	return names
}

// workloadNamespaces resolves which namespaces a resource-fold (Workloads,
// Config, Secrets, Network, Storage) should list against, by inspecting the
// drill path. When a NamespaceFolder (or system Namespace) parent is in the
// path the fold is scoped to that single namespace — what the user expects
// after drilling "payments/ ▸ Workloads". Otherwise (inside the All folder)
// it spans every user namespace, mirroring safina's pre-folders behavior.
func workloadNamespaces(ctx context.Context, cs kubernetes.Interface, path []NodeRef, cfgUser, cfgSystem []string) ([]string, error) {
	for i := len(path) - 1; i >= 0; i-- {
		switch path[i].Kind {
		case k8s.KindNamespaceFolder, k8s.KindNamespace:
			return []string{path[i].Name}, nil
		}
	}
	_, sysNs, err := k8s.ListAndClassifyNamespaces(ctx, cs, cfgUser, cfgSystem)
	if err != nil {
		return nil, err
	}
	sysSet := make(map[string]bool, len(sysNs))
	for _, n := range sysNs {
		sysSet[n.Name] = true
	}
	return allNamespaces(ctx, cs, sysSet), nil
}
