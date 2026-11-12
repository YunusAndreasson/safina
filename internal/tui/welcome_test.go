package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/YunusAndreasson/safina/internal/config"
	"github.com/YunusAndreasson/safina/internal/k8s"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestHarbourShowsTheSailAboveTheClustersAtEverySize(t *testing.T) {
	m := NewModel(&k8s.Manager{}, &config.AppConfig{})
	t.Cleanup(m.ctx.Cancel)
	for _, contexts := range [][]config.ContextInfo{nil, {{Name: "production"}, {Name: "staging"}}} {
		m.SetContexts(contexts)
		for _, size := range [][2]int{{50, 14}, {80, 24}, {120, 36}, {160, 48}} {
			m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			frame := m.View().Content
			require.Equal(t, size[1], lipgloss.Height(frame))
			for _, row := range strings.Split(frame, "\n") {
				require.LessOrEqual(t, ansi.StringWidth(row), size[0])
			}
			plain := ansi.Strip(frame)
			require.Contains(t, plain, "s a f i n a")
			require.Contains(t, plain, "The Kubernetes TUI")
			require.Empty(t, m.tabHitboxes)
			require.Empty(t, m.backLabel(), "the Harbour is the root: nothing to go back to")
			for _, c := range contexts {
				require.Contains(t, plain, c.Name, "the cluster list always fits at %v", size)
			}
			if size[1] >= 24 {
				require.Contains(t, plain, "#", "the sail shows when there is room")
			}
		}
	}
}

func TestReturningToTheHarbourRestoresTheSail(t *testing.T) {
	m := shiftFooterModel(t)
	require.NotContains(t, ansi.Strip(m.renderPage(70, 28)), "s a f i n a")
	m.handleKey(keyMsg("esc"))
	require.Equal(t, k8s.KindContextList, m.tree.Focus().Kind)
	require.NotEmpty(t, m.ctx.ActiveContext, "remembered context must not hide the welcome screen")
	page := ansi.Strip(m.renderPage(70, 28))
	require.Contains(t, page, "s a f i n a")
	require.NotContains(t, page, "Details")
}
