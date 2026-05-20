package k8s

import (
	"context"
	"errors"
	"sort"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
)

// GenericResource is the row shape for any GVR rendered through the
// dynamic client. Status is a best-effort one-liner pulled from common
// conventions (.status.phase or .status.conditions[Ready]); it stays
// empty for resources that don't follow them, in which case the renderer
// shows only name + age.
type GenericResource struct {
	Name      string
	Namespace string
	Age       string
	Status    string
	UID       types.UID
}

// ListGeneric enumerates instances of the given GVR. namespace="" lists
// across all namespaces (the dynamic client's convention) — pass it for
// cluster-scoped GVRs or for an "all namespaces" view. Errors are passed
// through verbatim; the caller decides whether to surface them inline or
// silently degrade.
func ListGeneric(ctx context.Context, dyn dynamic.Interface, gvr schema.GroupVersionResource, namespace string) ([]GenericResource, error) {
	if dyn == nil {
		return nil, errors.New("dynamic client unavailable")
	}
	var lister dynamic.ResourceInterface
	if namespace == "" {
		lister = dyn.Resource(gvr)
	} else {
		lister = dyn.Resource(gvr).Namespace(namespace)
	}
	list, err := lister.List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	out := make([]GenericResource, 0, len(list.Items))
	for _, item := range list.Items {
		out = append(out, extractGenericRow(item))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Namespace != out[j].Namespace {
			return out[i].Namespace < out[j].Namespace
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// extractGenericRow pulls the common metadata + a best-effort status
// out of an unstructured object. The status logic is intentionally
// shallow: a CRD that doesn't expose `.status.phase` or `.status.conditions`
// just gets an empty Status, which the renderer collapses to a blank
// column. Going deeper (per-CRD additionalPrinterColumns) is a follow-up.
func extractGenericRow(u unstructured.Unstructured) GenericResource {
	g := GenericResource{
		Name:      u.GetName(),
		Namespace: u.GetNamespace(),
		Age:       HumanAge(u.GetCreationTimestamp().Time),
		UID:       u.GetUID(),
	}
	g.Status = extractGenericStatus(u)
	return g
}

// extractGenericStatus walks two well-trodden status conventions:
//  1. .status.phase — used by Pod, PVC, PV, Namespace, custom resources
//     that mimic them.
//  2. .status.conditions[type=Ready].status — used by most modern CRDs
//     (cert-manager, knative, argo, …). Falls back to the first
//     condition with Status=True if no Ready condition exists.
//
// Returns "" when neither matches.
func extractGenericStatus(u unstructured.Unstructured) string {
	if phase, ok, _ := unstructured.NestedString(u.Object, "status", "phase"); ok && phase != "" {
		return phase
	}
	conds, ok, _ := unstructured.NestedSlice(u.Object, "status", "conditions")
	if !ok {
		return ""
	}
	// First pass: look for Ready.
	for _, c := range conds {
		m, ok := c.(map[string]interface{})
		if !ok {
			continue
		}
		t, _, _ := unstructured.NestedString(m, "type")
		s, _, _ := unstructured.NestedString(m, "status")
		if strings.EqualFold(t, "Ready") {
			switch s {
			case "True":
				return "Ready"
			case "False":
				if reason, ok, _ := unstructured.NestedString(m, "reason"); ok && reason != "" {
					return reason
				}
				return "NotReady"
			}
		}
	}
	// Second pass: the first True condition is better than nothing.
	for _, c := range conds {
		m, ok := c.(map[string]interface{})
		if !ok {
			continue
		}
		t, _, _ := unstructured.NestedString(m, "type")
		s, _, _ := unstructured.NestedString(m, "status")
		if s == "True" && t != "" {
			return t
		}
	}
	return ""
}
