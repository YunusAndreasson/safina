package sections

import (
	"testing"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/sahilm/fuzzy"
	"github.com/stretchr/testify/require"
)

func TestFuzzyHighlightPreservesUnicodeAndMatchesRune(t *testing.T) {
	for _, tc := range []struct{ value, query string }{{"café 東京", "京"}, {"café", "é"}, {"cart", "a"}} {
		matches := fuzzy.Find(tc.query, []string{tc.value})
		require.Len(t, matches, 1)
		style := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#ffff00"))
		text := highlightMatches(tc.value, matches[0].MatchedIndexes, lipgloss.NewStyle(), style)
		require.True(t, utf8.ValidString(text))
		require.Equal(t, tc.value, ansi.Strip(text))
		require.Contains(t, text, style.Render(tc.query))
	}
}
