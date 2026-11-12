package tui

import (
	"errors"
	"testing"

	"k8s.io/apimachinery/pkg/runtime/schema"

	tea "charm.land/bubbletea/v2"
	"github.com/YunusAndreasson/safina/internal/config"
	"github.com/YunusAndreasson/safina/internal/k8s"
	"github.com/YunusAndreasson/safina/internal/tui/sections"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func emptyResourceModel(t *testing.T) *Model {
	t.Helper()
	m := shiftFooterModel(t)
	m.tree.Push(sections.NodeRef{Kind: k8s.KindCustomFold, Name: "Pods", Display: "Pods", GVR: schema.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"}})
	m.tree.SetChildren(m.tree.Path(), &sections.Children{Kind: k8s.KindPod})
	return m
}

func TestEmptyListSaysSoInPlainWords(t *testing.T) {
	m := emptyResourceModel(t)
	page := ansi.Strip(m.renderPage(80, 30))
	require.Contains(t, page, "── Pods")
	require.Contains(t, page, "no pods")
	require.NotContains(t, page, "~", "no waterline under an empty-state message that has rows above it")
}

func TestListStatesAreNotMaskedByDecoration(t *testing.T) {
	for _, state := range []string{"loading", "error", "filter"} {
		t.Run(state, func(t *testing.T) {
			m := emptyResourceModel(t)
			switch state {
			case "loading":
				m.tree.SetChildren(m.tree.Path(), nil)
				m.tree.Loading = true
			case "error":
				m.tree.SetChildren(m.tree.Path(), nil)
				m.tree.LoadErr = errors.New("permission denied")
			case "filter":
				m.tree.SetChildren(m.tree.Path(), &sections.Children{Kind: k8s.KindPod, Pods: []k8s.PodInfo{{Name: "api", Namespace: "shop"}}})
				m.tree.Focused = true
				m.tree.Update(keyMsg("/"), m.ctx)
				m.tree.Update(tea.PasteMsg{Content: "missing"}, m.ctx)
				m.tree.Update(keyMsg("enter"), m.ctx)
			}
			page := ansi.Strip(m.renderPage(80, 30))
			require.NotContains(t, page, "~", "the waterline never sits under a loading, error or filtered list")
		})
	}
}

func TestFolderOpensItsListAndAResourceOpensItsPage(t *testing.T) {
	m := resourceBrowserModel(t)
	m.tree.SetEnabledResources(config.DefaultEnabledResources())
	m.tree.SetCursorByRef(k8s.KindWorkloadsFold, "", "Workloads")
	m.handleKey(keyMsg("enter"))
	require.Equal(t, k8s.KindWorkloadsFold, m.tree.Focus().Kind)
	m.tree.SetChildren(m.tree.Path(), &sections.Children{Kind: k8s.KindApp, Apps: []k8s.AppInfo{{Name: "api", Namespace: "shop", Kind: "Deployment", Health: k8s.HealthHealthy}}})
	require.Contains(t, ansi.Strip(m.renderPage(100, 30)), "── Workloads")
	m.handleKey(keyMsg("enter"))
	require.Equal(t, "api", m.tree.Focus().Name, "a resource that holds something opens its list")
	require.Contains(t, ansi.Strip(m.View().Content), "Deployment", "the topbar says what the page is")
	require.NotContains(t, ansi.Strip(m.View().Content), "2 Details", "pages have no tabs")
}
