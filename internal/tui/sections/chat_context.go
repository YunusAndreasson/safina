package sections

import (
	"fmt"
	"strings"

	"github.com/YunusAndreasson/safina/internal/k8s"
)

// ContextSnapshot is the structured "what is the user looking at" payload
// that gets prepended to every chat message. Built from the live tree state
// at send-time so it always reflects the latest navigation, not whatever was
// selected when the user started typing.
type ContextSnapshot struct {
	// Crumbs is the breadcrumb in display order — for the strip at the top
	// of the chat panel. Each crumb is (glyph, label).
	Crumbs []ContextCrumb

	// Block is the multi-line "[Selection]" payload prepended to the prompt.
	// Empty when there's nothing useful to attach (e.g. at the cluster picker
	// before any drill).
	Block string
}

// ContextCrumb is one named segment of the context strip.
type ContextCrumb struct {
	Label string
}

// BuildContextSnapshot inspects the tree's current path + selection and
// renders both the visible breadcrumb (Crumbs) and the textual block sent
// to Claude (Block).
//
// Design choice: we ship structured key:value lines, not YAML. YAML
// indentation rules don't help an LLM and the [Selection] header makes the
// scope unambiguous. We omit anything we don't actually have on hand —
// inventing fields would just waste tokens.
func BuildContextSnapshot(t *Tree, activeContext string) ContextSnapshot {
	var snap ContextSnapshot

	// Build crumbs from the path. The root "Clusters" node is visual chrome —
	// drop it so the strip starts with the cluster the user is in.
	for _, ref := range t.Path() {
		if ref.Kind == k8s.KindContextList {
			continue
		}
		snap.Crumbs = append(snap.Crumbs, ContextCrumb{
			Label: ref.Display,
		})
	}
	// Add the highlighted child as the trailing crumb (the user almost
	// always means "this row" when they ask a question).
	sel := t.SelectedRef()
	if sel.Kind != "" && sel.Kind != k8s.KindContextList {
		snap.Crumbs = append(snap.Crumbs, ContextCrumb{
			Label: sel.Display,
		})
	}

	// Block — kind-specific. Walk the path so we can always include the
	// cluster + namespace context even when the highlighted row is deeper.
	var b strings.Builder
	b.WriteString("[Selection]\n")

	if activeContext != "" {
		fmt.Fprintf(&b, "cluster: %s\n", activeContext)
	}
	if ns := pathNamespace(t.Path(), sel); ns != "" {
		fmt.Fprintf(&b, "namespace: %s\n", ns)
	}
	if app, ok := pathApp(t); ok {
		// Prefer SubKind ("Deployment", "StatefulSet", …) over the generic
		// KindApp tag — Claude needs the concrete workload kind, not "app".
		kind := app.SubKind
		if kind == "" {
			kind = app.Kind
		}
		fmt.Fprintf(&b, "%s: %s\n", strings.ToLower(kind), app.Name)
	}

	switch sel.Kind {
	case k8s.KindApp:
		if app, ok := t.SelectedAppInfo(); ok {
			writeApp(&b, app)
		}
	case k8s.KindPod:
		if pod, ok := t.SelectedPodInfo(); ok {
			writePod(&b, pod)
		}
	case k8s.KindContainer:
		if cs := t.Children(); cs != nil {
			rows := t.visibleRows()
			ci := -1
			for i, r := range rows {
				if r.Kind == sel.Kind && r.Name == sel.Name {
					ci = i
					break
				}
			}
			if ci >= 0 && ci < len(cs.Containers) {
				writeContainer(&b, cs.Containers[ci])
			}
		}
	case k8s.KindNode:
		if cs := t.Children(); cs != nil {
			for _, n := range cs.Nodes {
				if n.Name == sel.Name {
					writeNode(&b, n)
					break
				}
			}
		}
	case k8s.KindNodePool:
		if cs := t.Children(); cs != nil {
			for _, p := range cs.Pools {
				if p.Name == sel.Name {
					writePool(&b, p)
					break
				}
			}
		}
	case k8s.KindNamespace, k8s.KindNamespaceFolder:
		fmt.Fprintf(&b, "selected: namespace/%s\n", sel.Name)
	case k8s.KindContext:
		fmt.Fprintf(&b, "selected: cluster picker (no resource yet)\n")
	}

	if b.Len() > len("[Selection]\n") {
		snap.Block = strings.TrimRight(b.String(), "\n")
	}
	return snap
}

func writeApp(b *strings.Builder, a k8s.AppInfo) {
	fmt.Fprintf(b, "%s/%s:\n", strings.ToLower(a.Kind), a.Name)
	fmt.Fprintf(b, "  health: %s\n", healthLabel(a.Health))
	if a.Ready != "" {
		fmt.Fprintf(b, "  ready: %s\n", a.Ready)
	}
	if a.Age != "" {
		fmt.Fprintf(b, "  age: %s\n", a.Age)
	}
	if a.CPU > 0 || a.Memory > 0 {
		fmt.Fprintf(b, "  cpu: %s  mem: %s\n", k8s.FormatCPU(a.CPU), k8s.FormatMem(a.Memory))
	}
	if a.HPA != nil {
		fmt.Fprintf(b, "  hpa: current=%d min=%d max=%d", a.HPA.Current, a.HPA.Min, a.HPA.Max)
		if a.HPA.PinnedAtMax {
			b.WriteString(" pinned_at_max=true")
		}
		b.WriteByte('\n')
	}
	if a.NextSchedule != "" {
		fmt.Fprintf(b, "  next: %s\n", a.NextSchedule)
	}
}

