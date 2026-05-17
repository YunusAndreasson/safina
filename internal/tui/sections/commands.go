package sections

import (
	"context"
	"errors"
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/yunus/safina/internal/debug"
	"github.com/yunus/safina/internal/k8s"
)

// Cmd builders that fetch from / mutate the cluster and emit the message
// types declared in messages.go. The tree-children loader lives in
// tree_children.go because its dispatch switch is large enough to deserve
// its own file.

// errContextNotFound is returned by every loader/command when the requested
// kube context is missing from the manager's cache. One sentinel so the
// banner pipeline sees a consistent error string regardless of which loader
// tripped over the missing client.
var errContextNotFound = errors.New("context not found")

// hotListSize is the cap on rows shown in the hot-list modal. Twenty is
// "more than a screen but not so many that the operator has to scan a
// page of mostly-quiet pods" — the noisy-neighbour case usually surfaces
// in the first 3-5 rows. Defining it here (not in the section) so the
// loader can pre-truncate at the network edge.
const hotListSize = 20

// actionCmd wraps a mutating API call into an ActionDoneMsg envelope.
// Centralises the GetClient → "context not found" boilerplate so each verb
// reads as one line of intent + one line of dispatch.
func actionCmd(mgr *k8s.Manager, contextName, action, subject string, do func(*k8s.Client) error) tea.Cmd {
	return func() tea.Msg {
		client, ok := mgr.GetClient(contextName)
		if !ok {
			return ActionDoneMsg{Action: action, Subject: subject, Err: errContextNotFound}
		}
		return ActionDoneMsg{Action: action, Subject: subject, Err: do(client)}
	}
}

// LoadDescribe fetches the YAML representation of a single resource. Used by
// the describe modal.
func LoadDescribe(ctx context.Context, mgr *k8s.Manager, contextName, namespace, kind, name string) tea.Cmd {
	return func() tea.Msg {
		defer debug.Track("k8s.describe", "context", contextName, "kind", kind, "name", name)()
		client, ok := mgr.GetClient(contextName)
		if !ok {
			return DescribeLoadedMsg{Err: errContextNotFound}
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
		defer debug.Track("k8s.reveal_secret", "context", contextName, "namespace", namespace, "name", name)()
		client, ok := mgr.GetClient(contextName)
		if !ok {
			return DescribeLoadedMsg{Err: errContextNotFound}
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
		defer debug.Track("k8s.edit_load", "context", contextName, "kind", kind, "name", name)()
		client, ok := mgr.GetClient(contextName)
		if !ok {
			return EditYAMLLoadedMsg{Kind: kind, Namespace: namespace, Name: name, Err: errContextNotFound}
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
		defer debug.Track("k8s.edit_apply", "context", contextName, "kind", kind, "name", name)()
		subject := fmt.Sprintf("%s/%s", kind, name)
		client, ok := mgr.GetClient(contextName)
		if !ok {
			return ActionDoneMsg{Action: "edit", Subject: subject, Err: errContextNotFound}
		}
		err := k8s.ApplyEditedYAML(ctx, client.Clientset, kind, namespace, name, data)
		return ActionDoneMsg{Action: "edit", Subject: subject, Err: err}
	}
}

// LoadHot fetches a fresh top-N-by-CPU pod list across the whole cluster.
// Cluster-scoped list call + metrics-server fetch, then sort + truncate.
// Pods without metrics (metrics-server unavailable, or pod hasn't reported
// yet) are excluded — the hot list is meaningless if you can't sort by
// usage, and rendering them at the top with "—" would mislead the operator.
func LoadHot(ctx context.Context, mgr *k8s.Manager, contextName string) tea.Cmd {
	return func() tea.Msg {
		defer debug.Track("k8s.hot", "context", contextName)()
		client, ok := mgr.GetClient(contextName)
		if !ok {
			return HotLoadedMsg{Err: errContextNotFound}
		}
		pods, err := k8s.ListHotPodsByCPU(ctx, client.Clientset, hotListSize)
		return HotLoadedMsg{Pods: pods, Err: err}
	}
}

// LoadEvents fetches the event list for involvedObject = kind/name in
// namespace. Newest first.
func LoadEvents(ctx context.Context, mgr *k8s.Manager, contextName, namespace, kind, name string) tea.Cmd {
	return func() tea.Msg {
		defer debug.Track("k8s.events", "context", contextName, "kind", kind, "name", name)()
		client, ok := mgr.GetClient(contextName)
		if !ok {
			return EventsLoadedMsg{Err: errContextNotFound}
		}
		events, err := k8s.ListEvents(ctx, client.Clientset, namespace, kind, name)
		return EventsLoadedMsg{Events: events, Err: err}
	}
}

// LoadDiscovery enumerates every GVR the API server exposes. Best-
// effort — partial discovery (one CRD group wedged) still returns
// useful data.
func LoadDiscovery(ctx context.Context, mgr *k8s.Manager, contextName string) tea.Cmd {
	return func() tea.Msg {
		defer debug.Track("k8s.discovery", "context", contextName)()
		client, ok := mgr.GetClient(contextName)
		if !ok {
			return DiscoveryLoadedMsg{Err: errContextNotFound}
		}
		kinds, err := k8s.ListAvailableResources(client.Discovery)
		return DiscoveryLoadedMsg{Kinds: kinds, Err: err}
	}
}

// DeleteResource issues a cluster-side delete for any kind the tree
// surfaces. Subject is just the bare name — the success/failure banner
// reads "deleted · frontend" without the kind, matching how the user
// referred to the row when they pressed the verb.
func DeleteResource(ctx context.Context, mgr *k8s.Manager, contextName, kind, namespace, name string) tea.Cmd {
	return actionCmd(mgr, contextName, "delete", name, func(c *k8s.Client) error {
		return k8s.DeleteResource(ctx, c.Clientset, kind, namespace, name)
	})
}

func ScaleDeployment(ctx context.Context, mgr *k8s.Manager, contextName, namespace, name string, replicas int32) tea.Cmd {
	return actionCmd(mgr, contextName, "scale", fmt.Sprintf("%s → %d", name, replicas), func(c *k8s.Client) error {
		return k8s.ScaleDeployment(ctx, c.Clientset, namespace, name, replicas)
	})
}

func RollbackDeployment(ctx context.Context, mgr *k8s.Manager, contextName, namespace, name string) tea.Cmd {
	return actionCmd(mgr, contextName, "rollback", name, func(c *k8s.Client) error {
		return k8s.RollbackDeployment(ctx, c.Clientset, namespace, name)
	})
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
			return ActionDoneMsg{Action: "logs", Err: errContextNotFound}
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
			return ActionDoneMsg{Action: "logs", Err: errContextNotFound}
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
			return ShellExitMsg{Err: errContextNotFound}
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
			return ShellExitMsg{Err: errContextNotFound}
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
	return actionCmd(mgr, contextName, "restart", name, func(c *k8s.Client) error {
		return k8s.DeleteResource(ctx, c.Clientset, "Pod", namespace, name)
	})
}

// RestartApp triggers a rolling restart for an App by patching the pod
// template's annotations. Works for Deployment / StatefulSet / DaemonSet.
func RestartApp(ctx context.Context, mgr *k8s.Manager, contextName, namespace, kind, name string) tea.Cmd {
	return actionCmd(mgr, contextName, "restart", name, func(c *k8s.Client) error {
		return k8s.RestartWorkload(ctx, c.Clientset, namespace, kind, name)
	})
}
