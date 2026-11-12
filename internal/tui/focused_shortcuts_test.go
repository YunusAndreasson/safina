package tui

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFiltersGetLiteralShortcutLetters(t *testing.T) {
	m := shiftFooterModel(t)
	m.handleKey(keyMsg("/"))
	require.True(t, m.tree.FilterActive())
	for _, letter := range []string{"y", "l", "e", "1", "i"} {
		m.handleKey(keyMsg(letter))
	}
	require.Equal(t, "yle1i", m.tree.FilterValue(), "a list filter must not lose letters to shortcuts")
	require.Equal(t, stateMain, m.state)
	require.NotEqual(t, "copied", m.bannerMsg)
}
