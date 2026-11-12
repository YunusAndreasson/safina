package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"

	"github.com/YunusAndreasson/safina/internal/config"
	"github.com/YunusAndreasson/safina/internal/k8s"
	"github.com/YunusAndreasson/safina/internal/tui/sections"
)

func TestPaletteSearchRequiresExplicitActionMode(t *testing.T) {
	m := NewModel(&k8s.Manager{}, &config.AppConfig{})
	m.state = statePalette
	m.palette.Open([]sections.PaletteEntry{{Kind: k8s.KindApp, SubKind: k8s.AppKindDeployment, Name: "qa-web-jk"}})
	for _, ch := range "qa-web-jk" {
		m.Update(keyMsg(string(ch)))
		require.Equal(t, statePalette, m.state)
	}
	require.Equal(t, "qa-web-jk", m.palette.Query())
	m.Update(keyMsg("tab"))
	require.True(t, m.palette.ActionsActive())
	m.Update(keyMsg("tab"))
	require.False(t, m.palette.ActionsActive())
}

func TestPaletteActionWaitsForTargetAndUsesCorrectRow(t *testing.T) {
	m := NewModel(&k8s.Manager{}, &config.AppConfig{})
	m.ctx.ActiveContext = "demo"
	e := sections.PaletteEntry{
		Kind: k8s.KindApp, SubKind: k8s.AppKindDeployment, Namespace: "qa", Name: "target",
		Path: []sections.NodeRef{
			{Kind: k8s.KindContextList, Name: "Clusters"},
			{Kind: k8s.KindContext, Name: "demo"},
			{Kind: k8s.KindNamespaceFolder, Name: "qa", Namespace: "qa"},
			{Kind: k8s.KindWorkloadsFold, Name: "Workloads"},
			{Kind: k8s.KindApp, Name: "target", Namespace: "qa"},
		},
	}
	_, cmd := m.dispatchPaletteVerb("S", e)
	require.NotNil(t, cmd)
	require.Equal(t, stateMain, m.state)
	m.Update(cmd()) // starts loading the uncached workload list
	require.Equal(t, stateMain, m.state)
	m.Update(sections.TreeChildrenLoadedMsg{Path: m.tree.Path(), Children: &sections.Children{
		Kind: k8s.KindApp, Apps: []k8s.AppInfo{
			{Kind: k8s.AppKindDeployment, Namespace: "qa", Name: "other", Ready: "1/1"},
			{Kind: k8s.AppKindDeployment, Namespace: "qa", Name: "target", Ready: "2/2"},
		},
	}})
	require.Equal(t, "target", m.tree.SelectedRef().Name)
	require.Equal(t, stateScaleInput, m.state)
	require.Equal(t, "2", m.scaleInput.Value())
}

func TestSearchOverlaysAcceptJK(t *testing.T) {
	for _, st := range []state{stateBrowser, stateClusters} {
		m := NewModel(&k8s.Manager{}, &config.AppConfig{})
		m.state = st
		for _, ch := range "jobs-kube" {
			m.Update(tea.KeyPressMsg{Code: ch, Text: string(ch)})
		}
		if st == stateBrowser {
			require.Equal(t, "jobs-kube", m.browser.Query())
		} else {
			require.Equal(t, "jobs-kube", m.clusters.Query())
		}
	}
}
