package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/YunusAndreasson/safina/internal/config"
	"github.com/YunusAndreasson/safina/internal/k8s"
	"github.com/YunusAndreasson/safina/internal/tui/sections"
	"github.com/stretchr/testify/require"
)

func TestNavigationOverlaysEscapeRestoresExactViewAndFocus(t *testing.T) {
	for _, st := range []state{stateMain, stateLogView, stateMetricsView, stateEventsView, stateDescribeView, stateHotView, stateForwardsList, stateDrainProgress} {
		for _, treeFocus := range []bool{false, true} {
			for _, opener := range []tea.KeyPressMsg{keyMsg(":"), {Code: 'p', Mod: tea.ModCtrl}, {Code: 'k', Mod: tea.ModCtrl}, {Code: 'b', Mod: tea.ModCtrl}} {
				m := shiftFooterModel(t)
				m.state = st
				m.peekWorkspace = st == stateMain && !treeFocus
				m.treeFocusInImmersive = st != stateMain && treeFocus
				m.workspaceExpanded = !treeFocus
				m.tree.Focused = treeFocus
				cancelled := false
				if st == stateLogView {
					m.logCancel = func() { cancelled = true }
					m.logs.Reset("source", "shop")
				}
				m.handleKey(opener)
				require.Contains(t, []state{statePalette, stateClusters, stateBrowser}, m.state)
				require.Equal(t, st, m.baseState())
				m.View() // rendering the overlay must not switch the underlying tab/focus
				m.handleKey(keyMsg("esc"))
				require.Equal(t, st, m.state)
				require.Equal(t, st == stateMain && !treeFocus, m.peekWorkspace)
				require.Equal(t, st != stateMain && treeFocus, m.treeFocusInImmersive)
				require.Equal(t, !treeFocus, m.workspaceExpanded)
				require.Equal(t, treeFocus, m.tree.Focused)
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
