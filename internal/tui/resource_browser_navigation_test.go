package tui

import (
	"errors"
	"testing"

	"k8s.io/apimachinery/pkg/runtime/schema"

	tea "charm.land/bubbletea/v2"
	"github.com/YunusAndreasson/safina/internal/config"
	"github.com/YunusAndreasson/safina/internal/k8s"
	"github.com/YunusAndreasson/safina/internal/tui/sections"
	"github.com/stretchr/testify/require"
)

func resourceBrowserModel(t *testing.T) *Model {
	t.Helper()
	m := shiftFooterModel(t)
	m.configWriter = func(*config.AppConfig) error { t.Fatal("browsing must not write preferences"); return nil }
	m.tree.Push(sections.NodeRef{Kind: k8s.KindNamespaceFolder, Name: "shop", Namespace: "shop", Display: "shop"})
	m.tree.SetChildren(m.tree.Path(), &sections.Children{Kind: k8s.KindApp})
	m.tree.SetDiscoveredKinds([]k8s.ResourceKind{
		{GroupVersionResource: schema.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"}, Kind: "Pod", Namespaced: true},
		{GroupVersionResource: schema.GroupVersionResource{Group: "", Version: "v1", Resource: "nodes"}, Kind: "Node"},
		{GroupVersionResource: schema.GroupVersionResource{Group: "cert-manager.io", Version: "v1", Resource: "certificates"}, Kind: "Certificate", Namespaced: true, IsCRD: true},
	})
	m.discoveryByContext["demo"] = true
	return m
}

func TestResourceBrowserVisibleEntryOpensWithoutPinningAndEscapeReturns(t *testing.T) {
	m := resourceBrowserModel(t)
	m.tree.SetCursorByRef(k8s.KindResourceBrowser, "", "All resource types…")
	parent := m.tree.Path()
	_, cmd := m.handleKey(keyMsg("enter"))
	require.NotNil(t, cmd)
	m.Update(cmd())
	require.Equal(t, stateBrowser, m.state)
	require.Contains(t, m.browser.Scope, "namespace shop")
	m.browser.SetQuery("Certificate")
	m.handleKey(keyMsg("enter"))
	require.Equal(t, stateMain, m.state)
	require.Equal(t, schema.GroupVersionResource{Group: "cert-manager.io", Version: "v1", Resource: "certificates"}, m.tree.Focus().GVR)
	require.Equal(t, parent, m.tree.Path()[:len(parent)])
	m.handleKey(keyMsg("esc"))
	require.Equal(t, parent, m.tree.Path())
	require.Equal(t, k8s.KindResourceBrowser, m.tree.SelectedRef().Kind)
}

func TestResourceBrowserPinsImmediatelyWithoutLosingUnavailableKinds(t *testing.T) {
	m := resourceBrowserModel(t)
	unavailable := config.ResourceRef{Group: "example.io", Version: "v1", Resource: "widgets"}
	m.tree.SetEnabledResources([]config.ResourceRef{{Synthetic: "Workloads"}, unavailable})
	writes := 0
	m.configWriter = func(cfg *config.AppConfig) error {
		writes++
		require.Contains(t, cfg.EnabledResources, unavailable)
		return nil
	}
	m.openBrowser()
	m.browser.SetQuery("Certificate")
	m.handleKey(keyMsg(" "))
	require.Equal(t, 1, writes)
	require.Equal(t, stateBrowser, m.state)
	require.Equal(t, "Certificate", m.browser.Query())
	require.Len(t, m.tree.EnabledResources(), 3)
	m.handleKey(keyMsg("esc"))
	m.openBrowser()
	m.browser.SetQuery("Certificate")
	m.handleKey(keyMsg(" "))
	require.Equal(t, 2, writes)
	require.Len(t, m.tree.EnabledResources(), 2)
}

func TestResourceBrowserFailedPinKeepsSavedState(t *testing.T) {
	m := resourceBrowserModel(t)
	before := m.tree.EnabledResources()
	m.configWriter = func(*config.AppConfig) error { return errors.New("disk full") }
	m.openBrowser()
	m.browser.SetQuery("Certificate")
	m.handleKey(keyMsg(" "))
	require.Equal(t, before, m.tree.EnabledResources())
	require.Equal(t, stateBrowser, m.state)
}

func TestResourceBrowserDiscoveryPreservesQueryAndIgnoresOtherCluster(t *testing.T) {
	m := resourceBrowserModel(t)
	m.openBrowser()
	m.browser.SetQuery("Certificate")
	before, ok := m.browser.Selected()
	require.True(t, ok)
	m.Update(sections.DiscoveryLoadedMsg{Context: "old-cluster", Kinds: nil})
	require.Equal(t, "Certificate", m.browser.Query())
	selected, _ := m.browser.Selected()
	require.Equal(t, before, selected)
	m.Update(sections.DiscoveryLoadedMsg{Context: "demo", Kinds: m.tree.DiscoveredKinds(), Err: errors.New("one API unavailable")})
	require.Equal(t, "Certificate", m.browser.Query())
	selected, _ = m.browser.Selected()
	require.Equal(t, before, selected)
	require.False(t, m.browser.Loading)
	require.Contains(t, m.browser.DiscoveryError, "one API unavailable")
	require.False(t, m.discoveryByContext["demo"])
}

func TestResourceBrowserEnterWithNoMatchesDoesNothing(t *testing.T) {
	m := resourceBrowserModel(t)
	m.openBrowser()
	m.browser.SetQuery("does-not-exist-xyz")
	m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Equal(t, stateBrowser, m.state)
}

func TestPodRelationshipUsesTheTypedPodList(t *testing.T) {
	m := resourceBrowserModel(t)
	entry, err := connectedEntry(k8s.ConnectedRef{Kind: k8s.KindPod, Name: "standalone", Namespace: "shop"}, m.tree.Path(), "demo", m.tree.DiscoveredKinds())
	require.NoError(t, err)
	require.Equal(t, k8s.KindPod, entry.Kind)
	require.Empty(t, entry.SubKind)
	require.Equal(t, schema.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"}, entry.Path[len(entry.Path)-2].GVR)
}
