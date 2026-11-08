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

func TestStartupSailAcrossLayoutsAndPaneFocus(t *testing.T) {
	m := NewModel(&k8s.Manager{}, &config.AppConfig{})
	t.Cleanup(m.ctx.Cancel)
	for _, contexts := range [][]config.ContextInfo{nil, {{Name: "production"}, {Name: "staging"}}} {
		m.SetContexts(contexts)
		for _, size := range [][2]int{{50, 14}, {80, 24}, {120, 36}, {160, 48}} {
			m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			for _, peek := range []bool{false, true} {
				m.peekWorkspace = peek
				frame := m.View().Content
				require.Equal(t, size[1], lipgloss.Height(frame))
				for _, row := range strings.Split(frame, "\n") {
					require.LessOrEqual(t, ansi.StringWidth(row), size[0])
				}
				plain := ansi.Strip(frame)
				require.NotContains(t, plain, "pick a cluster on the left to begin")
				if size[0] >= 80 {
					require.Contains(t, plain, "s a f i n a")
					require.NotContains(t, plain, "Inspector")
					require.Empty(t, m.tabHitboxes)
					require.Contains(t, plain, "#")
				}
			}
		}
	}
}

func TestReturningToClusterPickerRestoresSail(t *testing.T) {
	m := shiftFooterModel(t)
	m.View()
	require.NotContains(t, ansi.Strip(m.renderWorkspace(70, 28, false)), "s a f i n a")
	m.handleKey(keyMsg("esc"))
	require.Equal(t, k8s.KindContextList, m.tree.Focus().Kind)
	require.NotEmpty(t, m.ctx.ActiveContext, "remembered context must not hide the welcome screen")
	pane := ansi.Strip(m.renderWorkspace(70, 28, false))
	require.Contains(t, pane, "s a f i n a")
	require.NotContains(t, pane, "Inspector")
	require.NotContains(t, pane, "nothing selected")
}
