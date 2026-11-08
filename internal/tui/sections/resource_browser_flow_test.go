package sections

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
	"github.com/yunus/safina/internal/config"
	"github.com/yunus/safina/internal/k8s"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
)

func TestResourceKindsRemainDiscoverableWithoutPins(t *testing.T) {
	c := newEventsCtx()
	tree := NewTree(c)
	tree.Push(NodeRef{Kind: k8s.KindNamespaceFolder, Name: "shop", Namespace: "shop"})
	tree.SetChildren(tree.Path(), &Children{Kind: k8s.KindApp})
	tree.SetEnabledResources([]config.ResourceRef{})
	require.Equal(t, k8s.KindResourceBrowser, tree.SelectedRef().Kind)
	body := ansi.Strip(tree.renderBody(c, 48, 10))
	require.Contains(t, body, "All resource types…")
	require.NotContains(t, body, "CPU")
	require.NotContains(t, body, "AGE")
	require.Equal(t, "Resource types", tree.PanelTitle())
}

func TestPodResourceFolderIncludesStandalonePodsInOnlySelectedNamespace(t *testing.T) {
	cs := fake.NewClientset(
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "standalone", Namespace: "shop"}},
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "outside", Namespace: "other"}},
	)
	client := &k8s.Client{Clientset: cs}
	path := []NodeRef{{Kind: k8s.KindNamespaceFolder, Name: "shop", Namespace: "shop"}, {Kind: k8s.KindCustomFold, Name: "Pods", GVR: schema.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"}}}
	ch, err := loadChildren(t.Context(), client, path, nil, nil)
	require.NoError(t, err)
	require.Equal(t, k8s.KindPod, ch.Kind)
	require.Len(t, ch.Pods, 1)
	require.Equal(t, "standalone", ch.Pods[0].Name)
}

func TestClusterResourceFolderDoesNotUseNamespaceEndpoint(t *testing.T) {
	gvr := schema.GroupVersionResource{Group: "example.io", Version: "v1", Resource: "widgets"}
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{gvr: "WidgetList"},
		&unstructured.Unstructured{Object: map[string]interface{}{"apiVersion": "example.io/v1", "kind": "Widget", "metadata": map[string]interface{}{"name": "global"}}})
	tree := NewTree(newEventsCtx())
	tree.SetDiscoveredKinds([]k8s.ResourceKind{{GroupVersionResource: schema.GroupVersionResource{Group: "example.io", Version: "v1", Resource: "widgets"}, Kind: "Widget", Namespaced: false}})
	folder := tree.ResourceFolder(config.ResourceRef{Group: "example.io", Version: "v1", Resource: "widgets"})
	require.True(t, folder.ClusterScoped)
	require.Contains(t, folder.Display, "cluster")
	path := []NodeRef{{Kind: k8s.KindNamespaceFolder, Name: "shop", Namespace: "shop"}, folder}
	ch, err := loadChildren(t.Context(), &k8s.Client{Dynamic: dyn}, path, nil, nil)
	require.NoError(t, err)
	require.Len(t, ch.Generic, 1)
	require.Empty(t, dyn.Actions()[0].GetNamespace())
}

func TestResourceListDistinguishesForbiddenUnavailableAndEmpty(t *testing.T) {
	c := newEventsCtx()
	tree := NewTree(c)
	tree.Push(NodeRef{Kind: k8s.KindNamespaceFolder, Name: "shop", Namespace: "shop"})
	tree.Push(NodeRef{Kind: k8s.KindCustomFold, Name: "Certificates", Display: "Certificates", GVR: schema.GroupVersionResource{Group: "cert-manager.io", Version: "v1", Resource: "certificates"}})
	for _, tc := range []struct {
		err   error
		label string
	}{
		{apierrors.NewForbidden(schema.GroupResource{Group: "cert-manager.io", Resource: "certificates"}, "", nil), "Permission denied"},
		{apierrors.NewNotFound(schema.GroupResource{Resource: "certificates"}, ""), "Resource unavailable"},
	} {
		tree.LoadErr = tc.err
		body := ansi.Strip(tree.renderBody(c, 44, 10))
		require.Contains(t, body, tc.label)
		for _, line := range strings.Split(body, "\n") {
			require.LessOrEqual(t, ansi.StringWidth(line), 44)
		}
	}
	tree.LoadErr = nil
	tree.SetChildren(tree.Path(), &Children{Kind: k8s.KindGenericResource})
	require.Contains(t, ansi.Strip(tree.renderBody(c, 80, 10)), "no certificates in namespace shop")
}

func TestNamespaceFilterKeepsResourceTypesAndPassesQueryToBrowser(t *testing.T) {
	c := newEventsCtx()
	tree := NewTree(c)
	tree.Push(NodeRef{Kind: k8s.KindNamespaceFolder, Name: "shop", Namespace: "shop"})
	tree.SetChildren(tree.Path(), &Children{Kind: k8s.KindApp, Apps: []k8s.AppInfo{{Name: "cert-workload"}}})
	tree.filter.Open()
	tree.filter.Update(tea.PasteMsg{Content: "cert"})
	tree.filter.Close()
	rows := tree.visibleRows()
	require.Len(t, rows, 1)
	require.Equal(t, k8s.KindResourceBrowser, rows[0].Kind)
	cmd := tree.Push(rows[0])
	request, ok := cmd().(ResourceBrowserRequestedMsg)
	require.True(t, ok)
	require.Equal(t, "cert", request.Query)
	require.Equal(t, "Resource types", tree.PanelTitle())
}

func TestCustomTypeBrowsingStaysInSelectedNamespace(t *testing.T) {
	gvr := schema.GroupVersionResource{Group: "example.io", Version: "v1", Resource: "widgets"}
	objects := make([]runtime.Object, 0, 2)
	for _, ns := range []string{"shop", "system"} {
		objects = append(objects, &unstructured.Unstructured{Object: map[string]interface{}{"apiVersion": "example.io/v1", "kind": "Widget", "metadata": map[string]interface{}{"name": "widget", "namespace": ns}}})
	}
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{gvr: "WidgetList"}, objects...)
	path := []NodeRef{{Kind: k8s.KindNamespaceFolder, Name: "shop", Namespace: "shop"}, {Kind: k8s.KindCustomFold, Name: "Widgets", GVR: schema.GroupVersionResource{Group: "example.io", Version: "v1", Resource: "widgets"}}}
	ch, err := loadChildren(t.Context(), &k8s.Client{Dynamic: dyn}, path, nil, nil)
	require.NoError(t, err)
	require.Len(t, ch.Generic, 1)
	require.Equal(t, "shop", ch.Generic[0].Namespace)
	require.Equal(t, "shop", dyn.Actions()[0].GetNamespace())
}

func TestResourceListFilterAndCursorSurviveBrowsingAndBack(t *testing.T) {
	c := newEventsCtx()
	tree := NewTree(c)
	tree.Push(NodeRef{Kind: k8s.KindNamespaceFolder, Name: "shop", Namespace: "shop"})
	tree.SetChildren(tree.Path(), &Children{Kind: k8s.KindApp})
	tree.filter.Restore("secret")
	tree.cursor = 0
	require.Equal(t, k8s.KindSecretsFold, tree.SelectedRef().Kind)
	tree.Push(tree.SelectedRef())
	require.Empty(t, tree.FilterValue())
	tree.Pop()
	require.Equal(t, "secret", tree.FilterValue())
	require.False(t, tree.FilterActive())
	require.Equal(t, k8s.KindSecretsFold, tree.SelectedRef().Kind)
}
