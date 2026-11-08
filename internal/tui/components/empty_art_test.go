package components

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"charm.land/bubbles/v2/viewport"
	"charm.land/lipgloss/v2"
	"github.com/YunusAndreasson/safina/internal/tui/theme"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestIllustratedEmptyFitsAndKeepsMessagesBeforeArt(t *testing.T) {
	for _, motif := range []EmptyArt{NoEmptyArt, BeaconArt, AnchorArt} {
		for _, size := range [][2]int{{1, 1}, {12, 3}, {24, 7}, {28, 16}, {40, 20}, {64, 30}, {100, 44}} {
			t.Run(fmt.Sprintf("%d/%dx%d", motif, size[0], size[1]), func(t *testing.T) {
				frame := IllustratedEmpty(theme.Default(), size[0], size[1], motif, "no resources", "Esc returns to the previous list.")
				require.Equal(t, size[1], lipgloss.Height(frame))
				for _, line := range strings.Split(frame, "\n") {
					require.Equal(t, size[0], ansi.StringWidth(line))
				}
				plain := ansi.Strip(frame)
				if size[0] >= 24 && size[1] >= 7 {
					require.Contains(t, plain, "no resources")
					require.Contains(t, strings.Join(strings.Fields(plain), " "), "Esc returns to the previous list.")
				}
				if size[1] < 10 {
					require.NotContains(t, plain, "#")
				}
				if size[0] >= 64 && size[1] >= 30 && motif != NoEmptyArt {
					require.Contains(t, plain, "#")
				}
			})
		}
	}
	require.Empty(t, IllustratedEmpty(theme.Default(), 0, 20, BeaconArt, "empty", ""))
	require.Empty(t, IllustratedEmpty(theme.Default(), 20, 0, BeaconArt, "empty", ""))
}

func TestNauticalArtworkIsPrintableASCII(t *testing.T) {
	for _, art := range []string{beaconLarge, beaconSmall, anchorLarge, anchorSmall} {
		for _, r := range art {
			require.True(t, r == '\n' || (r >= ' ' && r <= '~'))
		}
	}
}

func TestEmptyArtworkNeverReplacesErrorsLoadingOrData(t *testing.T) {
	t1 := theme.Default()
	vp := viewport.New(viewport.WithWidth(64), viewport.WithHeight(30))
	vp.SetContent("actual resource data")
	for _, state := range []BlockState{
		{Err: errors.New("permission denied"), EmptyArt: BeaconArt},
		{Loading: true, Loading_: "loading resources", EmptyArt: AnchorArt},
		{Loaded: true, EmptyArt: BeaconArt},
	} {
		text := ansi.Strip(CenteredViewportBlock(t1, "*", vp, state))
		require.NotContains(t, text, "#")
		if state.Err != nil {
			require.Contains(t, text, "permission denied")
		}
		if state.Loaded {
			require.Contains(t, text, "actual resource data")
		}
	}
}
