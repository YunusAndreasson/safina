package textw

import (
	"math/rand/v2"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// Runes that can glue onto the previous one or change its width.
var joiners = []rune{
	0x0301, 0x036f, 0x200c, 0x200d, 0x20e3, 0xfe0e, 0xfe0f, 0x1f3fb,
	0x0903, 0x0e33, 0x1160, 0x11a8, 0xe0020, 0x1f1f8, 0x00ad, 0x0600,
}

// Every rune the fast path admits must measure the same alone, before and
// after anything that might extend it, and next to its neighbours.
func TestWidthMatchesAnsiForEveryTableRune(t *testing.T) {
	singleOnce.Do(buildSingle)
	n := 0
	for r := range rune(tableEnd) {
		if !single[r] {
			continue
		}
		n++
		c := string(r)
		cases := make([]string, 0, 4+3*len(joiners))
		cases = append(cases, c, "a"+c+"b", c+c, "\x1b[1;38;2;1;2;3m"+c+"\x1b[m")
		for _, j := range joiners {
			cases = append(cases, c+string(j), string(j)+c, c+string(j)+c)
		}
		for _, s := range cases {
			require.Equal(t, ansi.StringWidth(s), StringWidth(s), "%q", s)
		}
	}
	require.Greater(t, n, 3000, "the table should admit the common symbol blocks")
}

func randomText(rng *rand.Rand) string {
	pieces := []string{
		"a", "Z", " ", "~", "│", "─", "╭", "⠋", "●", "▁", "…", "→", "✓", "é", "°",
		"\x1b[0m", "\x1b[38;5;203m", "\x1b[1;48;2;10;20;30m", "\x1b[m",
		"\x1b]8;;https://x\x1b\\link\x1b]8;;\x1b\\", "\t", "日本", "🚀", "e\u0301",
		"☺\ufe0f", "👍🏽", "\x07", "\x1b[", "\x1bP", "\u200d",
	}
	var b strings.Builder
	for range rng.IntN(30) {
		b.WriteString(pieces[rng.IntN(len(pieces))])
	}
	return b.String()
}

func TestWidthMatchesAnsiOnMixedText(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2)) //nolint:gosec // reproducible test input
	for range 20000 {
		s := randomText(rng)
		require.Equal(t, ansi.StringWidth(s), StringWidth(s), "%q", s)
		b := randomBlock(rng)
		require.Equal(t, lipgloss.Width(b), Width(b), "%q", b)
	}
}

func randomBlock(rng *rand.Rand) string {
	ls := make([]string, 1+rng.IntN(6))
	for i := range ls {
		ls[i] = randomText(rng)
	}
	sep := "\n"
	if rng.IntN(5) == 0 {
		sep = "\r\n"
	}
	return strings.Join(ls, sep)
}

func TestJoinsMatchLipgloss(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 4)) //nolint:gosec // reproducible test input
	positions := []lipgloss.Position{lipgloss.Top, lipgloss.Bottom, lipgloss.Center, 0.2, 0.7, -1, 2}
	for range 5000 {
		blocks := make([]string, rng.IntN(5))
		for i := range blocks {
			blocks[i] = randomBlock(rng)
		}
		pos := positions[rng.IntN(len(positions))]
		require.Equal(t, lipgloss.JoinVertical(pos, blocks...), JoinVertical(pos, blocks...), "vertical %v %q", pos, blocks)
		require.Equal(t, lipgloss.JoinHorizontal(pos, blocks...), JoinHorizontal(pos, blocks...), "horizontal %v %q", pos, blocks)
	}
}

func BenchmarkWidth(b *testing.B) {
	line := "│ \x1b[38;5;203m●\x1b[m api-123-7f77847d67-00123   shop-1   \x1b[1mRunning\x1b[m   2d   ▁▂▃▅▇ 120m │"
	b.Run("textw", func(b *testing.B) {
		for b.Loop() {
			StringWidth(line)
		}
	})
	b.Run("ansi", func(b *testing.B) {
		for b.Loop() {
			ansi.StringWidth(line)
		}
	})
}
