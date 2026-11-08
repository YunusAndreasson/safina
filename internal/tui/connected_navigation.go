package tui

import (
	"fmt"
	"slices"
	"strings"

	"k8s.io/apimachinery/pkg/runtime/schema"

	tea "charm.land/bubbletea/v2"
	"github.com/YunusAndreasson/safina/internal/k8s"
	"github.com/YunusAndreasson/safina/internal/tui/sections"
)

// Following a relationship uses the same async, cancellable selection path as
// search. Opening a resource selects it; it never performs a resource action.
func (m *Model) openConnected(ref k8s.ConnectedRef) (tea.Model, tea.Cmd) {
	if ref.Name == "" || m.ctx.ActiveContext == "" {
		return m, nil
	}
	entry, err := connectedEntry(ref, m.tree.Path(), m.ctx.ActiveContext, m.tree.DiscoveredKinds())
	if err != nil {
		return m, m.raiseError(err.Error())
	}
	source := m.tree.SelectedRef()
	if source.Kind == entry.Kind && source.SubKind == entry.SubKind && source.Name == entry.Name && source.Namespace == entry.Namespace {
		return m, nil
	}
	if source.Name != "" {
		sourcePath := append(append([]sections.NodeRef(nil), m.tree.Path()...), source)
		m.rememberConnected(sections.PaletteEntry{Kind: source.Kind, SubKind: source.SubKind, Namespace: source.Namespace, Name: source.Name, Path: sourcePath})
	}
	m.returnToMain()
	return m.drillToPaletteEntry(entry)
}

func connectedEntry(ref k8s.ConnectedRef, current []sections.NodeRef, cluster string, discovered []k8s.ResourceKind) (sections.PaletteEntry, error) {
	// Owner references inherit the source namespace even for cluster objects.
	if ref.Kind == k8s.KindNode || ref.Kind == k8s.KindPV {
		ref.Namespace = ""
	}
	for _, rk := range discovered {
		if rk.Kind == ref.Kind && !rk.Namespaced {
			ref.Namespace = ""
			break
		}
	}
	root := sections.NodeRef{Kind: k8s.KindContextList, Name: "Clusters", Display: "Clusters"}
	if len(current) > 0 && current[0].Kind == k8s.KindContextList {
		root = current[0]
	}
	context := sections.NodeRef{Kind: k8s.KindContext, Name: cluster, Display: cluster}
	for _, node := range current {
		if node.Kind == k8s.KindContext && node.Name == cluster {
			context = node
			break
		}
	}
	path := []sections.NodeRef{root, context}
	if ref.Namespace != "" {
		path = append(path, sections.NodeRef{Kind: k8s.KindNamespaceFolder, Name: ref.Namespace, Namespace: ref.Namespace, Display: ref.Namespace})
	}
	target := sections.NodeRef{Kind: ref.Kind, SubKind: ref.SubKind, Namespace: ref.Namespace, Name: ref.Name, Display: ref.Name}
	fold, label := "", ""
	switch ref.Kind {
	case k8s.KindApp:
		fold, label = k8s.KindWorkloadsFold, "Workloads"
	case k8s.KindConfigMap:
		fold, label = k8s.KindConfigFold, "ConfigMaps"
	case k8s.KindSecret:
		fold, label = k8s.KindSecretsFold, "Secrets"
	case k8s.KindService:
		fold, label = k8s.KindNetworkFold, "Services"
	case k8s.KindIngress:
		fold, label = k8s.KindIngressFold, "Ingresses"
	case k8s.KindPVC:
		fold, label = k8s.KindStorageFold, "PVCs"
	default:
		kind := ref.Kind
		if ref.SubKind != "" {
			kind = ref.SubKind
		}
		var gvr schema.GroupVersionResource
		switch ref.Kind {
		case k8s.KindServiceAccount:
			gvr = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "serviceaccounts"}
		case k8s.KindHPA:
			gvr = schema.GroupVersionResource{Group: "autoscaling", Version: "v2", Resource: "horizontalpodautoscalers"}
			kind = "HorizontalPodAutoscaler"
		case "ReplicaSet":
			gvr = schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "replicasets"}
		case k8s.KindNode:
			gvr = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "nodes"}
		case k8s.KindPod:
			gvr = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"}
		default:
			for _, rk := range discovered {
				if rk.Kind == kind && !strings.Contains(rk.Resource, "/") {
					gvr = rk.GroupVersionResource
					break
				}
			}
		}
		if gvr.Empty() {
			return sections.PaletteEntry{}, fmt.Errorf("cannot locate resource kind %s; refresh resource discovery and try again", kind)
		}
		path = append(path, sections.NodeRef{Kind: k8s.KindCustomFold, SubKind: kind, Name: kind, Display: kind, GVR: gvr, Namespace: ref.Namespace})
		target.Kind, target.SubKind, target.GVR = k8s.KindGenericResource, kind, gvr
		if ref.Kind == k8s.KindPod {
			target.Kind, target.SubKind, target.GVR = k8s.KindPod, "", schema.GroupVersionResource{}
		}
	}
	if fold != "" {
		path = append(path, sections.NodeRef{Kind: fold, Name: label, Display: label})
	}
	path = append(path, target)
	return sections.PaletteEntry{Kind: target.Kind, SubKind: target.SubKind, Namespace: target.Namespace, Name: target.Name, Path: path}, nil
}

const maxConnectedHistory = 128

func (m *Model) rememberConnected(entry sections.PaletteEntry) {
	m.connectedBack = append(m.connectedBack, entry)
	if len(m.connectedBack) > maxConnectedHistory {
		m.connectedBack = slices.Delete(m.connectedBack, 0, len(m.connectedBack)-maxConnectedHistory)
	}
}
