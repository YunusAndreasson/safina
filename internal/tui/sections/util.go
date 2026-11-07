package sections

import (
	tea "charm.land/bubbletea/v2"
	"github.com/yunus/safina/internal/k8s"
)

func isResourceGroup(kind string) bool {
	switch kind {
	case k8s.KindAllNamespacesFold, k8s.KindInfrastructureFold, k8s.KindClusterFold,
		k8s.KindSystemFold, k8s.KindWorkloadsFold, k8s.KindConfigFold,
		k8s.KindSecretsFold, k8s.KindNetworkFold, k8s.KindIngressFold,
		k8s.KindStorageFold, k8s.KindCustomFold:
		return true
	}
	return false
}

func selectionTitle(ref NodeRef) string {
	name := ref.Display
	if name == "" {
		name = ref.Name
	}
	if isResourceGroup(ref.Kind) {
		return name
	}
	kind := ref.Kind
	switch kind {
	case k8s.KindNamespaceFolder:
		kind = "Namespace"
	case k8s.KindContext:
		kind = "Cluster"
	case k8s.KindApp, k8s.KindGenericResource:
		if ref.SubKind != "" {
			kind = ref.SubKind
		}
	}
	if name == "" {
		return stringsTitle(kind)
	}
	return stringsTitle(kind) + " · " + name
}

// emit wraps a value as a tea.Cmd that yields it as a tea.Msg.
func emit(msg tea.Msg) tea.Cmd { return func() tea.Msg { return msg } }

func clamp(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
