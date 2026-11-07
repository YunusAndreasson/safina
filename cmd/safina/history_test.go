package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yunus/safina/internal/history"
)

func TestHistoryCLIWorksWithoutKubeconfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("KUBECONFIG", filepath.Join(home, "missing"))
	s, err := history.Open(history.Options{Directory: filepath.Join(home, ".safina", "history")})
	require.NoError(t, err)
	op := s.Begin(history.Target{Context: "prod", Namespace: "shop", Kind: "Deployment", Name: "api"}, "scale", map[string]string{"replicas": "3"})
	op.Record("accepted", "", nil)
	require.NoError(t, s.Close())
	cmd := historyCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"show", op.ID, "--format", "jsonl"})
	require.NoError(t, cmd.Execute())
	require.Contains(t, out.String(), `"replicas":"3"`)
	dest := filepath.Join(home, "incident")
	cmd = historyCommand()
	cmd.SetArgs([]string{"export", "--cluster", "prod", "--output", dest})
	require.NoError(t, cmd.Execute())
	_, err = os.Stat(filepath.Join(dest, "manifest.json"))
	require.NoError(t, err)
	cmd = historyCommand()
	cmd.SetArgs([]string{"list", "--since", "bad-date"})
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	require.Error(t, cmd.Execute())
}
