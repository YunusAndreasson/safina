package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/require"

	"github.com/YunusAndreasson/safina/internal/config"
	"github.com/YunusAndreasson/safina/internal/k8s"
)

func TestEditDiffFitsAndScrolls(t *testing.T) {
	m := NewModel(&k8s.Manager{}, &config.AppConfig{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.state = stateEditConfirm
	m.edit = &editSession{
		Kind: "ConfigMap", Namespace: "qa", Name: "settings",
		Orig:   []byte("data: old\n"),
		Edited: []byte("data: " + strings.Repeat("long annotation ", 100) + "\nlast: visible-at-end\n"),
	}
	card := m.renderEditConfirmModal()
	require.LessOrEqual(t, lipgloss.Width(card), 80)
	require.LessOrEqual(t, lipgloss.Height(card), 24)
	require.Contains(t, card, "apply")
	require.Contains(t, card, "cancel")
	require.NotContains(t, card, "visible-at-end")
	for range 20 {
		m.handleConfirm(tea.KeyPressMsg{Code: tea.KeyPgDown})
	}
	require.Contains(t, m.renderEditConfirmModal(), "visible-at-end")
	require.Equal(t, stateEditConfirm, m.state)
}
