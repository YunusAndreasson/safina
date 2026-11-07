package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDefaultEnabledResources_HasSixFolds confirms the seed list matches
// the six folds the tree historically rendered. If anyone reorders or
// adds to this set, the failure surfaces here so they update visibleRows
// in lock-step.
func TestDefaultEnabledResources_HasSixFolds(t *testing.T) {
	got := DefaultEnabledResources()
	require.Len(t, got, 6)

	// Workloads is a synthetic fold (unions five GVRs); the rest are 1:1.
	assert.Equal(t, "Workloads", got[0].Synthetic)
	assert.Equal(t, "configmaps", got[1].Resource)
	assert.Equal(t, "secrets", got[2].Resource)
	assert.Equal(t, "services", got[3].Resource)
	assert.Equal(t, "ingresses", got[4].Resource)
	assert.Equal(t, "networking.k8s.io", got[4].Group)
	assert.Equal(t, "persistentvolumeclaims", got[5].Resource)
}

func TestResourceRef_Equal(t *testing.T) {
	a := ResourceRef{Group: "apps", Version: "v1", Resource: "deployments"}
	b := ResourceRef{Group: "apps", Version: "v1", Resource: "deployments"}
	c := ResourceRef{Group: "apps", Version: "v1", Resource: "statefulsets"}
	d := ResourceRef{Synthetic: "Workloads"}
	e := ResourceRef{Synthetic: "Workloads"}

	assert.True(t, a.Equal(b))
	assert.False(t, a.Equal(c))
	assert.True(t, d.Equal(e))
	assert.False(t, a.Equal(d))
}

// TestLoad_SeedsDefaultsWhenEnabledResourcesAbsent — fresh install case:
// no config file exists, Load() returns the default fold set so the
// first launch renders the same six folds the user is used to.
func TestLoad_SeedsDefaultsWhenEnabledResourcesAbsent(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), ".safina", "config.yaml")

	loaded, err := loadFile(cfgPath)
	require.NoError(t, err)
	require.NotNil(t, loaded)
	assert.Equal(t, DefaultEnabledResources(), loaded.EnabledResources,
		"missing config file → seed defaults")
}

// TestLoad_RespectsExplicitEmptyList — the user opted out of every fold
// via the Resource Browser. Load must not re-seed defaults; that would
// silently undo their choice on every restart.
func TestLoad_RespectsExplicitEmptyList(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), ".safina", "config.yaml")

	cfg := &AppConfig{EnabledResources: []ResourceRef{}}
	err := saveFile(cfg, cfgPath)
	require.NoError(t, err)

	loaded, err := loadFile(cfgPath)
	require.NoError(t, err)
	assert.NotNil(t, loaded.EnabledResources, "empty list must round-trip as non-nil")
	assert.Empty(t, loaded.EnabledResources, "explicit empty must survive Load")
}

// TestSaveLoadRoundtrip_EnabledResources — a non-trivial selection
// (Workloads + a CRD) round-trips byte-for-byte through YAML.
func TestSaveLoadRoundtrip_EnabledResources(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), ".safina", "config.yaml")

	cfg := &AppConfig{
		EnabledResources: []ResourceRef{
			{Synthetic: "Workloads"},
			{Group: "monitoring.coreos.com", Version: "v1", Resource: "prometheusrules"},
			{Group: "cert-manager.io", Version: "v1", Resource: "certificates"},
		},
	}
	err := saveFile(cfg, cfgPath)
	require.NoError(t, err)

	data, err := os.ReadFile(cfgPath) // #nosec G304 -- Reads a fixture created in t.TempDir.
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "enabledResources", "key must be present in YAML")
	assert.Contains(t, content, "prometheusrules", "resource name must round-trip")

	loaded, err := loadFile(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, cfg.EnabledResources, loaded.EnabledResources)
}
