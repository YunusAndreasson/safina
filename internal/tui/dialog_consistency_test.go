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

func TestInputDialogsShareScopeAndCursorLayout(t *testing.T) {
	for _, size := range [][2]int{{50, 14}, {60, 18}, {80, 24}, {140, 42}} {
		m := NewModel(&k8s.Manager{}, &config.AppConfig{})
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		m.ctx.ActiveContext = "eu-north-1"
		m.pfTarget.Kind = "Pod"
		m.pfTarget.Name = "cart-abc12"
		m.pfTarget.Namespace = "storefront"
		m.pfInput.SetValue("8080:80")
		card := m.renderPortForwardModal()
		plain := ansi.Strip(card)
		require.Contains(t, plain, "cluster: eu-north-1")
		require.Contains(t, plain, "namespace: storefront")
		require.Contains(t, plain, "↵ start")
		require.LessOrEqual(t, lipgloss.Width(card), size[0])
		require.LessOrEqual(t, lipgloss.Height(card), size[1])
		require.Contains(t, strings.Split(plain, "\n")[m.modalInputRow+2], "8080:80")
		require.Equal(t, lipgloss.Width(card), lipgloss.Width(m.renderScaleModal()))
	}
}
