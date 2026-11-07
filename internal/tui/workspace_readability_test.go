package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/require"
	"github.com/yunus/safina/internal/config"
	"github.com/yunus/safina/internal/k8s"
	"github.com/yunus/safina/internal/tui/sections"
)

func TestExpandedWorkspaceAndTabReturnToTree(t *testing.T) {
	m := NewModel(&k8s.Manager{}, &config.AppConfig{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.state = stateLogView
	m.logs.Reset("actual-source-pod", "storefront")
	m.handleKey(keyMsg("z"))
	require.True(t, m.workspaceExpanded)
	require.Contains(t, m.workspaceTitle(sections.WSTabLogs), "actual-source-pod")
	view := m.View()
	require.Equal(t, 1, m.tabStripOriginX)
	require.LessOrEqual(t, lipgloss.Width(view.Content), 80)
	m.handleKey(keyMsg("tab"))
	require.False(t, m.workspaceExpanded)
	require.True(t, m.treeFocusInImmersive)
	m.View()
	require.Greater(t, m.tabStripOriginX, 1)
}

func TestZoomKeyIsLiteralInFilter(t *testing.T) {
	m := NewModel(&k8s.Manager{}, &config.AppConfig{})
	m.state = stateLogView
	m.handleKey(keyMsg("/"))
	m.handleKey(keyMsg("z"))
	require.False(t, m.workspaceExpanded)
}

func TestLateLogCompletionDoesNotEndNewStream(t *testing.T) {
	m := NewModel(&k8s.Manager{}, &config.AppConfig{})
	old := make(chan k8s.PodLogEvent)
	current := make(chan k8s.PodLogEvent)
	m.logCh = current
	m.logs.SetPaneSize(80, 12)
	m.logs.Append("new stream output")
	m.Update(logEventMsg{channel: old, event: k8s.PodLogEvent{Done: true}})
	require.NotContains(t, m.logs.Body(m.ctx), "stream ended")
	m.Update(logEventMsg{channel: current, event: k8s.PodLogEvent{Done: true}})
	require.Contains(t, m.logs.Body(m.ctx), "stream ended")
}

func TestNarrowTerminalShowsFocusedPaneWithoutClipping(t *testing.T) {
	for _, current := range []state{stateMain, stateLogView, stateEventsView, stateDescribeView, stateHotView, stateForwardsList, stateChatView} {
		m := NewModel(&k8s.Manager{}, &config.AppConfig{})
		m.Update(tea.WindowSizeMsg{Width: 50, Height: 14})
		m.state = current
		for range 2 {
			view := m.View()
			require.LessOrEqual(t, lipgloss.Width(view.Content), 50, "state %v", current)
			require.LessOrEqual(t, lipgloss.Height(view.Content), 14, "state %v", current)
			if m.tree.Focused {
				require.Empty(t, m.tabHitboxes)
			} else {
				require.Equal(t, 1, m.tabStripOriginX)
			}
			m.handleKey(keyMsg("tab"))
		}
	}
}
