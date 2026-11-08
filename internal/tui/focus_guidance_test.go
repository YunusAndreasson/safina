package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/YunusAndreasson/safina/internal/config"
	"github.com/YunusAndreasson/safina/internal/k8s"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestChatTabPreservesDraftAndReturnsFocus(t *testing.T) {
	m := NewModel(&k8s.Manager{}, &config.AppConfig{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.state = stateChatView
	m.chat.SetFocus(true)
	m.handleKey(keyMsg("x"))
	require.True(t, m.chat.HasInput())
	m.handleKey(keyMsg("tab"))
	require.Equal(t, stateChatView, m.state)
	require.True(t, m.treeFocusInImmersive)
	require.False(t, m.chat.Focused)
	require.True(t, m.chat.HasInput())
	footer := ansi.Strip(m.renderFooter())
	require.NotContains(t, footer, "send")
	require.Contains(t, footer, "tab pane")
	m.handleKey(keyMsg("?"))
	require.Equal(t, stateHelp, m.state)
	m.handleKey(keyMsg("esc"))
	require.Equal(t, stateChatView, m.state)
	m.handleKey(keyMsg("tab"))
	require.False(t, m.treeFocusInImmersive)
	require.True(t, m.chat.Focused)
	require.True(t, m.chat.HasInput())
	require.Contains(t, ansi.Strip(m.renderFooter()), "↵ send")
}

func TestReadingFooterFollowsTreeFocus(t *testing.T) {
	for _, st := range []state{stateLogView, stateEventsView, stateDescribeView, stateHotView, stateForwardsList} {
		m := NewModel(&k8s.Manager{}, &config.AppConfig{})
		m.Update(tea.WindowSizeMsg{Width: 140, Height: 30})
		m.state = st
		require.Contains(t, ansi.Strip(m.renderFooter()), "←/→ tabs")
		m.handleKey(keyMsg("tab"))
		footer := ansi.Strip(m.renderFooter())
		require.Contains(t, footer, "q close view")
		require.NotContains(t, footer, "q quit")
		require.NotContains(t, footer, "z expand")
	}
}
