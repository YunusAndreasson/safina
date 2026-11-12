package tui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/YunusAndreasson/safina/internal/config"
	"github.com/YunusAndreasson/safina/internal/k8s"
	"github.com/YunusAndreasson/safina/internal/tui/sections"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestReadingViewsUseTheFullWidth(t *testing.T) {
	m := pageModel(t)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m.handleKey(keyMsg("l"))
	m.logs.Append(strings.Repeat("x", 100))
	view := ansi.Strip(m.View().Content)
	require.Contains(t, view, strings.Repeat("x", 100), "a log line fits the modal's width")
	m.stopLogStream()
}

func TestZIsLiteralInFilter(t *testing.T) {
	m := pageModel(t)
	m.handleKey(keyMsg("l"))
	m.handleKey(keyMsg("/"))
	m.handleKey(keyMsg("z"))
	require.Equal(t, stateLogView, m.state)
	m.stopLogStream()
}

func TestLateLogCompletionDoesNotEndNewStream(t *testing.T) {
	m := NewModel(&k8s.Manager{}, &config.AppConfig{})
	old := make(chan k8s.PodLogEvent)
	current := make(chan k8s.PodLogEvent)
	m.logCh = current
	m.logs.SetPaneSize(80, 12)
	m.logs.Append("new stream output")
	m.Update(logEventMsg{channel: old, event: k8s.PodLogEvent{Done: true}})
	require.NotContains(t, m.tabState(sections.WSTabLogs), "stream ended")
	m.Update(logEventMsg{channel: current, event: k8s.PodLogEvent{Done: true}})
	require.Contains(t, m.tabState(sections.WSTabLogs), "stream ended")
}

func TestNarrowTerminalPagesDoNotClip(t *testing.T) {
	for _, current := range []state{stateMain, stateLogView, stateEventsView, stateDescribeView, stateHotView, stateForwardsList, stateChatView} {
		m := pageModel(t)
		m.handleKey(keyMsg("enter"))
		m.Update(tea.WindowSizeMsg{Width: 50, Height: 14})
		m.state = current
		for range 2 {
			view := m.View()
			require.LessOrEqual(t, lipgloss.Width(view.Content), 50, "state %v", current)
			require.LessOrEqual(t, lipgloss.Height(view.Content), 14, "state %v", current)
			m.handleKey(keyMsg("tab"))
		}
	}
}

// A stream that fails before its first line says why in the pane, once. It
// used to read "No log output. Press l to reopen logs." for a Job whose pods
// were long gone, where reopening can never help.
func TestLogFailureBeforeFirstLineShowsTheReasonInThePane(t *testing.T) {
	m := NewModel(&k8s.Manager{}, &config.AppConfig{})
	ch := make(chan k8s.PodLogEvent)
	m.logCh = ch
	m.logs.SetPaneSize(80, 12)
	denied := errors.New(`pods "web-1" is forbidden: User "dev" cannot get resource "pods/log"`)
	m.Update(logEventMsg{channel: ch, event: k8s.PodLogEvent{Err: denied}})
	body := ansi.Strip(m.logs.Body(m.ctx))
	require.Contains(t, body, "cannot get resource")
	require.NotContains(t, body, "No log output")
	require.Empty(t, m.bannerMsg, "the pane already says it")
	require.Contains(t, m.notifications[len(m.notifications)-1].text, "cannot get resource")

	m = NewModel(&k8s.Manager{}, &config.AppConfig{})
	m.logCh = ch
	m.logs.SetPaneSize(80, 12)
	m.logs.Append("served a while\n")
	m.Update(logEventMsg{channel: ch, event: k8s.PodLogEvent{Err: denied}})
	require.NotEmpty(t, m.bannerMsg, "behind output the banner is the only place the error shows")
}
