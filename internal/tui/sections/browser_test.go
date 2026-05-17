package sections

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yunus/safina/internal/config"
	"github.com/yunus/safina/internal/k8s"
)

func discoveredKinds() []k8s.ResourceKind {
	return []k8s.ResourceKind{
		{Group: "", Version: "v1", Resource: "configmaps", Kind: "ConfigMap", Namespaced: true},
		{Group: "", Version: "v1", Resource: "secrets", Kind: "Secret", Namespaced: true},
		{Group: "", Version: "v1", Resource: "services", Kind: "Service", Namespaced: true},
		{Group: "networking.k8s.io", Version: "v1", Resource: "ingresses", Kind: "Ingress", Namespaced: true},
		{Group: "", Version: "v1", Resource: "persistentvolumeclaims", Kind: "PersistentVolumeClaim", Namespaced: true},
		{Group: "monitoring.coreos.com", Version: "v1", Resource: "prometheusrules", Kind: "PrometheusRule", Namespaced: true, IsCRD: true},
		{Group: "cert-manager.io", Version: "v1", Resource: "certificates", Kind: "Certificate", Namespaced: true, IsCRD: true},
	}
}

func TestBuildBrowserEntries_WorkloadsFirst(t *testing.T) {
	entries := buildBrowserEntries(discoveredKinds(), config.DefaultEnabledResources())
	require.NotEmpty(t, entries)
	assert.Equal(t, "Workloads", entries[0].Display)
	assert.Equal(t, "Workloads", entries[0].Ref.Synthetic)
	assert.True(t, entries[0].Enabled, "Workloads is in defaults")
}

func TestBuildBrowserEntries_OrderedByTier(t *testing.T) {
	entries := buildBrowserEntries(discoveredKinds(), config.DefaultEnabledResources())
	require.GreaterOrEqual(t, len(entries), 8)
	// Workloads first, then core kinds alpha by Kind name, then non-core
	// built-ins, then CRDs alpha by group. This is the "familiar first,
	// exotic last" sort the modal applies.
	want := []string{
		"Workloads",
		"ConfigMap",
		"PersistentVolumeClaim",
		"Secret",
		"Service",
		"Ingress",        // networking.k8s.io is non-core, non-CRD
		"Certificate",    // cert-manager.io CRD
		"PrometheusRule", // monitoring.coreos.com CRD
	}
	for i, w := range want {
		assert.Equal(t, w, entries[i].Display, "entry %d mismatch", i)
	}
}

func TestBuildBrowserEntries_OmitsKindsNotInDiscovery(t *testing.T) {
	// Only configmaps and a CRD discovered — the rest of the historical
	// defaults must not appear, because the cluster doesn't have them.
	kinds := []k8s.ResourceKind{
		{Group: "", Version: "v1", Resource: "configmaps", Kind: "ConfigMap"},
		{Group: "monitoring.coreos.com", Version: "v1", Resource: "prometheusrules", Kind: "PrometheusRule", IsCRD: true},
	}
	entries := buildBrowserEntries(kinds, config.DefaultEnabledResources())
	displays := make([]string, len(entries))
	for i, e := range entries {
		displays[i] = e.Display
	}
	assert.ElementsMatch(t, []string{"Workloads", "ConfigMap", "PrometheusRule"}, displays,
		"only Workloads + what discovery returned should appear")
}

func TestBuildBrowserEntries_MarksEnabled(t *testing.T) {
	enabled := []config.ResourceRef{
		{Synthetic: "Workloads"},
		{Group: "monitoring.coreos.com", Version: "v1", Resource: "prometheusrules"},
	}
	entries := buildBrowserEntries(discoveredKinds(), enabled)
	var seenWorkloads, seenPromRule, seenConfigMap bool
	for _, e := range entries {
		switch {
		case e.Ref.Synthetic == "Workloads":
			assert.True(t, e.Enabled)
			seenWorkloads = true
		case e.Ref.Resource == "prometheusrules":
			assert.True(t, e.Enabled, "prometheusrules should be marked enabled")
			seenPromRule = true
		case e.Ref.Resource == "configmaps":
			assert.False(t, e.Enabled, "configmaps not in caller's enabled list")
			seenConfigMap = true
		}
	}
	assert.True(t, seenWorkloads && seenPromRule && seenConfigMap, "all checked entries observed")
}

func TestBrowser_Toggle_FlipsCheckbox(t *testing.T) {
	b := NewBrowser()
	b.Open(discoveredKinds(), nil)
	// Cursor starts at 0 (Workloads). Initially unchecked since enabled
	// was nil.
	require.False(t, b.entries[0].Enabled)
	b.Toggle()
	assert.True(t, b.entries[0].Enabled)
	b.Toggle()
	assert.False(t, b.entries[0].Enabled)
}

func TestBrowser_Confirm_PreservesOrder(t *testing.T) {
	b := NewBrowser()
	b.Open(discoveredKinds(), nil)
	// Toggle Workloads (idx 0), ConfigMap (idx 1), PrometheusRule (a CRD).
	b.Toggle() // Workloads
	b.CursorDown()
	b.Toggle() // ConfigMap
	// Move cursor down until we hit prometheusrules.
	for b.cursor < len(b.matches)-1 {
		b.CursorDown()
		if b.entries[b.matches[b.cursor]].Ref.Resource == "prometheusrules" {
			break
		}
	}
	b.Toggle()

	got := b.Confirm()
	require.Len(t, got, 3)
	assert.Equal(t, "Workloads", got[0].Synthetic)
	assert.Equal(t, "configmaps", got[1].Resource)
	assert.Equal(t, "prometheusrules", got[2].Resource)
}

func TestBrowser_FuzzyFilter_FindsCRD(t *testing.T) {
	b := NewBrowser()
	b.Open(discoveredKinds(), nil)
	b.SetQuery("prom")
	require.NotEmpty(t, b.matches, "fuzzy match should find PrometheusRule")

	found := false
	for _, idx := range b.matches {
		if b.entries[idx].Ref.Resource == "prometheusrules" {
			found = true
			break
		}
	}
	assert.True(t, found, "PrometheusRule should appear in 'prom' results")
}

func TestBrowser_Confirm_ReturnsEmptyWhenNothingChecked(t *testing.T) {
	b := NewBrowser()
	b.Open(discoveredKinds(), nil)
	got := b.Confirm()
	assert.NotNil(t, got, "Confirm must return non-nil even when empty")
	assert.Empty(t, got)
}

func TestBrowser_Confirm_RoundTripsExistingSelection(t *testing.T) {
	enabled := config.DefaultEnabledResources()
	b := NewBrowser()
	b.Open(discoveredKinds(), enabled)
	// No toggles → confirm should return the same set.
	got := b.Confirm()
	assert.Len(t, got, len(enabled))
}

func TestRefKey_DistinguishesSyntheticFromGVR(t *testing.T) {
	syn := config.ResourceRef{Synthetic: "Workloads"}
	gvr := config.ResourceRef{Group: "apps", Version: "v1", Resource: "deployments"}
	assert.NotEqual(t, refKey(syn), refKey(gvr))
}
