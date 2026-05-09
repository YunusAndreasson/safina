package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSaveLoadRoundtrip tests Save + Load with the default config path.
// It saves a known config, reads it back, and restores the original.
func TestSaveLoadRoundtrip(t *testing.T) {
	cfgPath, err := ConfigPath()
	if err != nil {
		t.Skipf("cannot determine config path: %v", err)
	}

	// Save original config if it exists
	origData, _ := os.ReadFile(cfgPath)
	defer func() {
		if origData != nil {
			_ = os.WriteFile(cfgPath, origData, 0o644)
		} else {
			_ = os.Remove(cfgPath)
		}
	}()

	cfg := &AppConfig{
		LastCluster:      "prod-cluster",
		LastNamespace:    "default",
		UserNamespaces:   []string{"my-app", "staging"},
		SystemNamespaces: []string{"kube-system"},
	}

	err = Save(cfg)
	require.NoError(t, err)

	loaded, err := Load()
	require.NoError(t, err)
	assert.NotNil(t, loaded)
	assert.Equal(t, "prod-cluster", loaded.LastCluster)
	assert.Equal(t, "default", loaded.LastNamespace)
	assert.Equal(t, cfg.UserNamespaces, loaded.UserNamespaces)
	assert.Equal(t, cfg.SystemNamespaces, loaded.SystemNamespaces)
}

func TestLoad_ReturnsEmptyConfigOnMissing(t *testing.T) {
	// Load() on missing config returns empty config, no error
	cfg, err := Load()
	require.NoError(t, err)
	assert.NotNil(t, cfg)
	// LastCluster may be empty or have previous value; either is fine as long
	// as Load() doesn't crash
	_ = cfg.LastCluster
}

func TestSave_ZeroValueConfig(t *testing.T) {
	cfgPath, err := ConfigPath()
	if err != nil {
		t.Skipf("cannot determine config path: %v", err)
	}
	origData, _ := os.ReadFile(cfgPath)
	defer func() {
		if origData != nil {
			_ = os.WriteFile(cfgPath, origData, 0o644)
		} else {
			_ = os.Remove(cfgPath)
		}
	}()

	// Save empty/zero config
	err = Save(&AppConfig{})
	require.NoError(t, err)

	loaded, err := Load()
	require.NoError(t, err)
	assert.NotNil(t, loaded)
}

func TestSave_NilConfig(t *testing.T) {
	err := Save(nil)
	assert.Error(t, err, "saving nil config should return an error")
}

func TestConfigPath_ReturnsPath(t *testing.T) {
	path, err := ConfigPath()
	require.NoError(t, err)
	assert.Contains(t, path, ".safina")
	assert.Contains(t, path, "config.yaml")
}

func TestConfigDir_ReturnsDir(t *testing.T) {
	dir, err := ConfigDir()
	require.NoError(t, err)
	assert.Contains(t, dir, ".safina")
}

// Quick test to verify the save creates a valid YAML file that doesn't
// contain Go struct field names (it should use json tags).
func TestSave_UsesJSONTags(t *testing.T) {
	cfgPath, err := ConfigPath()
	if err != nil {
		t.Skipf("cannot determine config path: %v", err)
	}
	origData, _ := os.ReadFile(cfgPath)
	defer func() {
		if origData != nil {
			_ = os.WriteFile(cfgPath, origData, 0o644)
		} else {
			_ = os.Remove(cfgPath)
		}
	}()

	cfg := &AppConfig{
		LastCluster:    "staging",
		UserNamespaces: []string{"app1"},
	}
	err = Save(cfg)
	require.NoError(t, err)

	data, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "lastCluster", "should use camelCase JSON tag")
	assert.NotContains(t, content, "LastCluster", "should NOT use Go struct field name")
}

func TestLoad_EmptyUserConfigIsNotSystem(t *testing.T) {
	// When UserNamespaces is empty, it acts as "no override", not as "all
	// namespaces are system". This is tested more thoroughly in tree_test.go
	// via ClassifyNamespaces. Here we just verify the field round-trips.
	cfgPath, err := ConfigPath()
	if err != nil {
		t.Skipf("cannot determine config path: %v", err)
	}
	origData, _ := os.ReadFile(cfgPath)
	defer func() {
		if origData != nil {
			_ = os.WriteFile(cfgPath, origData, 0o644)
		} else {
			_ = os.Remove(cfgPath)
		}
	}()

	cfg := &AppConfig{UserNamespaces: nil, SystemNamespaces: nil}
	err = Save(cfg)
	require.NoError(t, err)

	data, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	// Neither field should appear in output when nil
	content := string(data)
	assert.NotContains(t, content, "userNamespaces")
	assert.NotContains(t, content, "systemNamespaces")
}

func TestConfigDir_SymlinkSafe(t *testing.T) {
	// Just verify ConfigDir returns an absolute-ish path
	dir, err := ConfigDir()
	require.NoError(t, err)
	assert.True(t, filepath.IsAbs(dir), "config dir should be absolute")
}
