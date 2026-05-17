package sections

import (
	"context"
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/yunus/safina/internal/debug"
	"github.com/yunus/safina/internal/k8s"
)

// InspectorDetailMsg is the result envelope for the Inspector tile's
// per-kind detail fetch. The body is the kind-specific snapshot
// (PodDetail / AppDetail / NodeDetail / ServiceDetail / IngressDetail)
// surfaced via a kind-erased interface so the Update switch carries one
// branch instead of five.
//
// Kind / Namespace / Name echo the load target so the model can drop a
// stale result when the user navigated to a different selection between
// fetch dispatch and arrival.
type InspectorDetailMsg struct {
	Kind, Namespace, Name string
	// SubKind matches AppInfo.Kind for App rows (Deployment, StatefulSet,
	// CronJob, …); empty for any other kind. Lets the inspector pick the
	// right per-kind builder when applying the result.
	SubKind string

	// Exactly one of the typed fields below is populated, matching the
	// k8s.Kind* of the row that requested the load. Empty result means
	// the fetch failed (see Err) and the inspector should keep the
	// base-from-list rendering rather than blanking the tile.
	Pod     *k8s.PodDetail
	App     *k8s.AppDetail
	Node    *k8s.NodeDetail
	Service *k8s.ServiceDetail
	Ingress *k8s.IngressDetail
	Err     error
}

// LoadInspectorDetail dispatches a per-kind Get against the API server
// and packages the result as an InspectorDetailMsg. Kinds the inspector
// doesn't deeply enrich (ConfigMap, Secret, PVC, PV — all already small
// enough that the row tells the whole story) return a sentinel "not
// supported" envelope so the caller can short-circuit without an
// API call.
//
// kind is the user-facing tree Kind (k8s.KindPod, KindApp, …); subKind
// is AppInfo.Kind for App rows so the dispatcher can pick deployment
// vs statefulset vs etc. without re-resolving from the tree.
func LoadInspectorDetail(ctx context.Context, mgr *k8s.Manager, contextName, kind, subKind, namespace, name string) tea.Cmd {
	return func() tea.Msg {
		done := debug.Track("k8s.inspect", "context", contextName, "kind", kind, "name", name)
		defer done()
		out := InspectorDetailMsg{Kind: kind, SubKind: subKind, Namespace: namespace, Name: name}
		client, ok := mgr.GetClient(contextName)
		if !ok {
			out.Err = fmt.Errorf("context not found")
			return out
		}
		cs := client.Clientset
		switch kind {
		case k8s.KindPod:
			d, err := k8s.InspectPod(ctx, cs, namespace, name)
			out.Err = err
			if err == nil {
				out.Pod = &d
			}
		case k8s.KindApp:
			d, err := k8s.InspectApp(ctx, cs, subKind, namespace, name)
			out.Err = err
			if err == nil {
				out.App = &d
			}
		case k8s.KindNode:
			d, err := k8s.InspectNode(ctx, cs, name)
			out.Err = err
			if err == nil {
				out.Node = &d
			}
		case k8s.KindService:
			d, err := k8s.InspectService(ctx, cs, namespace, name)
			out.Err = err
			if err == nil {
				out.Service = &d
			}
		case k8s.KindIngress:
			d, err := k8s.InspectIngress(ctx, cs, namespace, name)
			out.Err = err
			if err == nil {
				out.Ingress = &d
			}
		}
		return out
	}
}

// inspectorDetailKey returns the cache key for a detail result. We key
// on the tuple the load target carries so two rows sharing a name in
// different namespaces don't collide.
func inspectorDetailKey(kind, namespace, name string) string {
	return kind + "/" + namespace + "/" + name
}
