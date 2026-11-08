package sections

import (
	"testing"

	"k8s.io/apimachinery/pkg/runtime/schema"

	tea "charm.land/bubbletea/v2"

	"github.com/stretchr/testify/require"
	"github.com/yunus/safina/internal/k8s"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	kubefake "k8s.io/client-go/kubernetes/fake"
)

func TestConnectedGenericDescribeLoadsExactResource(t *testing.T) {
	obj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "autoscaling/v2", "kind": "HorizontalPodAutoscaler",
		"metadata": map[string]any{"namespace": "shop", "name": "scaler"},
		"spec":     map[string]any{"minReplicas": int64(2), "maxReplicas": int64(8)},
	}}
	dyn := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), obj)
	mgr := &k8s.Manager{Clients: map[string]*k8s.Client{"demo": {Dynamic: dyn}}}
	ref := NodeRef{Kind: k8s.KindGenericResource, SubKind: "HorizontalPodAutoscaler", Namespace: "shop", Name: "scaler", GVR: schema.GroupVersionResource{Group: "autoscaling", Version: "v2", Resource: "horizontalpodautoscalers"}}
	msg, ok := LoadGenericDescribe(t.Context(), mgr, "demo", ref)().(DescribeLoadedMsg)
	require.True(t, ok)
	require.NoError(t, msg.Err)
	require.Contains(t, msg.YAML, "maxReplicas: 8")
	require.Contains(t, msg.YAML, "name: scaler")
	require.Len(t, dyn.Actions(), 1)
	require.Equal(t, "get", dyn.Actions()[0].GetVerb())
	require.Equal(t, "shop", dyn.Actions()[0].GetNamespace())
	ref.Name = "missing"
	missing, ok := LoadGenericDescribe(t.Context(), mgr, "demo", ref)().(DescribeLoadedMsg)
	require.True(t, ok)
	require.Error(t, missing.Err)
	require.Empty(t, missing.YAML)
}

func TestConnectedSecretDescribeDoesNotRevealPlaintext(t *testing.T) {
	cs := kubefake.NewClientset(&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "db", Namespace: "shop"}, Data: map[string][]byte{"password": []byte("never-print-this")}})
	mgr := &k8s.Manager{Clients: map[string]*k8s.Client{"demo": {Clientset: cs}}}
	ref := NodeRef{Kind: k8s.KindGenericResource, SubKind: "Secret", Namespace: "shop", Name: "db", GVR: schema.GroupVersionResource{Group: "", Version: "v1", Resource: "secrets"}}
	msg, ok := LoadGenericDescribe(t.Context(), mgr, "demo", ref)().(DescribeLoadedMsg)
	require.True(t, ok)
	require.NoError(t, msg.Err)
	require.NotContains(t, msg.YAML, "never-print-this")
	require.Contains(t, msg.YAML, "bmV2ZXItcHJpbnQtdGhpcw==")
}

func TestGenericDescribeRefreshKeepsItsAPIIdentity(t *testing.T) {
	obj := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "example.io/v1", "kind": "Widget", "metadata": map[string]any{"namespace": "shop", "name": "target"}, "spec": map[string]any{"setting": "kept"}}}
	dyn := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), obj)
	mgr := &k8s.Manager{Clients: map[string]*k8s.Client{"demo": {Dynamic: dyn}}}
	c := newEventsCtx()
	c.Manager, c.ActiveContext = mgr, "demo"
	describe := NewDescribe(c)
	describe.Reset("Widget", "target", "shop")
	describe.SetSourceGVR(schema.GroupVersionResource{Group: "example.io", Version: "v1", Resource: "widgets"})
	cmd, leave := describe.Update(tea.KeyPressMsg{Code: 'r', Text: "r"}, c)
	require.False(t, leave)
	require.NotNil(t, cmd)
	msg, ok := cmd().(DescribeLoadedMsg)
	require.True(t, ok)
	require.NoError(t, msg.Err)
	require.Contains(t, msg.YAML, "setting: kept")
	describe.Reset("ConfigMap", "new", "shop")
	require.Empty(t, describe.sourceGVR, "the next resource must not inherit a previous GVR")
}
