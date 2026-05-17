package k8s

import (
	"testing"

	openapi_v2 "github.com/google/gnostic-models/openapiv2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/version"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/openapi"
	"k8s.io/client-go/rest"
)

// stubDiscovery is the minimum surface of discovery.DiscoveryInterface
// our tests exercise. ListAvailableResources only calls
// ServerPreferredResources; the rest of the methods are present so the
// type satisfies the interface but return zero values.
type stubDiscovery struct {
	resources []*metav1.APIResourceList
}

func (s *stubDiscovery) ServerPreferredResources() ([]*metav1.APIResourceList, error) {
	return s.resources, nil
}

// The remaining methods are unused in tests; they exist to satisfy
// the DiscoveryInterface contract.
func (s *stubDiscovery) RESTClient() rest.Interface { return nil }

func (s *stubDiscovery) ServerGroups() (*metav1.APIGroupList, error) {
	return &metav1.APIGroupList{}, nil
}

func (s *stubDiscovery) ServerResourcesForGroupVersion(string) (*metav1.APIResourceList, error) {
	return &metav1.APIResourceList{}, nil
}

func (s *stubDiscovery) ServerGroupsAndResources() ([]*metav1.APIGroup, []*metav1.APIResourceList, error) {
	return nil, s.resources, nil
}

func (s *stubDiscovery) ServerPreferredNamespacedResources() ([]*metav1.APIResourceList, error) {
	return s.resources, nil
}

func (s *stubDiscovery) ServerVersion() (*version.Info, error) {
	return &version.Info{}, nil
}
func (s *stubDiscovery) OpenAPISchema() (*openapi_v2.Document, error) { return nil, nil }
func (s *stubDiscovery) OpenAPIV3() openapi.Client                    { return nil }
func (s *stubDiscovery) WithLegacy() discovery.DiscoveryInterface     { return s }

func fakeDisco(_ *testing.T, lists []*metav1.APIResourceList) discovery.DiscoveryInterface {
	return &stubDiscovery{resources: lists}
}

func TestListAvailableResources_FiltersSubresources(t *testing.T) {
	disco := fakeDisco(t, []*metav1.APIResourceList{
		{
			GroupVersion: "v1",
			APIResources: []metav1.APIResource{
				{Name: "pods", Kind: "Pod", Namespaced: true, Verbs: metav1.Verbs{"get", "list"}},
				{Name: "pods/log", Kind: "Pod", Namespaced: true, Verbs: metav1.Verbs{"get"}},
				{Name: "pods/exec", Kind: "Pod", Namespaced: true, Verbs: metav1.Verbs{"create"}},
				{Name: "nodes", Kind: "Node", Namespaced: false, Verbs: metav1.Verbs{"get", "list"}},
			},
		},
	})

	got, err := ListAvailableResources(disco)
	require.NoError(t, err)

	// pods/log and pods/exec are subresources and must be filtered out.
	require.Len(t, got, 2)
	names := []string{got[0].Resource, got[1].Resource}
	assert.Contains(t, names, "pods")
	assert.Contains(t, names, "nodes")
	for _, k := range got {
		assert.NotContains(t, k.Resource, "/")
	}
}

func TestListAvailableResources_DropsNonListable(t *testing.T) {
	disco := fakeDisco(t, []*metav1.APIResourceList{
		{
			GroupVersion: "v1",
			APIResources: []metav1.APIResource{
				{Name: "pods", Kind: "Pod", Namespaced: true, Verbs: metav1.Verbs{"get", "list"}},
				{Name: "bindings", Kind: "Binding", Namespaced: true, Verbs: metav1.Verbs{"create"}},
			},
		},
	})

	got, err := ListAvailableResources(disco)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "pods", got[0].Resource)
}

func TestListAvailableResources_MarksCRDs(t *testing.T) {
	disco := fakeDisco(t, []*metav1.APIResourceList{
		{
			GroupVersion: "v1",
			APIResources: []metav1.APIResource{
				{Name: "configmaps", Kind: "ConfigMap", Namespaced: true, Verbs: metav1.Verbs{"list"}},
			},
		},
		{
			GroupVersion: "monitoring.coreos.com/v1",
			APIResources: []metav1.APIResource{
				{Name: "prometheusrules", Kind: "PrometheusRule", Namespaced: true, Verbs: metav1.Verbs{"list"}},
			},
		},
	})

	got, err := ListAvailableResources(disco)
	require.NoError(t, err)
	require.Len(t, got, 2)

	for _, k := range got {
		switch k.Resource {
		case "configmaps":
			assert.False(t, k.IsCRD, "core groups are not CRDs")
		case "prometheusrules":
			assert.True(t, k.IsCRD, "monitoring.coreos.com is outside the built-in groups")
			assert.Equal(t, "PrometheusRule", k.Kind)
			assert.Equal(t, "monitoring.coreos.com", k.Group)
			assert.Equal(t, "v1", k.Version)
		}
	}
}

func TestListAvailableResources_SortsStable(t *testing.T) {
	disco := fakeDisco(t, []*metav1.APIResourceList{
		{
			GroupVersion: "z-group/v1",
			APIResources: []metav1.APIResource{
				{Name: "widgets", Kind: "Widget", Verbs: metav1.Verbs{"list"}},
			},
		},
		{
			GroupVersion: "a-group/v1",
			APIResources: []metav1.APIResource{
				{Name: "things", Kind: "Thing", Verbs: metav1.Verbs{"list"}},
			},
		},
	})

	got, err := ListAvailableResources(disco)
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "a-group", got[0].Group)
	assert.Equal(t, "z-group", got[1].Group)
}
