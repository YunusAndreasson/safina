package sections

import (
	"strings"

	"github.com/YunusAndreasson/safina/internal/k8s"
	"github.com/YunusAndreasson/safina/internal/tui/components"
	"github.com/YunusAndreasson/safina/internal/tui/theme"
)

// ResourceOrientation replaces vacant detail tiles for navigation rows only.
// Namespaces, nodes and resource instances retain their facts and events.
func ResourceOrientation(t *theme.Theme, ref NodeRef, w, h int) (string, bool) {
	var caption, hint string
	switch {
	case ref.Kind == "":
		caption = "No resource selected"
		hint = "Select a resource in the left pane to see details."
	case ref.Kind == k8s.KindResourceBrowser:
		caption = "All resource types"
		hint = "Press Enter to browse. Pin your usual types there."
	case ref.Kind == k8s.KindAllNamespacesFold:
		caption = "Resources across user namespaces"
		hint = "Press Enter to choose a resource type."
	case ref.Kind == k8s.KindInfrastructureFold:
		caption = "Cluster infrastructure"
		hint = "Press Enter for nodes and system namespaces."
	case ref.Kind == k8s.KindSystemFold:
		caption = "System namespaces"
		hint = "Press Enter to browse."
	case ref.Kind == k8s.KindClusterFold:
		caption = "Nodes"
		hint = "Press Enter to browse node pools and nodes."
	case IsResourceGroup(ref.Kind):
		caption = ref.Name
		if ref.ClusterScoped {
			caption += " · cluster scope"
		}
		hint = "Press Enter to list resources."
	default:
		return "", false
	}
	hint = strings.Replace(hint, "Enter", t.HelpKey.Render("Enter"), 1)
	return components.IllustratedEmpty(t, w, h, components.BeaconArt, caption, hint), true
}
