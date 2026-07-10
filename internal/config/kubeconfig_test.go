package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const minimalKubeconfig = `apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://example.com
  name: test-cluster
contexts:
- context:
    cluster: test-cluster
    user: test-user
  name: test-context
current-context: test-context
users:
- name: test-user
  user:
    token: fake-token
`

func TestLoadKubeConfig_Valid(t *testing.T) {
	path := writeTempKubeconfig(t, minimalKubeconfig)
	cfg, err := LoadKubeConfig(path)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	assert.Len(t, cfg.Contexts, 1)
	assert.Contains(t, cfg.Contexts, "test-context")
	assert.Equal(t, "test-context", cfg.CurrentContext)
}

func TestLoadKubeConfig_EmptyPath(t *testing.T) {
	// explicitPath="" falls back to $KUBECONFIG / ~/.kube/config via the
	// default clientcmd loading rules. Pin the env so the test doesn't
	// depend on the developer's real kubeconfig.
	t.Setenv("KUBECONFIG", writeTempKubeconfig(t, minimalKubeconfig))
	cfg, err := LoadKubeConfig("")
	require.NoError(t, err)
	assert.Contains(t, cfg.Contexts, "test-context")
}

func TestLoadKubeConfig_InvalidPath(t *testing.T) {
	cfg, err := LoadKubeConfig("/nonexistent/path/that/does/not/exist")
	require.Error(t, err, "non-existent path should error")
	assert.Nil(t, cfg)
}

func TestLoadKubeConfig_MultiFileMerge(t *testing.T) {
	config1 := `apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://cluster1.example.com
  name: cluster-1
contexts:
- context:
    cluster: cluster-1
    user: user-1
  name: context-1
current-context: context-1
users:
- name: user-1
  user:
    token: token-1
`
	config2 := `apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://cluster2.example.com
  name: cluster-2
contexts:
- context:
    cluster: cluster-2
    user: user-2
  name: context-2
users:
- name: user-2
  user:
    token: token-2
`

	path1 := writeTempKubeconfig(t, config1)
	path2 := writeTempKubeconfig(t, config2)

	// Multi-file merge happens via $KUBECONFIG, matching kubectl: an
	// explicit --kubeconfig path is a single file and never a list.
	t.Setenv("KUBECONFIG", path1+string(os.PathListSeparator)+path2)

	cfg, err := LoadKubeConfig("")
	require.NoError(t, err)
	assert.Len(t, cfg.Contexts, 2, "both configs should be merged")
	assert.Contains(t, cfg.Contexts, "context-1")
	assert.Contains(t, cfg.Contexts, "context-2")
	// kubectl merge semantics: the first file to set current-context wins.
	assert.Equal(t, "context-1", cfg.CurrentContext)
}

func TestLoadKubeConfig_EmptyFile(t *testing.T) {
	path := writeTempKubeconfig(t, "")
	cfg, err := LoadKubeConfig(path)
	require.Error(t, err, "empty kubeconfig should error")
	assert.Nil(t, cfg)
}

func TestListContexts(t *testing.T) {
	cfg, err := LoadKubeConfig(writeTempKubeconfig(t, minimalKubeconfig))
	require.NoError(t, err)

	contexts := ListContexts(cfg)
	require.Len(t, contexts, 1)
	assert.Equal(t, "test-context", contexts[0].Name)
	assert.Equal(t, "test-cluster", contexts[0].Cluster)
	assert.Equal(t, "test-user", contexts[0].User)
}

func TestListContexts_Sorted(t *testing.T) {
	multiCfg := `apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://example.com
  name: cluster
contexts:
- context:
    cluster: cluster
    user: user
  name: zebra
- context:
    cluster: cluster
    user: user
  name: alpha
- context:
    cluster: cluster
    user: user
  name: delta
current-context: alpha
users:
- name: user
  user:
    token: token
`
	cfg, err := LoadKubeConfig(writeTempKubeconfig(t, multiCfg))
	require.NoError(t, err)

	contexts := ListContexts(cfg)
	require.Len(t, contexts, 3)
	assert.Equal(t, "alpha", contexts[0].Name)
	assert.Equal(t, "delta", contexts[1].Name)
	assert.Equal(t, "zebra", contexts[2].Name)
}

func TestListContexts_EmptyConfig(t *testing.T) {
	contexts := ListContexts(nil)
	assert.Empty(t, contexts)
}

func writeTempKubeconfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "kubeconfig")
	err := os.WriteFile(path, []byte(content), 0o644)
	require.NoError(t, err)
	return path
}
