package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
	"github.com/yunus/safina/internal/config"
	"github.com/yunus/safina/internal/k8s"
	"github.com/yunus/safina/internal/tui/sections"
)

func TestOpenReadingViewStaysVisibleWhenTreeMovesToFolder(t *testing.T) {
	for _, tc := range []struct {
		state state
		tab   sections.WorkspaceTab
	}{
		{stateLogView, sections.WSTabLogs},
		{stateEventsView, sections.WSTabEvents},
		{stateDescribeView, sections.WSTabYaml},
	} {
		m := shiftFooterModel(t)
		m.tree.Push(sections.NodeRef{Kind: k8s.KindNamespaceFolder, Name: "shop", Namespace: "shop"})
		m.tree.SetChildren(m.tree.Path(), &sections.Children{Kind: k8s.KindApp})
		m.state, m.treeFocusInImmersive = tc.state, true
		require.Contains(t, sections.AvailableTabs(m.workspaceTabAvailability()), tc.tab)
	}
}

func TestLogbookScopesFoldersToNamespaceInsteadOfInventingResource(t *testing.T) {
	m := shiftFooterModel(t)
	m.tree.SetEnabledResources(config.DefaultEnabledResources())
	m.tree.Push(sections.NodeRef{Kind: k8s.KindNamespaceFolder, Name: "shop", Namespace: "shop"})
	m.tree.SetChildren(m.tree.Path(), &sections.Children{Kind: k8s.KindApp})
	m.tree.SetCursorByRef(k8s.KindWorkloadsFold, "", "Workloads")
	filter := m.selectedHistoryFilter()
	require.Equal(t, "shop", filter.Namespace)
	require.Empty(t, filter.Resource)
	m.tree.Push(m.tree.SelectedRef())
	m.tree.SetChildren(m.tree.Path(), &sections.Children{Kind: k8s.KindApp, Apps: []k8s.AppInfo{{Kind: k8s.AppKindDeployment, Name: "api", Namespace: "shop"}}})
	filter = m.selectedHistoryFilter()
	require.Equal(t, "shop", filter.Namespace)
	require.Equal(t, "Deployment/api", filter.Resource)
}

func TestMetricsDescribeShortcutStillOpensResourceYAML(t *testing.T) {
	m := shiftFooterModel(t)
	m.Update(keyMsg("m"))
	require.False(t, m.metrics.ShowActivity)
	m.Update(keyMsg("d"))
	require.Equal(t, stateDescribeView, m.state)
	require.Nil(t, m.choices)
}

func TestFormAndChoiceShortcutsUseSharedInk(t *testing.T) {
	m := shiftFooterModel(t)
	th := m.ctx.Theme
	m.openNotifications()
	require.Contains(t, m.choicesCard(), strings.TrimSuffix(th.HelpKey.Render("esc"), "\x1b[m"))
	require.Contains(t, m.renderScaleModal(), strings.TrimSuffix(th.Danger.Render("↵"), "\x1b[m"))
	require.Contains(t, m.renderConfirmModal("restart", "api"), strings.TrimSuffix(th.HelpKey.Render("n/esc"), "\x1b[m"))
	require.Contains(t, ansi.Strip(m.renderHelpModal()), "Navigate & tools")
}
