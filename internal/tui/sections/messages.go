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

// ── Messages emitted by sections ──────────────────────────────────────

// PodSelectedMsg is emitted when the user activates a pod (Enter / l).
type PodSelectedMsg struct {
	Name, Namespace, Container string
}

// TreeFocusChangedMsg is emitted by the Tree section after Push/Pop. The
// root model uses it to schedule a load for the new focus.
type TreeFocusChangedMsg struct {
	Path []NodeRef
}

// TreeChildrenLoadedMsg carries a freshly fetched payload for a tree level.
type TreeChildrenLoadedMsg struct {
	Path     []NodeRef
	Children *Children
	Err      error
}

// ── Loader / action commands and their result messages ────────────────

// ActionDoneMsg is the result envelope for every mutating call we issue
// against the cluster (delete, scale, restart, rollback, …). Subject is the
// human label of the affected resource — surfaced in the success banner so
// the operator gets visible confirmation of what just happened. Action is
// the verb (must match the keymap label so the success banner reads in the
// same vocabulary the user pressed: Jakob's Law).
type ActionDoneMsg struct {
	Action  string
	Subject string
	Err     error
}

type LogStreamStartedMsg struct {
	Pod string
	Ch  chan k8s.PodLogEvent
}

// EventsLoadedMsg is delivered when the events fetch completes for the
// resource currently shown in the events modal.
type EventsLoadedMsg struct {
	Events []k8s.EventInfo
	Err    error
}

// HotLoadedMsg carries the top-N pods sorted by CPU usage for the hot-list
// modal. Mirrors EventsLoadedMsg's shape so the same load-flow patterns
// apply: SetItems(items, err) on the section, done.
type HotLoadedMsg struct {
	Pods []k8s.PodInfo
	Err  error
}

// DescribeLoadedMsg carries the YAML dump for the resource currently shown
// in the describe modal.
type DescribeLoadedMsg struct {
	YAML string
	Err  error
}

// EditYAMLLoadedMsg is the result of fetching a resource's YAML for editing.
// The root model handles it by writing YAML to a tempfile and launching
// $EDITOR via tea.ExecProcess. Kind/Namespace/Name echo back so the next
// step (apply) doesn't have to re-resolve them from the tree.
type EditYAMLLoadedMsg struct {
	Kind, Namespace, Name string
	YAML                  string
	Err                   error
}

// EditorExitedMsg is emitted by the tea.ExecProcess callback once the user
// closes their editor. The model reads the tempfile, diffs it against the
// original, and either banners (no changes / parse error) or transitions to
// the edit-confirm modal.
type EditorExitedMsg struct {
	Err error
}

// ShellSessionStartedMsg is emitted once the remotecommand stream has been
// dialed. The Shell section attaches to Session and the root pump-loop
// starts pulling ShellEvent messages from it.
type ShellSessionStartedMsg struct {
	Session *k8s.ShellSession
	IsDebug bool // true when this came from AttachDebug (netshoot fall-back)
}

// ShellOutputMsg carries one chunk of stdout/stderr bytes from the running
// shell. The section feeds it into the vt10x emulator.
type ShellOutputMsg struct {
	Bytes []byte
}

// ShellExitMsg is emitted when the shell session ends — either cleanly
// (user typed `exit`) or via an error. NoShell signals the distroless /
// scratch case where we should offer the netshoot debug-pod fall-back.
// Bytes carries any final buffered output that was greedy-drained alongside
// the Done event in the same Cmd; the consumer ingests Bytes before
// processing the exit so the pod's last words aren't dropped.
type ShellExitMsg struct {
	Bytes   []byte
	Err     error
	NoShell bool
}

// LoadDescribe fetches the YAML representation of a single resource. Used by
// the describe modal.
func LoadDescribe(ctx context.Context, mgr *k8s.Manager, contextName, namespace, kind, name string) tea.Cmd {
	return func() tea.Msg {
		done := debug.Track("k8s.describe", "context", contextName, "kind", kind, "name", name)
		defer done()
		client, ok := mgr.GetClient(contextName)
		if !ok {
			return DescribeLoadedMsg{Err: fmt.Errorf("context not found")}
		}
		yaml, err := k8s.DescribeYAML(ctx, client.Clientset, namespace, kind, name)
		return DescribeLoadedMsg{YAML: yaml, Err: err}
	}
}

