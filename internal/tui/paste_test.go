package tui

import (
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"

	"github.com/yunus/safina/internal/config"
	"github.com/yunus/safina/internal/k8s"
	"github.com/yunus/safina/internal/tui/sections"
)

func TestPasteStaysInFocusedInput(t *testing.T) {
	m := NewModel(&k8s.Manager{}, &config.AppConfig{})
	m.Update(keyMsg("/"))
	m.Update(tea.PasteMsg{Content: "qK\nrS"})
	require.Equal(t, stateMain, m.state)
	require.True(t, m.tree.FilterActive())
	require.Contains(t, m.tree.FilterValue(), "qK")
	m.Update(keyMsg("esc"))
	m.state = statePalette
	m.Update(tea.PasteMsg{Content: "qa-web\nK"})
	require.Equal(t, "qa-web K", m.palette.Query())
	require.Equal(t, statePalette, m.state)
	m.state = statePortForwardInput
	m.pfInput.Focus()
	m.Update(tea.PasteMsg{Content: "18080:8080"})
	require.Equal(t, "18080:8080", m.pfInput.Value())
	require.Equal(t, statePortForwardInput, m.state)
}

func TestStaleLoadDoesNotReplaceCurrentErrorOrLoading(t *testing.T) {
	m := NewModel(&k8s.Manager{}, &config.AppConfig{})
	m.tree.PushContext("healthy")
	m.tree.Loading = true
	m.bannerMsg = "current message"
	m.Update(sections.TreeChildrenLoadedMsg{
		Path: []sections.NodeRef{{Kind: k8s.KindContext, Name: "old-cluster"}},
		Err:  errors.New("old connection refused"),
	})
	require.True(t, m.tree.Loading)
	require.Equal(t, "current message", m.bannerMsg)
	require.NoError(t, m.tree.LoadErr)
}
