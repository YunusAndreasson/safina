package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/YunusAndreasson/safina/internal/config"
	"github.com/YunusAndreasson/safina/internal/k8s"
	"github.com/YunusAndreasson/safina/internal/tui/sections"
	"github.com/stretchr/testify/require"
)

func TestNavigationOverlaysEscapeRestoresExactView(t *testing.T) {
	for _, st := range []state{stateMain, stateLogView, stateMetricsView, stateEventsView, stateDescribeView, stateHotView, stateForwardsList, stateDrainProgress} {
		for _, tab := range []sections.WorkspaceTab{sections.WSTabChildren, sections.WSTabDetails} {
			for _, opener := range []tea.KeyPressMsg{keyMsg(":"), {Code: 'p', Mod: tea.ModCtrl}, {Code: 'k', Mod: tea.ModCtrl}, {Code: 'b', Mod: tea.ModCtrl}} {
				viewer := st == stateLogView || st == stateEventsView || st == stateDescribeView
				if viewer && opener.Mod == tea.ModCtrl && opener.Code == 'b' {
					continue // in a viewer ctrl+b pages up, as in less
				}
				m := pageModel(t)
				m.handleKey(keyMsg("enter"))
				m.showTab(tab)
				m.state = st
				m.settlePage()
				wantTab := m.pageTab
				cancelled := false
				if st == stateLogView {
					m.logCancel = func() { cancelled = true }
					m.logs.Reset("source", "shop")
				}
				m.handleKey(opener)
				require.Contains(t, []state{statePalette, stateClusters, stateBrowser}, m.state)
				require.Equal(t, st, m.baseState())
				m.View() // rendering the overlay must not switch the underlying page or tab
				m.handleKey(keyMsg("esc"))
				require.Equal(t, st, m.state)
				require.Equal(t, wantTab, m.pageTab)
				require.Equal(t, "api", m.tree.Focus().Name)
				require.False(t, cancelled, "opening/cancelling search must keep logs running")
				m.stopLogStream()
			}
		}
	}
}

func TestSearchOverlayKeepsPendingLogsAliveAndDoesNotCloseOnLoad(t *testing.T) {
	m := shiftFooterModel(t)
	m.openCurrentLogs(m.tree.SelectedRef())
	m.handleKey(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	require.Equal(t, statePalette, m.state)
	cmd := m.setLogTargets(logTargetsMsg{request: m.logPickerRequest, automatic: true, streamRequest: m.logRequest, targets: []logTarget{{pod: "target", namespace: "shop", container: "app"}}})
	require.NotNil(t, cmd)
	require.Equal(t, statePalette, m.state, "loaded logs must stay behind search")
	ch := make(chan k8s.PodLogEvent)
	m.Update(sections.LogStreamStartedMsg{Request: m.logRequest, Ch: ch})
	require.Equal(t, ch, m.logCh)
	close(ch)
	m.handleKey(keyMsg("esc"))
	require.Equal(t, stateLogView, m.state)
	require.Equal(t, "target", m.logTarget.pod)
	m.stopLogStream()
}

func TestSelectingSearchDestinationStopsPriorLogStream(t *testing.T) {
	m := shiftFooterModel(t)
	m.state = stateLogView
	cancelled := false
	m.logCancel = func() { cancelled = true }
	m.handleKey(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	entry, _ := connectedEntry(k8s.ConnectedRef{Kind: k8s.KindSecret, Namespace: "shop", Name: "db"}, m.tree.Path(), "demo", nil)
	m.palette.Open([]sections.PaletteEntry{entry})
	m.handleKey(keyMsg("enter"))
	require.True(t, cancelled)
	require.Equal(t, stateMain, m.state)
	require.Equal(t, k8s.KindSecretsFold, m.tree.Focus().Kind)
	require.NotNil(t, m.paletteTarget)
}

func TestColonRemainsLiteralInFocusedInputs(t *testing.T) {
	m := shiftFooterModel(t)
	m.handleKey(keyMsg("/"))
	m.handleKey(keyMsg(":"))
	require.True(t, m.tree.FilterActive())
	require.Equal(t, ":", m.tree.FilterValue())
	require.Equal(t, stateMain, m.state)
	m.handleKey(keyMsg("esc"))
	m.handleKey(keyMsg("i"))
	m.handleKey(keyMsg(":"))
	require.Equal(t, stateChatView, m.state)
	require.True(t, m.chat.HasInput())
}

func TestBrowserPinUsesConfiguredWriter(t *testing.T) {
	m := shiftFooterModel(t)
	writes := 0
	m.configWriter = func(cfg *config.AppConfig) error {
		writes++
		require.NotNil(t, cfg.EnabledResources)
		return nil
	}
	m.handleKey(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	require.Equal(t, stateBrowser, m.state)
	m.handleKey(keyMsg(" "))
	require.Equal(t, 1, writes)
	require.Equal(t, stateBrowser, m.state)
	m.handleKey(keyMsg("esc"))
	require.Equal(t, stateMain, m.state)
}

// `:` finds resource types by fuzzy match too: `:node` + Enter opens the
// cluster's Nodes list.
func TestSearchFindsResourceTypes(t *testing.T) {
	m := pageModel(t)
	m.handleKey(keyMsg(":"))
	require.Equal(t, statePalette, m.state)
	for _, r := range "node" {
		m.handleKey(keyMsg(string(r)))
	}
	entry, ok := m.palette.Selected()
	require.True(t, ok)
	require.True(t, entry.Type, "the type ranks first: %+v", entry)
	require.Equal(t, "Nodes", entry.Name)
	m.handleKey(keyMsg("enter"))
	require.Equal(t, k8s.KindClusterFold, m.tree.Focus().Kind)
	require.Equal(t, "Nodes", m.tree.Focus().Name)
}
