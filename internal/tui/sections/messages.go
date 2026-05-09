package sections

import (
	"context"
	"fmt"

	tea "charm.land/bubbletea/v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
		ch, err := loadChildren(ctx, client.Clientset, path, userNS, systemNS)
		return TreeChildrenLoadedMsg{Path: path, Children: ch, Err: err}
	}
}

func loadChildren(ctx context.Context, cs kubernetes.Interface, path []NodeRef, cfgUser, cfgSystem []string) (*Children, error) {
	focus := path[len(path)-1]

	switch focus.Kind {
	case k8s.KindContextList:
		// The context list is pre-seeded into the cache by the root model
		// from the kubeconfig — there's no API call to make here. Returning
		// empty children is a safety net; in practice the cache hits first.
		return &Children{Kind: k8s.KindContextList}, nil

	case k8s.KindContext:
		// Drilling into a single cluster: same payload as the previous
		// "Home" root — user apps, with Cluster + System virtual rows
		// composed in by the Tree section's visibleRows.
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
			Kind:        k8s.KindApp,
			Apps:        apps,
			SystemCount: len(sysNames),
			SystemNS:    sysNames,
		}, nil

	case k8s.KindClusterFold:
		pools, _, err := k8s.ListNodesAndPools(ctx, cs)
		if err != nil {
			return nil, err
		}
		return &Children{Kind: k8s.KindNodePool, Pools: pools}, nil

	case k8s.KindSystemFold:
		_, sysNs, err := k8s.ListAndClassifyNamespaces(ctx, cs, cfgUser, cfgSystem)
		if err != nil {
			return nil, err
		}
		return &Children{Kind: k8s.KindNamespace, Namespaces: sysNs}, nil

	case k8s.KindNamespace:
		// Drilling into a single namespace lists its apps.
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
		_, sysNs, err := k8s.ListAndClassifyNamespaces(ctx, cs, cfgUser, cfgSystem)
		if err != nil {
			return nil, err
		}
		sysSet := make(map[string]bool, len(sysNs))
		for _, n := range sysNs {
			sysSet[n.Name] = true
		}
		cms, err := k8s.ListConfigMapsAcrossNamespaces(ctx, cs, allNamespaces(ctx, cs, sysSet))
		if err != nil {
			return nil, err
		}
		return &Children{Kind: k8s.KindConfigMap, ConfigMaps: cms}, nil

	case k8s.KindSecretsFold:
		_, sysNs, err := k8s.ListAndClassifyNamespaces(ctx, cs, cfgUser, cfgSystem)
		if err != nil {
			return nil, err
		}
		sysSet := make(map[string]bool, len(sysNs))
		for _, n := range sysNs {
			sysSet[n.Name] = true
		}
		secrets, err := k8s.ListSecretsAcrossNamespaces(ctx, cs, allNamespaces(ctx, cs, sysSet))
		if err != nil {
			return nil, err
		}
		return &Children{Kind: k8s.KindSecret, Secrets: secrets}, nil

	case k8s.KindNetworkFold:
		_, sysNs, err := k8s.ListAndClassifyNamespaces(ctx, cs, cfgUser, cfgSystem)
		if err != nil {
			return nil, err
		}
		sysSet := make(map[string]bool, len(sysNs))
		for _, n := range sysNs {
			sysSet[n.Name] = true
		}
		svcs, err := k8s.ListServicesAcrossNamespaces(ctx, cs, allNamespaces(ctx, cs, sysSet))
		if err != nil {
			return nil, err
		}
		return &Children{Kind: k8s.KindService, Services: svcs}, nil

	case k8s.KindService:
		ings, err := k8s.ListIngressesForService(ctx, cs, focus.Namespace, focus.Name)
		if err != nil {
			return nil, err
		}
		return &Children{Kind: k8s.KindIngress, Ingresses: ings}, nil

	case k8s.KindStorageFold:
		_, sysNs, err := k8s.ListAndClassifyNamespaces(ctx, cs, cfgUser, cfgSystem)
		if err != nil {
			return nil, err
		}
		sysSet := make(map[string]bool, len(sysNs))
		for _, n := range sysNs {
			sysSet[n.Name] = true
		}
		pvcs, err := k8s.ListPVCsAcrossNamespaces(ctx, cs, allNamespaces(ctx, cs, sysSet))
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
	}

	return &Children{Kind: focus.Kind}, nil
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

func DeletePod(ctx context.Context, mgr *k8s.Manager, contextName, namespace, podName string) tea.Cmd {
	return func() tea.Msg {
		client, ok := mgr.GetClient(contextName)
		if !ok {
			return ActionDoneMsg{Action: "delete", Subject: podName, Err: fmt.Errorf("context not found")}
		}
		err := k8s.DeletePod(ctx, client.Clientset, namespace, podName)
		return ActionDoneMsg{Action: "delete", Subject: podName, Err: err}
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
