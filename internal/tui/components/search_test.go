package components

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/YunusAndreasson/safina/internal/tui/theme"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestSearchInputScrollsToEditingPosition(t *testing.T) {
	var input SearchInput
	text := "START-" + strings.Repeat("long-", 40) + "末尾"
	input.SetValue(text)
	view := input.View(theme.Default(), 24)
	require.LessOrEqual(t, lipgloss.Width(view), 24)
	require.Contains(t, ansi.Strip(view), "末尾")
	_, changed := input.Update(tea.KeyPressMsg{Code: tea.KeyHome})
	require.False(t, changed)
	view = input.View(theme.Default(), 24)
	require.Contains(t, ansi.Strip(view), "START-")
	require.Equal(t, text, input.Value())
}
