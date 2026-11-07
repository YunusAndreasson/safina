package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSaveLoadRoundtrip exercises persistence in an isolated directory.
func TestSaveLoadRoundtrip(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), ".safina", "config.yaml")

	cfg := &AppConfig{
		LastCluster:      "prod-cluster",
		LastNamespace:    "default",
		UserNamespaces:   []string{"my-app", "staging"},
		SystemNamespaces: []string{"kube-system"},
	}

	err := saveFile(cfg, cfgPath)
	require.NoError(t, err)

	loaded, err := loadFile(cfgPath)
	require.NoError(t, err)
	assert.NotNil(t, loaded)
	assert.Equal(t, "prod-cluster", loaded.LastCluster)
	assert.Equal(t, "default", loaded.LastNamespace)
	assert.Equal(t, cfg.UserNamespaces, loaded.UserNamespaces)
	assert.Equal(t, cfg.SystemNamespaces, loaded.SystemNamespaces)
}

func TestLoad_ReturnsEmptyConfigOnMissing(t *testing.T) {
	// Load() on missing config returns empty config, no error
	cfg, err := loadFile(filepath.Join(t.TempDir(), "missing.yaml"))
	require.NoError(t, err)
	assert.NotNil(t, cfg)
	assert.Empty(t, cfg.LastCluster)
	assert.Empty(t, cfg.LastNamespace)
}

func TestSave_ZeroValueConfig(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), ".safina", "config.yaml")

	// Save empty/zero config
	err := saveFile(&AppConfig{}, cfgPath)
	require.NoError(t, err)

	loaded, err := loadFile(cfgPath)
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
	cfgPath := filepath.Join(t.TempDir(), ".safina", "config.yaml")

	cfg := &AppConfig{
		LastCluster:    "staging",
		UserNamespaces: []string{"app1"},
	}
	err := saveFile(cfg, cfgPath)
	require.NoError(t, err)

	data, err := os.ReadFile(cfgPath) // #nosec G304 -- Reads a fixture created in t.TempDir.
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "lastCluster", "should use camelCase JSON tag")
	assert.NotContains(t, content, "LastCluster", "should NOT use Go struct field name")
}

func TestLoad_EmptyUserConfigIsNotSystem(t *testing.T) {
	// When UserNamespaces is empty, it acts as "no override", not as "all
	// namespaces are system". This is tested more thoroughly in tree_test.go
	// via ClassifyNamespaces. Here we just verify the field round-trips.
	cfgPath := filepath.Join(t.TempDir(), ".safina", "config.yaml")

	cfg := &AppConfig{UserNamespaces: nil, SystemNamespaces: nil}
	err := saveFile(cfg, cfgPath)
	require.NoError(t, err)

	data, err := os.ReadFile(cfgPath) // #nosec G304 -- Reads a fixture created in t.TempDir.
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

func TestSave_ReplacesSymlinkWithoutOverwritingTarget(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	require.NoError(t, os.WriteFile(target, []byte("preserve"), 0o600))
	path := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.Symlink(target, path))
	require.NoError(t, saveFile(&AppConfig{LastCluster: "new"}, path))
	data, err := os.ReadFile(target) // #nosec G304 -- Reads a fixture created in t.TempDir.
	require.NoError(t, err)
	assert.Equal(t, "preserve", string(data))
	cfg, err := loadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "new", cfg.LastCluster)
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Len(t, entries, 2, "temporary file must be removed")
}

func TestSave_FailedReplacementCleansTemporaryFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.Mkdir(path, 0o700))
	require.Error(t, saveFile(&AppConfig{}, path))
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.True(t, entries[0].IsDir())
}
