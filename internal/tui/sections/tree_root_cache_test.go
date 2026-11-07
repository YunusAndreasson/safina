package sections

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/stretchr/testify/require"

	"github.com/yunus/safina/internal/config"
	"github.com/yunus/safina/internal/k8s"
)

func TestRefreshContextPreservesPicker(t *testing.T) {
	tr := newTestTree(t)
	contexts := []config.ContextInfo{{Name: "demo"}}
	tr.SeedContexts(contexts)
	tr.PushContext("demo")
	tr.InvalidateFocus()
	tr.Pop()
	require.NotNil(t, tr.Children(), "refreshing a cluster must not evict the kubeconfig picker")
	require.Equal(t, k8s.KindContextList, tr.Children().Kind)
	require.Equal(t, contexts, tr.Children().Contexts)
}

func TestTreeNoMatchDoesNotClaimClusterIsEmpty(t *testing.T) {
	tr := newTestTree(t)
	tr.SeedContexts([]config.ContextInfo{{Name: "demo"}})
	tr.filter.Open()
	tr.filter.Update(tea.PasteMsg{Content: "does-not-exist"})
	body := tr.renderBody(newEventsCtx(), 40, 12)
	require.Contains(t, body, "No matching resources")
	require.Contains(t, body, "Change or clear")
}