// LoadSecretReveal fetches a Secret with its Data base64-decoded into
// stringData and emits a DescribeLoadedMsg so the describe modal can render
// it like any other YAML dump. Bound to the Reveal key on Secret rows.
func LoadSecretReveal(ctx context.Context, mgr *k8s.Manager, contextName, namespace, name string) tea.Cmd {
	return func() tea.Msg {
		done := debug.Track("k8s.reveal_secret", "context", contextName, "namespace", namespace, "name", name)
		defer done()
		client, ok := mgr.GetClient(contextName)
		if !ok {
			return DescribeLoadedMsg{Err: fmt.Errorf("context not found")}
		}
		yaml, err := k8s.RevealSecretYAML(ctx, client.Clientset, namespace, name)
		return DescribeLoadedMsg{YAML: yaml, Err: err}
	}
}

// LoadEditYAML fetches the YAML for a resource the user wants to edit. The
// payload (with namespace/kind/name echoed back) is delivered as
// EditYAMLLoadedMsg; the root model then writes it to a tempfile and hands
// off to $EDITOR.
func LoadEditYAML(ctx context.Context, mgr *k8s.Manager, contextName, namespace, kind, name string) tea.Cmd {
	return func() tea.Msg {
		done := debug.Track("k8s.edit_load", "context", contextName, "kind", kind, "name", name)
		defer done()
		client, ok := mgr.GetClient(contextName)
		if !ok {
			return EditYAMLLoadedMsg{Kind: kind, Namespace: namespace, Name: name, Err: fmt.Errorf("context not found")}
		}
		yaml, err := k8s.BuildEditableYAML(ctx, client.Clientset, kind, namespace, name)
		return EditYAMLLoadedMsg{Kind: kind, Namespace: namespace, Name: name, YAML: yaml, Err: err}
	}
}

// ApplyEdit submits an edited YAML buffer back to the API server. Wraps the
// k8s.ApplyEditedYAML call in the standard ActionDoneMsg envelope so the
// success/error banner pipeline lights up exactly like every other mutating
// action (delete, scale, restart, …).
func ApplyEdit(ctx context.Context, mgr *k8s.Manager, contextName, namespace, kind, name string, data []byte) tea.Cmd {
	return func() tea.Msg {
		subject := fmt.Sprintf("%s/%s", kind, name)
		done := debug.Track("k8s.edit_apply", "context", contextName, "kind", kind, "name", name)
		defer done()
		client, ok := mgr.GetClient(contextName)
		if !ok {
			return ActionDoneMsg{Action: "edit", Subject: subject, Err: fmt.Errorf("context not found")}
		}
		err := k8s.ApplyEditedYAML(ctx, client.Clientset, kind, namespace, name, data)
		return ActionDoneMsg{Action: "edit", Subject: subject, Err: err}
	}
}

// hotListSize is the cap on rows shown in the hot-list modal. Twenty is
// "more than a screen but not so many that the operator has to scan a
// page of mostly-quiet pods" — the noisy-neighbour case usually surfaces
// in the first 3-5 rows. Defining it here (not in the section) so the
// loader can pre-truncate at the network edge.
const hotListSize = 20

// LoadHot fetches a fresh top-N-by-CPU pod list across the whole cluster.
// Cluster-scoped list call + metrics-server fetch, then sort + truncate.
// Pods without metrics (metrics-server unavailable, or pod hasn't reported
// yet) are excluded — the hot list is meaningless if you can't sort by
// usage, and rendering them at the top with "—" would mislead the operator.
func LoadHot(ctx context.Context, mgr *k8s.Manager, contextName string) tea.Cmd {
	return func() tea.Msg {
		done := debug.Track("k8s.hot", "context", contextName)
		defer done()
		client, ok := mgr.GetClient(contextName)
		if !ok {
			return HotLoadedMsg{Err: fmt.Errorf("context not found")}
		}
		pods, err := k8s.ListHotPodsByCPU(ctx, client.Clientset, hotListSize)
		return HotLoadedMsg{Pods: pods, Err: err}
	}
}

// LoadEvents fetches the event list for involvedObject = kind/name in
// namespace. Newest first.
func LoadEvents(ctx context.Context, mgr *k8s.Manager, contextName, namespace, kind, name string) tea.Cmd {
	return func() tea.Msg {
		done := debug.Track("k8s.events", "context", contextName, "kind", kind, "name", name)
		defer done()
		client, ok := mgr.GetClient(contextName)
		if !ok {
			return EventsLoadedMsg{Err: fmt.Errorf("context not found")}
		}
		events, err := k8s.ListEvents(ctx, client.Clientset, namespace, kind, name)
		return EventsLoadedMsg{Events: events, Err: err}
	}
}

