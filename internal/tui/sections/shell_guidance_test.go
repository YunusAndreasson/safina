package sections

import (
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/YunusAndreasson/safina/internal/tui/theme"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestShellHintsDescribeActualSession(t *testing.T) {
	s := Shell{phase: phaseLive, gridCols: 120}
	require.NotContains(t, ansi.Strip(s.keysRow(theme.Default())), "debug")
	s.isDebug = true
	require.Contains(t, ansi.Strip(s.keysRow(theme.Default())), "until pod deletion")
	s.gridCols = 30
	line := s.keysRow(theme.Default())
	require.LessOrEqual(t, lipgloss.Width(line), 30)
	require.Contains(t, ansi.Strip(line), "esc detach")
	require.Contains(t, ansi.Strip(line), "ctrl+c interrupt")
}
