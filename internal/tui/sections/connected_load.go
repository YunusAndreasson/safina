package sections

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"github.com/yunus/safina/internal/debug"
	"github.com/yunus/safina/internal/k8s"
)

// ConnectedMsg is the result envelope for a Connected-tile fetch. Kind /
// Namespace / Name echo the load target so the section can drop a stale
// result when the user navigated to a different selection between dispatch
// and arrival.
type ConnectedMsg struct {
	Kind, SubKind, Namespace, Name string
	Connections                    k8s.Connections
	Err                            error
}

// LoadConnections dispatches the kind-specific connections resolver and
// packages the result as a ConnectedMsg. Unsupported kinds short-circuit at
// the section layer; the load command is only issued when the section's
// SetSelection actually needs the data.
func LoadConnections(ctx context.Context, mgr *k8s.Manager, contextName, kind, subKind, namespace, name string) tea.Cmd {
	return func() tea.Msg {
		defer debug.Track("k8s.connections", "context", contextName, "kind", kind, "name", name)()
		out := ConnectedMsg{Kind: kind, SubKind: subKind, Namespace: namespace, Name: name}
		client, ok := mgr.GetClient(contextName)
		if !ok {
			out.Err = errContextNotFound
			return out
		}
		cs := client.Clientset
		var (
			conns k8s.Connections
			err   error
		)
		switch kind {
		case k8s.KindApp:
			conns, err = k8s.ConnectionsForWorkload(ctx, cs, subKind, namespace, name)
		case k8s.KindPod:
			conns, err = k8s.ConnectionsForPod(ctx, cs, namespace, name)
		case k8s.KindService:
			conns, err = k8s.ConnectionsForService(ctx, cs, namespace, name)
		case k8s.KindIngress:
			conns, err = k8s.ConnectionsForIngress(ctx, cs, namespace, name)
		case k8s.KindConfigMap:
			conns, err = k8s.ConnectionsForConfigMap(ctx, cs, namespace, name)
		case k8s.KindSecret:
			conns, err = k8s.ConnectionsForSecret(ctx, cs, namespace, name)
		case k8s.KindServiceAccount:
			conns, err = k8s.ConnectionsForServiceAccount(ctx, cs, namespace, name)
		case k8s.KindHPA:
			conns, err = k8s.ConnectionsForHPA(ctx, cs, namespace, name)
		}
		out.Connections = conns
		out.Err = err
		return out
	}
}
