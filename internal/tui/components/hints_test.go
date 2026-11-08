package components

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/YunusAndreasson/safina/internal/tui/theme"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestBoundedHintsNeverCutShortcuts(t *testing.T) {
	pairs := []KeyDesc{{Key: "↵", Desc: "send"}, {Key: "tab", Desc: "tree"}, {Key: "⇧↵", Desc: "newline"}, {Key: "⌃l", Desc: "clear"}}
	for width := range 100 {
		for _, overflow := range []KeyDesc{{}, {Key: "?", Desc: "help"}} {
			line := RenderBoundedHints(theme.Default(), pairs, width, overflow)
			require.LessOrEqual(t, lipgloss.Width(line), width)
			if line == "" {
				continue
			}
			allowed := []string{"↵ send", "tab tree", "⇧↵ newline", "⌃l clear", "? help"}
			for _, hint := range strings.Split(ansi.Strip(line), " · ") {
				require.Contains(t, allowed, hint)
			}
			if width >= 6 && width < lipgloss.Width(RenderModalKeysRow(theme.Default(), pairs)) && overflow.Key != "" {
				require.Contains(t, ansi.Strip(line), "? help")
			}
		}
	}
}
