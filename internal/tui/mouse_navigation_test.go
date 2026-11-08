package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/YunusAndreasson/safina/internal/config"
	"github.com/YunusAndreasson/safina/internal/tui/sections"
	"github.com/stretchr/testify/require"
)

func clickWorkspaceTab(t *testing.T, m *Model, tab sections.WorkspaceTab) {
	t.Helper()
	for _, hit := range m.tabHitboxes {
		if hit.Tab == tab {
			m.Update(tea.MouseClickMsg{X: m.tabStripOriginX + (hit.X0+hit.X1)/2, Y: m.tabStripOriginY, Button: tea.MouseLeft})
			return
		}
	}
	t.Fatalf("tab %v not rendered", tab)
}

func TestBackgroundTabClickCannotEditOverlayInput(t *testing.T) {
	for _, st := range []state{statePalette, stateClusters, stateBrowser, stateScaleInput, statePortForwardInput, stateHelp} {
		m := shiftFooterModel(t)
		m.state = st
		m.scaleInput.SetValue("2")
		m.View()
		clickWorkspaceTab(t, m, sections.WSTabInspector)
		require.Equal(t, st, m.state)
		require.Empty(t, m.palette.Query())
		require.Empty(t, m.clusters.Query())
		require.Equal(t, "2", m.scaleInput.Value(), "background click must not type a tab's shortcut into a form")
	}
}

func TestClickingActiveLogTabDoesNotRestartStream(t *testing.T) {
	m := shiftFooterModel(t)
	m.state = stateLogView
	m.logTarget = logTarget{pod: "api-1", namespace: "shop", container: "app"}
	cancelled := false
	m.logCancel = func() { cancelled = true }
	m.logs.Reset("api-1", "shop")
	m.logs.Append("keep this line")
	m.View()
	request := m.logRequest
	clickWorkspaceTab(t, m, sections.WSTabLogs)
	require.False(t, cancelled)
	require.Equal(t, request, m.logRequest)
	require.Contains(t, m.logs.YankPayload(), "keep this line")
	m.stopLogStream()
}

func TestTabClickCommitsTreeFilterAndFocusesDestination(t *testing.T) {
	m := shiftFooterModel(t)
	m.handleKey(keyMsg("/"))
	m.handleKey(keyMsg("a"))
	m.View()
	clickWorkspaceTab(t, m, sections.WSTabEvents)
	require.Equal(t, stateEventsView, m.state)
	require.False(t, m.tree.FilterActive())
	require.Equal(t, "a", m.tree.FilterValue())
	require.True(t, m.workspaceOwnsFocus())
}

func TestInspectorTabClickActuallyFocusesInspector(t *testing.T) {
	m := shiftFooterModel(t)
	m.View()
	clickWorkspaceTab(t, m, sections.WSTabInspector)
	require.True(t, m.workspaceOwnsFocus())
	require.False(t, m.tree.Focused)
	m.handleKey(keyMsg("esc"))
	require.False(t, m.workspaceOwnsFocus())
}

func TestResizeInvalidatesMouseTargetsUntilRepaint(t *testing.T) {
	m := connectedModel(t)
	path := append([]sections.NodeRef(nil), m.tree.Path()...)
	x, y := m.tabStripOriginX+3, m.tabStripOriginY+3+m.connected.PanelTop
	m.Update(tea.WindowSizeMsg{Width: 50, Height: 14})
	m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	require.Equal(t, path, m.tree.Path())
	require.Nil(t, m.paletteTarget)
	require.Empty(t, m.tabHitboxes)
	require.Zero(t, m.connected.PanelHeight)
}

func TestMouseWheelMovesOverlaySelectionAndScrollsHelp(t *testing.T) {
	m := shiftFooterModel(t)
	m.state = statePalette
	m.palette.Open([]sections.PaletteEntry{{Name: "first"}, {Name: "second"}})
	m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	selected, ok := m.palette.Selected()
	require.True(t, ok)
	require.Equal(t, "second", selected.Name)
	m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	selected, _ = m.palette.Selected()
	require.Equal(t, "first", selected.Name)
	m.state = stateClusters
	m.clusters.Open([]config.ContextInfo{{Name: "first"}, {Name: "second"}}, "first")
	m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	cluster, ok := m.clusters.Selected()
	require.True(t, ok)
	require.Equal(t, "second", cluster.Name)
	m.state = stateHelp
	m.View()
	before := m.helpViewport.YOffset()
	m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	require.Greater(t, m.helpViewport.YOffset(), before)
}
