package sections

import "github.com/yunus/safina/internal/k8s"

// Namespace levels show resource folders until a filter exposes workloads.
// If every folder is disabled, the reference-list path falls back to apps.
func (t *Tree) appFolderRows() bool {
	if t.filter.Value() != "" {
		return false
	}
	switch t.Focus().Kind {
	case k8s.KindAllNamespacesFold, k8s.KindNamespaceFolder, k8s.KindNamespace:
		return true
	default:
		return false
	}
}

func appRowRef(a *k8s.AppInfo) NodeRef {
	return NodeRef{Kind: k8s.KindApp, SubKind: a.Kind, Namespace: a.Namespace, Name: a.Name, UID: a.UID, Display: a.Name, Health: a.Health}
}

func podRowRef(p *k8s.PodInfo) NodeRef {
	return NodeRef{Kind: k8s.KindPod, Namespace: p.Namespace, Name: p.Name, Display: p.Name, Health: k8s.PodHealth(*p)}
}

// ShowOverviewPreview supplies a transient cluster-root view. It is never
// inserted into the navigation cache or ingested as fresh metric samples;
// leaving and revisiting the root therefore still requires revalidation.
func (t *Tree) ShowOverviewPreview(ch *Children) {
	if t.Focus().Kind != k8s.KindContext || ch == nil || ch.Kind != k8s.KindNamespaceFolder {
		return
	}
	t.children = ch
	t.nsHealthCache = make(map[string]nsHealthSummary)
	for _, a := range ch.Apps {
		summary, ok := t.nsHealthCache[a.Namespace]
		if !ok {
			summary.worst = k8s.HealthHealthy
		}
		summary.worst = summary.worst.Worse(a.Health)
		summary.total++
		t.nsHealthCache[a.Namespace] = summary
	}
	t.Loading = false
	t.LoadErr = nil
	t.cursor = clamp(t.cursor, 0, max(0, t.visibleCount()-1))
}
