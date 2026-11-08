package tui

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/YunusAndreasson/safina/internal/config"
	"github.com/YunusAndreasson/safina/internal/k8s"
	"github.com/YunusAndreasson/safina/internal/tui/sections"
)

func TestExplicitStartupContext(t *testing.T) {
	m := NewModel(&k8s.Manager{}, &config.AppConfig{LastCluster: "remembered"})
	m.SetContexts([]config.ContextInfo{{Name: "chosen"}})
	m.Init()
	require.Equal(t, k8s.KindContextList, m.tree.Focus().Kind)
	m.SetActiveContext("chosen")
	m.Init()
	require.Equal(t, k8s.KindContext, m.tree.Focus().Kind)
	require.Equal(t, "chosen", m.tree.Focus().Name)
}

func TestStartupNamespace(t *testing.T) {
	for _, ns := range []string{"storefront", "kube-system", "missing"} {
		t.Run(ns, func(t *testing.T) {
			m := NewModel(&k8s.Manager{}, &config.AppConfig{})
			m.SetActiveNamespace(ns)
			m.tree.PushContext("chosen")
			_, cmd := m.Update(sections.TreeChildrenLoadedMsg{
				Path: m.tree.Path(),
				Children: &sections.Children{
					Kind:     k8s.KindNamespaceFolder,
					UserNS:   []k8s.NamespaceInfo{{Name: "storefront"}},
					SystemNS: []string{"kube-system"},
				},
			})
			if ns == "missing" {
				require.Equal(t, k8s.KindContext, m.tree.Focus().Kind)
				require.Contains(t, m.bannerMsg, "not found")
			} else {
				require.NotNil(t, cmd)
				require.Equal(t, k8s.KindNamespaceFolder, m.tree.Focus().Kind)
				require.Equal(t, ns, m.tree.Focus().Namespace)
			}
			require.Empty(t, m.startupNamespace)
		})
	}
}

func TestClusterSwitcherReentersActiveContextFromPicker(t *testing.T) {
	m := NewModel(&k8s.Manager{}, &config.AppConfig{})
	contexts := []config.ContextInfo{{Name: "chosen"}}
	m.SetContexts(contexts)
	m.ctx.ActiveContext = "chosen"
	m.clusters.Open(contexts, "chosen")
	_, cmd := m.handleClusterSwitch(keyMsg("enter"))
	require.NotNil(t, cmd)
	require.Equal(t, k8s.KindContext, m.tree.Focus().Kind)
	require.Equal(t, "chosen", m.tree.Focus().Name)
	_, cmd = m.handleClusterSwitch(keyMsg("enter"))
	require.Nil(t, cmd, "choosing the active cluster while inside it stays a no-op")
}
