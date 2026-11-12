package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/YunusAndreasson/safina/internal/k8s"
	"github.com/YunusAndreasson/safina/internal/tui/sections"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestChatPagePreservesDraftAcrossCloseAndHelp(t *testing.T) {
	m := pageModel(t)
	m.handleKey(keyMsg("i"))
	require.Equal(t, stateChatView, m.state)
	m.handleKey(keyMsg("x"))
	require.True(t, m.chat.HasInput())
	require.Contains(t, ansi.Strip(m.renderFooter()), "↵ send")
	m.handleKey(keyMsg("?"))
	require.Equal(t, stateChatView, m.state, "? is text while the chat has the keys")
	m.handleKey(keyMsg("tab"))
	require.Equal(t, stateMain, m.state, "Tab closes the chat page like Esc")
	m.handleKey(keyMsg("i"))
	require.True(t, m.chat.Focused)
	require.True(t, m.chat.HasInput(), "the draft survives closing the page")
}

func TestReadingFooterNamesWhereEscGoes(t *testing.T) {
	for _, tab := range []sections.WorkspaceTab{sections.WSTabLogs, sections.WSTabEvents, sections.WSTabYaml} {
		m := pageModel(t)
		m.Update(tea.WindowSizeMsg{Width: 140, Height: 30})
		m.handleKey(keyMsg("enter"))
		m.showTab(tab)
		footer := ansi.Strip(m.renderFooter())
		require.Contains(t, footer, "esc ‹ demo", tab)
		frame := ansi.Strip(m.View().Content)
		require.NotContains(t, frame, "←/→", "tabs have their own keys")
		require.NotContains(t, frame, "z expand")
		m.stopLogStream()
	}
	for _, tool := range []string{"h", "ctrl+f"} {
		m := pageModel(t)
		m.ctx.Manager.Forwards = k8s.NewForwardManager()
		msg := keyMsg(tool)
		if tool == "ctrl+f" {
			msg = tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl}
		}
		m.handleKey(msg)
		require.Contains(t, ansi.Strip(m.renderFooter()), "esc ‹ demo")
	}
}
