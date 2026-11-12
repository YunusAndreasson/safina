package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/YunusAndreasson/safina/internal/config"
	"github.com/YunusAndreasson/safina/internal/tui/sections"
	"github.com/stretchr/testify/require"
)

func TestResizeInvalidatesMouseTargetsUntilRepaint(t *testing.T) {
	m := connectedModel(t)
	path := append([]sections.NodeRef(nil), m.tree.Path()...)
	x := m.bodyOriginX + m.connected.PanelLeft + 3
	y := m.bodyOriginY + m.connected.PanelTop + 1
	m.Update(tea.WindowSizeMsg{Width: 50, Height: 14})
	m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	require.Equal(t, path, m.tree.Path())
	require.Nil(t, m.paletteTarget)
	require.Empty(t, m.tabHitboxes)
	require.Zero(t, m.connected.PanelHeight)
}

func TestMouseWheelMovesOverlaySelectionAndScrollsHelp(t *testing.T) {
	m := shiftFooterModel(t)
	m.state = statePalette
	m.palette.Open([]sections.PaletteEntry{{Name: "first"}, {Name: "second"}})
	m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	selected, ok := m.palette.Selected()
	require.True(t, ok)
	require.Equal(t, "second", selected.Name)
	m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	selected, _ = m.palette.Selected()
	require.Equal(t, "first", selected.Name)
	m.state = stateClusters
	m.clusters.Open([]config.ContextInfo{{Name: "first"}, {Name: "second"}}, "first")
	m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	cluster, ok := m.clusters.Selected()
	require.True(t, ok)
	require.Equal(t, "second", cluster.Name)
	m.state = stateHelp
	m.View()
	before := m.helpViewport.YOffset()
	m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	require.Greater(t, m.helpViewport.YOffset(), before)
}
