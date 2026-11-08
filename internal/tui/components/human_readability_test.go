package components

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/YunusAndreasson/safina/internal/tui/theme"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestReplicaNamesKeepDistinctSuffixes(t *testing.T) {
	first := TruncMiddlePad("ledger-api-b86f95fc8-cb929", 12)
	second := TruncMiddlePad("ledger-api-b86f95fc8-pqfln", 12)
	require.NotEqual(t, first, second)
	require.Contains(t, first, "cb929")
	require.Contains(t, second, "pqfln")
	require.Equal(t, 12, ansi.StringWidth(first))
}

func TestStatusRowStaysOnOneLineWithLongFilter(t *testing.T) {
	th := theme.Default()
	f := NewFilter(th)
	f.input.SetValue(strings.Repeat("filter", 20))
	f.Close()
	row := RenderModalStatusBar(th, 50, f, []Indicator{{Text: "123/500 matches"}, {Text: "scroll paused"}, {Text: "UTC"}})
	require.LessOrEqual(t, lipgloss.Width(row), 48)
	require.Equal(t, 1, lipgloss.Height(row))
}