// DiscoveryLoadedMsg carries the list of GVRs the cluster exposes,
// populated once per cluster switch (lazy on first open of the Resource
// Browser). The root model stores the list on the Tree so future fold
// rendering can use real Kind names from discovery.
type DiscoveryLoadedMsg struct {
	Kinds []k8s.ResourceKind
	Err   error
}

// LoadDiscovery enumerates every GVR the API server exposes. Best-
// effort — partial discovery (one CRD group wedged) still returns
// useful data.
func LoadDiscovery(ctx context.Context, mgr *k8s.Manager, contextName string) tea.Cmd {
	return func() tea.Msg {
		done := debug.Track("k8s.discovery", "context", contextName)
		defer done()
		client, ok := mgr.GetClient(contextName)
		if !ok {
			return DiscoveryLoadedMsg{Err: fmt.Errorf("context not found")}
		}
		kinds, err := k8s.ListAvailableResources(client.Discovery)
		return DiscoveryLoadedMsg{Kinds: kinds, Err: err}
	}
}

// LoadTreeChildren fetches the children for a tree focus path. Dispatches
// on the focus kind. Always returns TreeChildrenLoadedMsg — empty payload
// on error so the UI can clear its loading state.
func LoadTreeChildren(ctx context.Context, mgr *k8s.Manager, contextName string, path []NodeRef, userNS, systemNS []string) tea.Cmd {
	return func() tea.Msg {
		focus := path[len(path)-1]
		done := debug.Track("k8s.load_children", "context", contextName, "kind", string(focus.Kind), "name", focus.Name)
		defer done()
		client, ok := mgr.GetClient(contextName)
		if !ok {
			return TreeChildrenLoadedMsg{Path: path, Err: fmt.Errorf("context not found")}
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

// DeleteResource issues a cluster-side delete for any kind the tree
// surfaces. Subject is just the bare name — the success/failure banner
// reads "deleted · frontend" without the kind, matching how the user
// referred to the row when they pressed the verb.
func DeleteResource(ctx context.Context, mgr *k8s.Manager, contextName, kind, namespace, name string) tea.Cmd {
	return func() tea.Msg {
		client, ok := mgr.GetClient(contextName)
		if !ok {
			return ActionDoneMsg{Action: "delete", Subject: name, Err: fmt.Errorf("context not found")}
		}
		err := k8s.DeleteResource(ctx, client.Clientset, kind, namespace, name)
		return ActionDoneMsg{Action: "delete", Subject: name, Err: err}
	}
}

func ScaleDeployment(ctx context.Context, mgr *k8s.Manager, contextName, namespace, name string, replicas int32) tea.Cmd {
	return func() tea.Msg {
		subject := fmt.Sprintf("%s → %d", name, replicas)
		client, ok := mgr.GetClient(contextName)
		if !ok {
			return ActionDoneMsg{Action: "scale", Subject: subject, Err: fmt.Errorf("context not found")}
		}
		err := k8s.ScaleDeployment(ctx, client.Clientset, namespace, name, replicas)
		return ActionDoneMsg{Action: "scale", Subject: subject, Err: err}
	}
}

func RollbackDeployment(ctx context.Context, mgr *k8s.Manager, contextName, namespace, name string) tea.Cmd {
	return func() tea.Msg {
		client, ok := mgr.GetClient(contextName)
		if !ok {
			return ActionDoneMsg{Action: "rollback", Subject: name, Err: fmt.Errorf("context not found")}
		}
		err := k8s.RollbackDeployment(ctx, client.Clientset, namespace, name)
		return ActionDoneMsg{Action: "rollback", Subject: name, Err: err}
	}
}

// StreamAppLogs resolves a streamable pod for the given App (Deployment /
// StatefulSet / DaemonSet / CronJob / Job) and emits a PodSelectedMsg, which
// the root model already handles by switching to the log view and starting
// a stream. Lets the user press `l` on an App row at any level (root or
// drilled-in) without first opening the pod list.
//
// Pod selection: prefer the first Running pod; fall back to the first pod
// that exists. Reports an ActionDoneMsg error when no pod is found.
func StreamAppLogs(ctx context.Context, mgr *k8s.Manager, contextName string, app k8s.AppRef) tea.Cmd {
	return func() tea.Msg {
		client, ok := mgr.GetClient(contextName)
		if !ok {
			return ActionDoneMsg{Action: "logs", Err: fmt.Errorf("context not found")}
		}
		pods, err := k8s.ListPodsForApp(ctx, client.Clientset, app)
		if err != nil {
			return ActionDoneMsg{Action: "logs", Err: err}
		}
		if len(pods) == 0 {
			return ActionDoneMsg{Action: "logs", Err: fmt.Errorf("no pods for %s", app.Name)}
		}
		target := pods[0]
		for _, p := range pods {
			if p.Status == "Running" {
				target = p
				break
			}
		}
		return PodSelectedMsg{Name: target.Name, Namespace: target.Namespace}
	}
}

func StreamLogs(ctx context.Context, mgr *k8s.Manager, contextName, namespace, podName, container string) tea.Cmd {
	return func() tea.Msg {
		client, ok := mgr.GetClient(contextName)
		if !ok {
			return ActionDoneMsg{Action: "logs", Err: fmt.Errorf("context not found")}
		}
		ch := make(chan k8s.PodLogEvent)
		go k8s.StreamPodContainerLogs(ctx, client.Clientset, namespace, podName, container, false, ch)
		return LogStreamStartedMsg{Pod: podName, Ch: ch}
	}
}

// OpenShell dials a remotecommand exec stream into the named pod/container.
// Mirrors StreamLogs in shape but routes dial-time errors through
// ShellExitMsg so the modal flips to phaseError instead of leaving the
// "connecting…" spinner spinning forever.
func OpenShell(ctx context.Context, mgr *k8s.Manager, contextName, namespace, pod, container string) tea.Cmd {
	return func() tea.Msg {
		client, ok := mgr.GetClient(contextName)
		if !ok {
			return ShellExitMsg{Err: fmt.Errorf("context not found")}
		}
		sess, err := k8s.ExecPod(ctx, client.Clientset, client.RESTConfig, namespace, pod, container, nil)
		if err != nil {
			return ShellExitMsg{Err: err}
		}
		return ShellSessionStartedMsg{Session: sess}
	}
}

// AttachDebug creates a netshoot ephemeral container targeting the named
// container and attaches an interactive session to it. Used as the fall-back
// when ExecPod fails because the container has no shell.
func AttachDebug(ctx context.Context, mgr *k8s.Manager, contextName, namespace, pod, targetContainer, image string) tea.Cmd {
	return func() tea.Msg {
		client, ok := mgr.GetClient(contextName)
		if !ok {
			return ShellExitMsg{Err: fmt.Errorf("context not found")}
		}
		sess, err := k8s.AttachDebugContainer(ctx, client.Clientset, client.RESTConfig, namespace, pod, targetContainer, image)
		if err != nil {
			return ShellExitMsg{Err: err}
		}
		return ShellSessionStartedMsg{Session: sess, IsDebug: true}
	}
}

// RestartPod kills a single Pod so its parent controller recreates it.
// At the API level this is a delete; we wrap it here so the success banner
// reads "restart · podname" — matching the verb the keymap and footer
// taught, instead of the lower-level "delete" emitted by DeleteResource.
func RestartPod(ctx context.Context, mgr *k8s.Manager, contextName, namespace, name string) tea.Cmd {
	return func() tea.Msg {
		client, ok := mgr.GetClient(contextName)
		if !ok {
			return ActionDoneMsg{Action: "restart", Subject: name, Err: fmt.Errorf("context not found")}
		}
		err := k8s.DeleteResource(ctx, client.Clientset, "Pod", namespace, name)
		return ActionDoneMsg{Action: "restart", Subject: name, Err: err}
	}
}

// RestartApp triggers a rolling restart for an App by patching the pod
// template's annotations. Works for Deployment / StatefulSet / DaemonSet.
func RestartApp(ctx context.Context, mgr *k8s.Manager, contextName, namespace, kind, name string) tea.Cmd {
	return func() tea.Msg {
		client, ok := mgr.GetClient(contextName)
		if !ok {
			return ActionDoneMsg{Action: "restart", Subject: name, Err: fmt.Errorf("context not found")}
		}
		err := k8s.RestartWorkload(ctx, client.Clientset, namespace, kind, name)
		return ActionDoneMsg{Action: "restart", Subject: name, Err: err}
	}
}
