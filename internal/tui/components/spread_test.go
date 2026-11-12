package components

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSpreadColumnsSharesSpareWidthBetweenColumns(t *testing.T) {
	widths := []int{10, 5, 5}
	aligns := []Align{AlignLeft, AlignRight, AlignRight}
	labels := []string{"NAME", "READY", "AGE"}
	sp, w, a, l, left := SpreadColumns(widths, aligns, labels, 21, 8)
	require.Equal(t, 5, left, "two gaps of 8; 5 cells are the caller's")
	require.Equal(t, RowOuterWidth(widths)+21-left, RowOuterWidth(w))
	require.Len(t, a, len(w))
	require.Equal(t, []string{"NAME", "", "READY", "", "AGE"}, l)
	require.Equal(t, []string{"x", "", "y", "", "z"}, sp.Vals([]string{"x", "y", "z"}))

	_, w, _, _, left = SpreadColumns(widths, aligns, labels, 3, 8)
	require.Equal(t, widths, w, "too little to share")
	require.Equal(t, 3, left)
}
