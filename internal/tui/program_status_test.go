package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"

	"github.com/yunus/safina/internal/config"
	"github.com/yunus/safina/internal/k8s"
)

func TestProgramStatusChatAndConfirmation(t *testing.T) {
	m := NewModel(&k8s.Manager{}, &config.AppConfig{})
	require.Equal(t, tea.ProgramStateIdle, m.View().ProgramStatus.State)

	m.chat.SetSize(80, 24)
	m.chat.SetFocus(true)
	m.chat.Update(tea.PasteMsg{Content: "private prompt"}, m.ctx)
	_, submit, _ := m.chat.Update(tea.KeyPressMsg{Code: tea.KeyEnter}, m.ctx)
	require.NotNil(t, submit)
	require.Equal(t, tea.ProgramStateWorking, m.View().ProgramStatus.State)
	require.NotContains(t, m.View().ProgramStatus.Message, "private prompt")

	// A confirmation takes priority even while the background chat runs.
	m.state = stateDeleteConfirm
	require.Equal(t, tea.ProgramStateBlocked, m.View().ProgramStatus.State)
	require.Equal(t, tea.ProgramStatusKindPermission, m.View().ProgramStatus.Kind)
	m.state = stateMain
	require.Equal(t, tea.ProgramStateWorking, m.View().ProgramStatus.State)

	m.chat.CancelStream()
	require.Equal(t, tea.ProgramStateIdle, m.View().ProgramStatus.State)
	require.Empty(t, m.View().ProgramStatus.Kind)
}
