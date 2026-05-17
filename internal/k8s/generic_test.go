package k8s

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

func newUnstructured(group, version, kind, ns, name string, age time.Duration, status map[string]interface{}) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(schema.GroupVersionKind{Group: group, Version: version, Kind: kind})
	u.SetNamespace(ns)
	u.SetName(name)
	u.SetCreationTimestamp(metav1.NewTime(time.Now().Add(-age)))
	if status != nil {
		_ = unstructured.SetNestedMap(u.Object, status, "status")
	}
	return u
}

func TestListGeneric_ReturnsRows(t *testing.T) {
	gvr := schema.GroupVersionResource{Group: "monitoring.coreos.com", Version: "v1", Resource: "prometheusrules"}
	gvk := schema.GroupVersionKind{Group: "monitoring.coreos.com", Version: "v1", Kind: "PrometheusRule"}

	scheme := runtime.NewScheme()
	listKinds := map[schema.GroupVersionResource]string{gvr: "PrometheusRuleList"}
	scheme.AddKnownTypeWithName(gvk, &unstructured.Unstructured{})

	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, listKinds,
		newUnstructured("monitoring.coreos.com", "v1", "PrometheusRule", "alpha", "rule-a", time.Hour, nil),
		newUnstructured("monitoring.coreos.com", "v1", "PrometheusRule", "alpha", "rule-b", 2*time.Hour, nil),
		newUnstructured("monitoring.coreos.com", "v1", "PrometheusRule", "beta", "rule-c", 30*time.Minute, nil),
	)

	got, err := ListGeneric(context.Background(), dyn, gvr, "")
	require.NoError(t, err)
	require.Len(t, got, 3)

	// Sorted by namespace then name.
	assert.Equal(t, "alpha", got[0].Namespace)
	assert.Equal(t, "rule-a", got[0].Name)
	assert.Equal(t, "rule-b", got[1].Name)
	assert.Equal(t, "beta", got[2].Namespace)
	assert.Equal(t, "rule-c", got[2].Name)
	for _, r := range got {
		assert.NotEmpty(t, r.Age, "age should be populated from creationTimestamp")
	}
}

func TestListGeneric_FiltersByNamespace(t *testing.T) {
	gvr := schema.GroupVersionResource{Group: "monitoring.coreos.com", Version: "v1", Resource: "prometheusrules"}
	gvk := schema.GroupVersionKind{Group: "monitoring.coreos.com", Version: "v1", Kind: "PrometheusRule"}

	scheme := runtime.NewScheme()
	listKinds := map[schema.GroupVersionResource]string{gvr: "PrometheusRuleList"}
	scheme.AddKnownTypeWithName(gvk, &unstructured.Unstructured{})

	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, listKinds,
		newUnstructured("monitoring.coreos.com", "v1", "PrometheusRule", "alpha", "rule-a", time.Hour, nil),
		newUnstructured("monitoring.coreos.com", "v1", "PrometheusRule", "beta", "rule-b", time.Hour, nil),
	)

	got, err := ListGeneric(context.Background(), dyn, gvr, "alpha")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "alpha", got[0].Namespace)
}

func TestExtractGenericStatus_PhasePreferred(t *testing.T) {
	u := newUnstructured("apps", "v1", "Foo", "ns", "name", time.Hour, map[string]interface{}{
		"phase": "Running",
		"conditions": []interface{}{
			map[string]interface{}{"type": "Ready", "status": "False", "reason": "ShouldNotShow"},
		},
	})
	assert.Equal(t, "Running", extractGenericStatus(*u))
}

func TestExtractGenericStatus_ReadyTrue(t *testing.T) {
	u := newUnstructured("apps", "v1", "Foo", "ns", "name", time.Hour, map[string]interface{}{
		"conditions": []interface{}{
			map[string]interface{}{"type": "Progressing", "status": "True"},
			map[string]interface{}{"type": "Ready", "status": "True"},
		},
	})
	assert.Equal(t, "Ready", extractGenericStatus(*u))
}

func TestExtractGenericStatus_ReadyFalseWithReason(t *testing.T) {
	u := newUnstructured("apps", "v1", "Foo", "ns", "name", time.Hour, map[string]interface{}{
		"conditions": []interface{}{
			map[string]interface{}{"type": "Ready", "status": "False", "reason": "Pending"},
		},
	})
	assert.Equal(t, "Pending", extractGenericStatus(*u))
}

func TestExtractGenericStatus_NoSignal(t *testing.T) {
	u := newUnstructured("apps", "v1", "Foo", "ns", "name", time.Hour, nil)
	assert.Empty(t, extractGenericStatus(*u))
}

func TestExtractGenericStatus_FirstTrueConditionFallback(t *testing.T) {
	u := newUnstructured("apps", "v1", "Foo", "ns", "name", time.Hour, map[string]interface{}{
		"conditions": []interface{}{
			map[string]interface{}{"type": "Synced", "status": "True"},
		},
	})
	assert.Equal(t, "Synced", extractGenericStatus(*u))
}
