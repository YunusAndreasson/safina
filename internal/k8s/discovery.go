package k8s

import (
	"sort"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
)

// ResourceKind is one row in the Resource Browser. It carries enough to
// identify the resource (Group/Version/Resource), to render a friendly
// name (Kind), and to decide where it lives in the tree (Namespaced).
type ResourceKind struct {
	schema.GroupVersionResource
	Kind       string // singular, capitalised ("ConfigMap", "PrometheusRule")
	Namespaced bool
	// IsCRD is best-effort: the discovery API doesn't directly say
	// "this is a CRD", so we infer from group ownership — any GVR whose
	// group is not in the built-in core/apps/networking/etc. set is
	// treated as a CRD. Used purely for grouping in the browser.
	IsCRD bool
}

// builtInGroups are the groups safina considers "first-class" — anything
// outside this set is treated as a CRD for grouping purposes. Not a
// security boundary; just a UX hint so the browser groups properly.
var builtInGroups = map[string]bool{
	"":                             true, // core
	"apps":                         true,
	"batch":                        true,
	"networking.k8s.io":            true,
	"storage.k8s.io":               true,
	"rbac.authorization.k8s.io":    true,
	"autoscaling":                  true,
	"policy":                       true,
	"scheduling.k8s.io":            true,
	"node.k8s.io":                  true,
	"coordination.k8s.io":          true,
	"discovery.k8s.io":             true,
	"events.k8s.io":                true,
	"admissionregistration.k8s.io": true,
	"apiextensions.k8s.io":         true,
	"apiregistration.k8s.io":       true,
	"flowcontrol.apiserver.k8s.io": true,
	"certificates.k8s.io":          true,
	"authentication.k8s.io":        true,
	"authorization.k8s.io":         true,
}

// ListAvailableResources enumerates every listable, non-subresource GVR
// the API server reports. ServerPreferredResources only returns one
// version per resource (the preferred one) — which is what we want for
// the browser: showing v1 *and* v1beta1 of the same kind is just noise.
//
// Best-effort: if some groups fail (a flaky CRD aggregated API server is
// the usual culprit), we keep the rest of the list rather than failing
// the whole call. That's what ServerPreferredResources also does
// internally — it returns a partial list plus an error of type
// ErrGroupDiscoveryFailed; we accept the partial list.
func ListAvailableResources(disco discovery.DiscoveryInterface) ([]ResourceKind, error) {
	apiLists, err := disco.ServerPreferredResources()
	// Don't fail on ErrGroupDiscoveryFailed — partial results are common
	// when an aggregated API server is wedged, and the browser is still
	// useful with the surviving kinds.
	if err != nil && !discovery.IsGroupDiscoveryFailedError(err) {
		return nil, err
	}

	out := make([]ResourceKind, 0, 64)
	for _, list := range apiLists {
		gv, gvErr := schema.ParseGroupVersion(list.GroupVersion)
		if gvErr != nil {
			continue
		}
		for _, r := range list.APIResources {
			if !isListable(r) {
				continue
			}
			if strings.Contains(r.Name, "/") {
				// Subresource (e.g. "pods/log") — not a kind in its own right.
				continue
			}
			out = append(out, ResourceKind{
				GroupVersionResource: gv.WithResource(r.Name),
				Kind:                 r.Kind,
				Namespaced:           r.Namespaced,
				IsCRD:                !builtInGroups[gv.Group],
			})
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Group != out[j].Group {
			return out[i].Group < out[j].Group
		}
		return out[i].Kind < out[j].Kind
	})

	return out, err
}

// isListable reports whether a resource supports the "list" verb. The
// browser only surfaces resources we can actually enumerate — subresources
// like "pods/exec" or write-only resources are filtered out.
func isListable(r metav1.APIResource) bool {
	for _, v := range r.Verbs {
		if v == "list" {
			return true
		}
	}
	return false
}
