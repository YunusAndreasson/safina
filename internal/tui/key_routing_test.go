package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"

	"github.com/YunusAndreasson/safina/internal/config"
	"github.com/YunusAndreasson/safina/internal/k8s"
	"github.com/YunusAndreasson/safina/internal/tui/sections"
)

func TestInvalidPortIsVisibleInsideDialog(t *testing.T) {
	m := NewModel(&k8s.Manager{}, &config.AppConfig{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.state = statePortForwardInput
	m.pfInput.SetValue("99999")
	_, cmd := m.Update(keyMsg("enter"))
	require.Nil(t, cmd)
	require.Equal(t, statePortForwardInput, m.state)
	require.Contains(t, m.View().Content, "invalid port pair")
	m.Update(keyMsg("8"))
	require.Empty(t, m.pfError)
}

func TestTreeFilterConsumesActionLetters(t *testing.T) {
	m := NewModel(&k8s.Manager{}, &config.AppConfig{})
	m.tree.PushContext("demo")
	m.tree.SetChildren(m.tree.Path(), &sections.Children{Kind: k8s.KindApp, Apps: []k8s.AppInfo{
		{Kind: k8s.AppKindDeployment, Name: "qa-web", Namespace: "qa"},
	}})
	m.Update(keyMsg("/"))
	query := "safina-qa?rdleSKUR:hivfy"
	for _, ch := range query {
		m.Update(keyMsg(string(ch)))
		require.Equal(t, stateMain, m.state, "filter character %q must not activate a shortcut", ch)
		require.True(t, m.tree.FilterActive())
	}
	require.Equal(t, query, m.tree.FilterValue())
	m.Update(keyMsg("enter"))
	require.False(t, m.tree.FilterActive())
	require.Equal(t, query, m.tree.FilterValue())
}

func TestSectionLocalKeysWinOverResourceActions(t *testing.T) {
	for _, tc := range []struct {
		state state
		key   string
	}{
		{stateForwardsList, "K"},
		{stateLogView, "f"},
		{stateEventsView, "r"},
		{stateDescribeView, "r"},
		{stateHotView, "r"},
	} {
		m := NewModel(&k8s.Manager{}, &config.AppConfig{})
		m.state = tc.state
		_, _ = m.Update(keyMsg(tc.key))
		require.Equal(t, tc.state, m.state, "key %q must stay in its section", tc.key)
	}
}
