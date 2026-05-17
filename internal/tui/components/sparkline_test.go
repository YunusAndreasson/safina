package components

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/assert"

	"github.com/yunus/safina/internal/tui/theme"
)

// brailleFloor is the 1-cell "flat floor" pattern (h=1 dots on both halves)
// the idle branch of Sparkline emits — single bottom-row dots in each half.
var brailleFloor = string(brailleBase + rune(brailleLeft[1]+brailleRight[1]))

// brailleFull is the 1-cell solid block (h=4 on both halves) the busy
// branch emits at full magnitude — every dot lit.
var brailleFull = string(brailleBase + rune(brailleLeft[4]+brailleRight[4]))

// TestSparkline_IdleRendersAsFloor is the regression test for the bug the
// user surfaced: a pod stable at 1 millicore previously rendered ████ in
// red because the buffer normalised to local-max (1/1 = 100%). After the
// floor-ceiling rewrite + braille upgrade it should render as a dim flat
// braille floor.
func TestSparkline_IdleRendersAsFloor(t *testing.T) {
	th := theme.Default()
	bg := lipgloss.NewStyle()

	buf := NewMetricBufferWithFloor(32, 1000) // CPU: 1 core floor
	for range 10 {
		buf.Push(1)
	}

	out := stripStyles(Sparkline(th, buf, 4, bg))
	assert.Equal(t, strings.Repeat(brailleFloor, 4), out,
		"idle should render as 4 floor cells, got %q", out)
}

// TestSparkline_BusyHitsTopBlock verifies the absolute-percent colouring
// at the top of the gradient. A pod pushing 950m against a 1000m ceiling
// should render at top block height in the trailing cell.
func TestSparkline_BusyHitsTopBlock(t *testing.T) {
	th := theme.Default()
	bg := lipgloss.NewStyle()

	buf := NewMetricBufferWithFloor(32, 1000)
	for range 16 {
		buf.Push(950)
	}

	out := stripStyles(Sparkline(th, buf, 4, bg))
	cells := []rune(out)
	assert.Equal(t, brailleFull, string(cells[3]),
		"trailing cell should be full braille block, got %q", out)
}

// TestSparkline_RatchetsCeiling proves the autoscale step. Push values
// that exceed the floor; ceiling jumps to the next 2× multiple. Until
// then a 1500m sample against a 1000m floor would have rendered above
// the top block (1500/1000 = 1.5) — the new code clips, then ratchets.
func TestSparkline_RatchetsCeiling(t *testing.T) {
	buf := NewMetricBufferWithFloor(32, 1000)
	buf.Push(500)
	assert.Equal(t, int64(1000), buf.Ceiling(), "below floor: ceiling stays at floor")

	buf.Push(1500)
	assert.Equal(t, int64(2000), buf.Ceiling(), "exceeds floor: doubles")

	buf.Push(3000)
	assert.Equal(t, int64(4000), buf.Ceiling(), "exceeds 2x: doubles again")

	buf.Push(100)
	assert.Equal(t, int64(4000), buf.Ceiling(), "high-water is sticky; never shrinks")
}

// TestSparkline_NoFloorFallsBackToHighWater covers the legacy constructor
// (NewMetricBuffer with no floor) — used for unknown-magnitude series.
// Ceiling becomes high-water + 20%.
func TestSparkline_NoFloorFallsBackToHighWater(t *testing.T) {
	buf := NewMetricBuffer(32)
	buf.Push(100)
	buf.Push(200)
	assert.Equal(t, int64(240), buf.Ceiling(), "200 + 20%")
}

// TestSparkline_FewerThanTwoSamples covers the "data forthcoming" branch.
func TestSparkline_FewerThanTwoSamples(t *testing.T) {
	th := theme.Default()
	bg := lipgloss.NewStyle()

	buf := NewMetricBufferWithFloor(32, 1000)
	out := stripStyles(Sparkline(th, buf, 4, bg))
	assert.Equal(t, "····", out, "zero samples → dots")

	buf.Push(50)
	out = stripStyles(Sparkline(th, buf, 4, bg))
	assert.Equal(t, "····", out, "one sample → still dots (need ≥2 for trend)")
}

// TestSparkline_VariableTrendRenders proves the normal case still
// produces a sensible mix of heights when there's actual variation.
func TestSparkline_VariableTrendRenders(t *testing.T) {
	th := theme.Default()
	bg := lipgloss.NewStyle()

	buf := NewMetricBufferWithFloor(32, 1000)
	// Climbing trend across full sample capacity (width=4 ⇒ 8 samples).
	for _, v := range []int64{50, 150, 250, 350, 450, 550, 650, 800} {
		buf.Push(v)
	}
	out := []rune(stripStyles(Sparkline(th, buf, 4, bg)))

	// We expect monotonic non-decreasing per-cell max-level since input
	// is monotonic. Heights map to dot patterns, decoded by maxLevel.
	for i := 1; i < len(out); i++ {
		assert.GreaterOrEqual(t, brailleMaxLevel(out[i]), brailleMaxLevel(out[i-1]),
			"climbing input should produce non-decreasing braille heights, got %q",
			string(out))
	}
}

// brailleMaxLevel decodes a braille-bar cell back to the max of its two
// half-column heights (0..4). Inverse of the encoding in sparkline.go.
func brailleMaxLevel(r rune) int {
	if r < brailleBase || r > brailleBase+0xFF {
		return -1
	}
	pat := int(r - brailleBase)
	left := decodeHalf(pat&0x47, brailleLeft[:])
	right := decodeHalf(pat&0xB8, brailleRight[:])
	if left > right {
		return left
	}
	return right
}

func decodeHalf(bits int, table []int) int {
	for i, v := range table {
		if v == bits {
			return i
		}
	}
	return -1
}

// stripStyles strips lipgloss ANSI escapes from a rendered string so
// tests assert on the underlying glyphs without color noise. lipgloss
// wraps each cell separately, so this leaves a clean rune sequence.
func stripStyles(s string) string {
	out := strings.Builder{}
	in := false
	for _, r := range s {
		switch {
		case r == 0x1b:
			in = true
		case in && r == 'm':
			in = false
		case !in:
			out.WriteRune(r)
		}
	}
	return out.String()
}
