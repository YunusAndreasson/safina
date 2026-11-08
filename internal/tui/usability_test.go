package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
	"github.com/yunus/safina/internal/config"
	"github.com/yunus/safina/internal/k8s"
)

func TestHelpFitsAndReturnsToWorkspace(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {60, 18}, {50, 14}, {140, 42}} {
		m := NewModel(&k8s.Manager{}, &config.AppConfig{})
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		m.state = stateDescribeView
		m.handleKey(keyMsg("?"))
		require.Equal(t, stateHelp, m.state)
		card := m.renderHelpModal()
		require.LessOrEqual(t, lipgloss.Width(card), size[0])
		require.LessOrEqual(t, lipgloss.Height(card), size[1])
		require.Contains(t, ansi.Strip(card), "esc/? close")
		for range 30 {
			m.handleKey(tea.KeyPressMsg{Code: tea.KeyPgDown})
		}
		require.Contains(t, ansi.Strip(m.renderHelpModal()), "quit")
		m.handleKey(keyMsg("esc"))
		require.Equal(t, stateDescribeView, m.state)
	}
}

func TestTreeFilterFooterDoesNotAdvertiseActions(t *testing.T) {
	m := NewModel(&k8s.Manager{}, &config.AppConfig{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.handleKey(keyMsg("/"))
	footer := ansi.Strip(m.renderFooter())
	require.Contains(t, footer, "↵ apply")
	require.NotContains(t, footer, "quit")
	m.handleKey(keyMsg("?"))
	require.Equal(t, stateMain, m.state)
	require.Equal(t, "?", m.tree.FilterValue())
}

func TestHelpKeyDescriptionsHaveSeparation(t *testing.T) {
	m := NewModel(&k8s.Manager{}, &config.AppConfig{})
	for _, group := range m.ctx.Keys.Groups() {
		text := ansi.Strip(renderHelpGroup(m.ctx.Theme, group))
		require.NotContains(t, text, "ctrl+fforwards")
		require.NotContains(t, text, "tab also")
		if strings.Contains(text, "ctrl+f") {
			require.Contains(t, text, "ctrl+f  forwards")
		}
	}
}

func TestInvalidScaleKeepsEditableForm(t *testing.T) {
	m := NewModel(&k8s.Manager{}, &config.AppConfig{})
	for _, value := range []string{"", "-1", "2147483648", "not-a-number"} {
		m.state = stateScaleInput
		m.scaleInput.Focus()
		m.scaleInput.SetValue(value)
		m.handleScaleInput(keyMsg("enter"))
		require.Equal(t, stateScaleInput, m.state)
		require.True(t, m.scaleInput.Focused())
		require.Equal(t, value, m.scaleInput.Value())
		require.NotEmpty(t, m.scaleError)
	}
	m.handleScaleInput(keyMsg("esc"))
	require.Equal(t, stateMain, m.state)
}

func TestActionConfirmationIdentifiesClusterAndNamespace(t *testing.T) {
	m := NewModel(&k8s.Manager{}, &config.AppConfig{})
	m.ctx.ActiveContext = "eu-north-1"
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	require.Equal(t, "cluster: eu-north-1\nnamespace: storefront", m.actionScope("storefront"))
	require.Contains(t, ansi.Strip(m.renderConfirmModal("delete pod", "web")), "cluster: eu-north-1")
}

func TestWrappedBannerKeepsFooterAndMouseCoordinatesAligned(t *testing.T) {
	m := NewModel(&k8s.Manager{}, &config.AppConfig{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.bannerMsg = strings.Repeat("connection refused ", 12)
	m.bannerErr = true
	view := m.View()
	require.LessOrEqual(t, lipgloss.Height(view.Content), 24)
	require.Equal(t, 2+lipgloss.Height(m.renderBanner(80)), m.tabStripOriginY)
}

func TestCtrlCExitsOverlaysAndWorkspaceViews(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, current := range []state{stateMain, statePalette, stateBrowser, stateClusters, stateHelp, stateScaleInput, statePortForwardInput, stateDeleteConfirm, stateLogView, stateEventsView, stateDescribeView, stateHotView, stateForwardsList, stateChatView} {
		m := NewModel(&k8s.Manager{}, &config.AppConfig{})
		m.state = current
		_, cmd := m.handleKey(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
		require.NotNil(t, cmd, "state %v must allow exit", current)
		require.IsType(t, tea.QuitMsg{}, cmd())
	}
}
