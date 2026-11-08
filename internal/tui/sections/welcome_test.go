package sections

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
	"github.com/yunus/safina/internal/tui/theme"
)

func TestWelcomeSailFitsPaneAndMonochrome(t *testing.T) {
	for _, size := range [][2]int{{1, 1}, {8, 3}, {18, 8}, {28, 16}, {34, 22}, {48, 28}, {70, 32}, {100, 44}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			frame := WelcomeSail(theme.Default(), size[0], size[1])
			require.Equal(t, size[1], lipgloss.Height(frame))
			for _, row := range strings.Split(frame, "\n") {
				require.Equal(t, size[0], ansi.StringWidth(row))
			}
			plain := ansi.Strip(frame)
			if size[0] >= 10 {
				require.Contains(t, plain, "s a f i n a")
			}
			if size[1] >= 16 {
				require.Contains(t, plain, "#", "the sail remains visible without color")
			}
			if size[1] >= 22 {
				require.Contains(t, strings.Join(strings.Fields(plain), " "), "The Kubernetes TUI for teams that ship.")
			}
		})
	}
	require.Empty(t, WelcomeSail(theme.Default(), 0, 10))
	require.Empty(t, WelcomeSail(theme.Default(), 10, 0))
}

func TestWelcomeSailUsesPortableASCII(t *testing.T) {
	for _, art := range []string{sail16, sail28, sail40} {
		for _, r := range art {
			require.True(t, r == '\n' || (r >= ' ' && r <= '~'), "logo must render without Unicode block or braille glyphs")
		}
	}
}
