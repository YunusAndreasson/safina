package tui

import (
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"

	"github.com/YunusAndreasson/safina/internal/claude"
	"github.com/YunusAndreasson/safina/internal/config"
	"github.com/YunusAndreasson/safina/internal/k8s"
	"github.com/YunusAndreasson/safina/internal/tui/sections"
)

func TestChatIgnoresSupersededStream(t *testing.T) {
	m := NewModel(&k8s.Manager{}, &config.AppConfig{})
	t.Cleanup(m.ctx.Cancel)
	m.chat.SetSize(80, 24)
	m.chat.SetFocus(true)
	m.chat.Update(tea.PasteMsg{Content: "old prompt"}, m.ctx)
	m.chat.Update(tea.KeyPressMsg{Code: tea.KeyEnter}, m.ctx)
	old := make(chan claude.Event, 4)
	m.chatStreamCh = old
	m.chat.ResetSession()
	m.chat.Update(tea.PasteMsg{Content: "new prompt"}, m.ctx)
	m.chat.Update(tea.KeyPressMsg{Code: tea.KeyEnter}, m.ctx)
	current := make(chan claude.Event, 2)
	m.chatStreamCh = current

	old <- claude.Event{Kind: claude.KindToken, Delta: "stale text"}
	old <- claude.Event{Kind: claude.KindRetry, Reason: "stale retry"}
	old <- claude.Event{Kind: claude.KindDone, Err: errors.New("stale error")}
	close(old)
	for range 4 { // Includes the closed-channel completion message.
		_, cmd := m.Update(sections.NextChatEvent(old)())
		require.Nil(t, cmd)
		require.True(t, m.chat.Streaming())
		require.Equal(t, (<-chan claude.Event)(current), m.chatStreamCh)
		require.Empty(t, m.chat.YankPayload())
	}

	current <- claude.Event{Kind: claude.KindToken, Delta: "new answer"}
	current <- claude.Event{Kind: claude.KindDone}
	m.Update(sections.NextChatEvent(current)())
	require.Equal(t, "new answer", m.chat.YankPayload())
	m.Update(sections.NextChatEvent(current)())
	require.False(t, m.chat.Streaming())
	require.Nil(t, m.chatStreamCh)
}

func TestClearChatStopsPendingTurn(t *testing.T) {
	m := NewModel(&k8s.Manager{}, &config.AppConfig{})
	t.Cleanup(m.ctx.Cancel)
	m.chat.SetSize(80, 24)
	m.chat.SetFocus(true)
	m.chat.Update(tea.PasteMsg{Content: "prompt"}, m.ctx)
	m.chat.Update(tea.KeyPressMsg{Code: tea.KeyEnter}, m.ctx)
	require.True(t, m.chat.Streaming())
	m.chat.Update(tea.KeyPressMsg{Code: 'l', Mod: tea.ModCtrl}, m.ctx)
	require.False(t, m.chat.Streaming())
	m.chat.Update(tea.PasteMsg{Content: "next prompt"}, m.ctx)
	_, submit, _ := m.chat.Update(tea.KeyPressMsg{Code: tea.KeyEnter}, m.ctx)
	require.NotNil(t, submit, "clearing chat must allow another prompt immediately")
}
