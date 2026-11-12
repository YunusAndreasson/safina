package sections

import "github.com/YunusAndreasson/safina/internal/k8s"

// Single-pane navigation knows two kinds of tree focus. A place (the
// cluster, a namespace, a resource-type fold) is a list page: its rows are
// what you came for. A thing (an app, a pod, a ConfigMap) is a resource
// page: a header about the thing and tabs onto it, the first of which may be
// its children (an app's pods, a pod's containers).

// IsResourceKind reports whether a focus of this kind is a resource page.
func IsResourceKind(kind string) bool {
	switch kind {
	case k8s.KindApp, k8s.KindPod, k8s.KindContainer, k8s.KindNode,
		k8s.KindService, k8s.KindIngress, k8s.KindPVC, k8s.KindPV,
		k8s.KindConfigMap, k8s.KindSecret, k8s.KindGenericResource:
		return true
	}
	return false
}

// ChildrenLabel names the children tab of a resource page, or "" when the
// kind has no children worth a tab.
func ChildrenLabel(kind string) string {
	switch kind {
	case k8s.KindApp, k8s.KindNode:
		return "Pods"
	case k8s.KindPod:
		return "Containers"
	case k8s.KindService:
		return "Ingresses"
	case k8s.KindPVC:
		return "Volume"
	}
	return ""
}

// OnResourcePage reports whether the focus is a thing rather than a place.
func (t *Tree) OnResourcePage() bool { return IsResourceKind(t.Focus().Kind) }

// Parent returns the ref one level above the focus (the pod above a
// container page), or the zero ref at the root.
func (t *Tree) Parent() NodeRef {
	if len(t.path) < 2 {
		return NodeRef{}
	}
	return t.path[len(t.path)-2]
}

// ContainerPod returns the pod that owns the selected container. On a
// container's own page the pod is the parent; on a pod page listing
// containers it is the focus.
func (t *Tree) ContainerPod() NodeRef {
	if t.PageSubject && t.Focus().Kind == k8s.KindContainer {
		return t.Parent()
	}
	return t.Focus()
}

// subject is the page's own resource while PageSubject is set.
func (t *Tree) subject() (NodeRef, bool) {
	if !t.PageSubject || !t.OnResourcePage() {
		return NodeRef{}, false
	}
	return t.Focus(), true
}

// parentLevel is the loaded list the page's resource was opened from. Its
// row carries the resource's live summary (health, readiness, metrics).
func (t *Tree) parentLevel() *Children {
	if len(t.path) < 2 {
		return nil
	}
	return t.cache[pathKeyFor(t.path[:len(t.path)-1])]
}

// ParentPath is the path of the list the page's resource was opened from.
func (t *Tree) ParentPath() []NodeRef {
	if len(t.path) < 2 {
		return nil
	}
	return clonePath(t.path[:len(t.path)-1])
}

func (t *Tree) subjectApp() (k8s.AppInfo, bool) {
	ref, ok := t.subject()
	ch := t.parentLevel()
	if !ok || ref.Kind != k8s.KindApp || ch == nil {
		return k8s.AppInfo{}, false
	}
	for _, a := range ch.Apps {
		if a.Name == ref.Name && a.Namespace == ref.Namespace && a.Kind == ref.SubKind {
			return a, true
		}
	}
	return k8s.AppInfo{}, false
}

func (t *Tree) subjectPod() (k8s.PodInfo, bool) {
	ref, ok := t.subject()
	ch := t.parentLevel()
	if !ok || ref.Kind != k8s.KindPod || ch == nil {
		return k8s.PodInfo{}, false
	}
	for _, p := range ch.Pods {
		if p.Name == ref.Name && p.Namespace == ref.Namespace {
			return p, true
		}
	}
	return k8s.PodInfo{}, false
}

func (t *Tree) subjectNode() (k8s.NodeInfo, bool) {
	ref, ok := t.subject()
	ch := t.parentLevel()
	if !ok || ref.Kind != k8s.KindNode || ch == nil {
		return k8s.NodeInfo{}, false
	}
	for _, n := range ch.Nodes {
		if n.Name == ref.Name {
			return n, true
		}
	}
	return k8s.NodeInfo{}, false
}

// PathKey identifies a tree path, for per-page memory in the root model.
func PathKey(path []NodeRef) string { return pathKeyFor(path) }

// RowCount is how many rows the current list shows.
func (t *Tree) RowCount() int {
	if t.Focus().Kind == k8s.KindContextList {
		return len(t.filteredContexts())
	}
	return t.visibleCount()
}
