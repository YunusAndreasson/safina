package k8s

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// A login that rewrites the kubeconfig while safina runs used to need a
// restart before the cluster opened with the new credentials.
func TestReloadPicksUpAKubeconfigRewrittenWhileRunning(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	write := func(cfg *clientcmdapi.Config) {
		t.Helper()
		require.NoError(t, clientcmd.WriteToFile(*cfg, path))
	}
	load := func() (*clientcmdapi.Config, error) { return clientcmd.LoadFromFile(path) }
	cfg := testKubeconfig("dev", "")
	write(cfg)
	raw, err := load()
	require.NoError(t, err)
	m, err := NewManager(raw)
	require.NoError(t, err)
	t.Cleanup(m.Close)
	m.SetKubeconfigLoader(load)
	m.SetNamespace("team")

	before, _ := m.GetClient("dev")
	_, err = m.Reload("dev")
	require.NoError(t, err)
	same, _ := m.GetClient("dev")
	require.Same(t, before, same, "an unchanged kubeconfig keeps the client and its caches")

	cfg.AuthInfos["user"].Token = "fresh-token"
	cfg.Clusters["cluster"].Server = "https://127.0.0.1:7443"
	cfg.Contexts["added"] = &clientcmdapi.Context{Cluster: "cluster", AuthInfo: "user"}
	write(cfg)
	got, err := m.Reload("dev")
	require.NoError(t, err)
	require.Contains(t, got.Contexts, "added", "the caller's context list comes back current")
	after, _ := m.GetClient("dev")
	require.NotSame(t, before, after)
	require.Equal(t, "fresh-token", after.RESTConfig.BearerToken)
	require.Equal(t, "https://127.0.0.1:7443", after.RESTConfig.Host)
	require.Equal(t, "team", after.Namespace(), "--namespace still applies to the rebuilt client")

	_, err = m.Reload("added")
	require.NoError(t, err)
	added, ok := m.GetClient("added")
	require.True(t, ok, "a context added while running can be opened")
	require.Equal(t, "https://127.0.0.1:7443", added.RESTConfig.Host)
}