func writePod(b *strings.Builder, p k8s.PodInfo) {
	fmt.Fprintf(b, "pod/%s:\n", p.Name)
	fmt.Fprintf(b, "  status: %s\n", p.Status)
	fmt.Fprintf(b, "  restarts: %d\n", p.Restarts)
	if p.Age != "" {
		fmt.Fprintf(b, "  age: %s\n", p.Age)
	}
	if p.NodeName != "" {
		fmt.Fprintf(b, "  node: %s\n", p.NodeName)
	}
	if p.CPU > 0 || p.Memory > 0 {
		fmt.Fprintf(b, "  cpu: %s  mem: %s\n", k8s.FormatCPU(p.CPU), k8s.FormatMem(p.Memory))
	}
}

func writeContainer(b *strings.Builder, c k8s.ContainerInfo) {
	fmt.Fprintf(b, "container/%s (in pod/%s):\n", c.Name, c.PodName)
	fmt.Fprintf(b, "  kind: %s\n", c.Kind)
	if c.Image != "" {
		fmt.Fprintf(b, "  image: %s\n", c.Image)
	}
	if c.State != "" {
		fmt.Fprintf(b, "  state: %s\n", c.State)
	}
	fmt.Fprintf(b, "  ready: %t\n", c.Ready)
	if c.Restarts > 0 {
		fmt.Fprintf(b, "  restarts: %d\n", c.Restarts)
	}
	if c.LastReason != "" {
		fmt.Fprintf(b, "  last_termination: %s\n", c.LastReason)
	}
	if c.CPU > 0 || c.Memory > 0 {
		fmt.Fprintf(b, "  cpu: %s  mem: %s\n", k8s.FormatCPU(c.CPU), k8s.FormatMem(c.Memory))
	}
}

func writeNode(b *strings.Builder, n k8s.NodeInfo) {
	fmt.Fprintf(b, "node/%s:\n", n.Name)
	if n.Pool != "" {
		fmt.Fprintf(b, "  pool: %s\n", n.Pool)
	}
	if n.Status != "" {
		fmt.Fprintf(b, "  status: %s\n", n.Status)
	}
	if n.Roles != "" {
		fmt.Fprintf(b, "  roles: %s\n", n.Roles)
	}
	fmt.Fprintf(b, "  schedulable: %t\n", n.Schedulable)
	fmt.Fprintf(b, "  pods: %d/%d\n", n.PodsCurrent, n.PodsMax)
	if n.CPUAlloc > 0 {
		fmt.Fprintf(b, "  cpu: %s/%s\n", k8s.FormatCPU(n.CPUUsed), k8s.FormatCPU(n.CPUAlloc))
	}
	if n.MemAlloc > 0 {
		fmt.Fprintf(b, "  mem: %s/%s\n", k8s.FormatMem(n.MemUsed), k8s.FormatMem(n.MemAlloc))
	}
}

func writePool(b *strings.Builder, p k8s.NodePoolInfo) {
	fmt.Fprintf(b, "nodepool/%s:\n", p.Name)
	if p.Provider != "" {
		fmt.Fprintf(b, "  provider: %s\n", p.Provider)
	}
	fmt.Fprintf(b, "  nodes: %d\n", p.Nodes)
	fmt.Fprintf(b, "  health: %s\n", healthLabel(p.Health))
	if p.CPUAlloc > 0 {
		fmt.Fprintf(b, "  cpu: %s/%s\n", k8s.FormatCPU(p.CPUUsed), k8s.FormatCPU(p.CPUAlloc))
	}
	if p.MemAlloc > 0 {
		fmt.Fprintf(b, "  mem: %s/%s\n", k8s.FormatMem(p.MemUsed), k8s.FormatMem(p.MemAlloc))
	}
	fmt.Fprintf(b, "  pods: %d/%d\n", p.PodsCurrent, p.PodsMax)
}

func healthLabel(h k8s.Health) string {
	switch h {
	case k8s.HealthHealthy:
		return "healthy"
	case k8s.HealthDegraded:
		return "degraded"
	case k8s.HealthFailing:
		return "failing"
	case k8s.HealthInProgress:
		return "in_progress"
	case k8s.HealthUnknown:
		return "unknown"
	default:
		return "unknown"
	}
}

// pathNamespace finds the namespace covering the current selection. Apps,
// Pods, and Containers all carry one; Nodes and NodePools don't.
func pathNamespace(path []NodeRef, sel NodeRef) string {
	if sel.Namespace != "" {
		return sel.Namespace
	}
	for i := len(path) - 1; i >= 0; i-- {
		if path[i].Namespace != "" {
			return path[i].Namespace
		}
		if path[i].Kind == k8s.KindNamespace || path[i].Kind == k8s.KindNamespaceFolder {
			return path[i].Name
		}
	}
	return ""
}

// pathApp returns the App ref enclosing the current selection (only when the
// user is viewing pods/containers under an app).
func pathApp(t *Tree) (NodeRef, bool) {
	for _, r := range t.Path() {
		if r.Kind == k8s.KindApp {
			return r, true
		}
	}
	return NodeRef{}, false
}
