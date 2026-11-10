// Package textw measures and joins styled terminal text.
//
// lipgloss measures every line with ansi.StringWidth, which segments each
// string into grapheme clusters. That is most of a frame's cost: a full
// redraw measures every screen line several times over while panels are
// joined. Almost everything safina draws is ASCII, SGR escapes, box drawing,
// braille and a handful of symbols, each one cell wide and never part of a
// larger cluster. Width counts those directly and hands any other string to
// ansi.StringWidth, so its result always equals ansi.StringWidth's.
package textw

import (
	"math"
	"strings"
	"sync"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Runes below tableEnd that ansi.StringWidth measures as one cell on their
// own and that never join a neighbour into one cluster.
const tableEnd = 0x2c00

var (
	singleOnce sync.Once
	single     [tableEnd]bool
)

// safeRanges hold symbols and letters that stand alone: no combining marks,
// joiners, variation selectors or emoji-presentation characters (the table
// also drops any rune here whose own width is not one).
var safeRanges = [][2]rune{
	{0x00a1, 0x02ff}, // Latin-1 supplement and Latin extended, minus the soft hyphen (width 0)
	{0x2010, 0x2027}, // dashes, quotes, bullets, ellipsis
	{0x2030, 0x205e}, // per mille, primes, guillemets
	{0x2070, 0x209f}, // super- and subscripts
	{0x20a0, 0x20c0}, // currency
	{0x2100, 0x23ff}, // letterlike, number forms, arrows, math, technical
	{0x2460, 0x24ff}, // enclosed alphanumerics
	{0x2500, 0x25ff}, // box drawing, blocks, geometric shapes
	{0x2600, 0x26ff}, // miscellaneous symbols
	{0x2700, 0x27bf}, // dingbats
	{0x27c0, 0x2bff}, // math, arrows, braille, misc symbols
}

func buildSingle() {
	for _, r := range safeRanges {
		for c := r[0]; c <= r[1]; c++ {
			single[c] = ansi.StringWidth(string(c)) == 1
		}
	}
}

// StringWidth is the number of cells s occupies, the same as
// ansi.StringWidth (which counts a newline as zero cells).
func StringWidth(s string) int {
	if w, ok := fastWidth(s); ok {
		return w
	}
	return ansi.StringWidth(s)
}

// Width is the width of the widest line in s, the same as lipgloss.Width.
func Width(s string) int {
	if strings.IndexByte(s, '\n') < 0 {
		return StringWidth(s)
	}
	w := 0
	for line := range strings.SplitSeq(s, "\n") {
		w = max(w, StringWidth(line))
	}
	return w
}

func fastWidth(s string) (int, bool) {
	singleOnce.Do(buildSingle)
	w := 0
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c >= 0x20 && c < 0x7f:
			w++
			i++
		case c == 0x1b:
			n, ok := csiLen(s[i:])
			if !ok {
				return 0, false
			}
			i += n
		case c >= utf8.RuneSelf:
			r, size := utf8.DecodeRuneInString(s[i:])
			if r >= tableEnd || !single[r] {
				return 0, false
			}
			// The next rune must not extend this one. Every rune the table
			// admits is a cluster of its own, and any other rune (marks,
			// joiners, selectors) sends the whole string to the slow path.
			w++
			i += size
		default:
			return 0, false
		}
	}
	return w, true
}

// csiLen is the length of the CSI sequence (ESC [ params intermediates final)
// at the start of s. Other escapes are left to ansi.StringWidth.
func csiLen(s string) (int, bool) {
	if len(s) < 2 || s[1] != '[' {
		return 0, false
	}
	i := 2
	for i < len(s) && s[i] >= 0x30 && s[i] <= 0x3f {
		i++
	}
	for i < len(s) && s[i] >= 0x20 && s[i] <= 0x2f {
		i++
	}
	if i < len(s) && s[i] >= 0x40 && s[i] <= 0x7e {
		return i + 1, true
	}
	return 0, false
}

// lines splits a block the way lipgloss does and measures each line once.
func lines(s string) (ls []string, widths []int, widest int) {
	if strings.IndexByte(s, '\t') >= 0 {
		s = strings.ReplaceAll(s, "\t", "    ")
	}
	if strings.IndexByte(s, '\r') >= 0 {
		s = strings.ReplaceAll(s, "\r\n", "\n")
	}
	ls = strings.Split(s, "\n")
	widths = make([]int, len(ls))
	for i, l := range ls {
		widths[i] = StringWidth(l)
		widest = max(widest, widths[i])
	}
	return ls, widths, widest
}

func value(p lipgloss.Position) float64 { return math.Min(1, math.Max(0, float64(p))) }

func pad(b *strings.Builder, n int) {
	for ; n > 0; n-- {
		b.WriteByte(' ')
	}
}

// JoinVertical returns the same string as lipgloss.JoinVertical.
func JoinVertical(pos lipgloss.Position, strs ...string) string {
	if len(strs) == 0 {
		return ""
	}
	if len(strs) == 1 {
		return strs[0]
	}
	blocks := make([][]string, len(strs))
	widths := make([][]int, len(strs))
	maxWidth, size := 0, 0
	for i, s := range strs {
		var w int
		blocks[i], widths[i], w = lines(s)
		maxWidth = max(maxWidth, w)
		size += len(s) + len(blocks[i])
	}
	var b strings.Builder
	b.Grow(size + maxWidth)
	for i, block := range blocks {
		for j, line := range block {
			w := maxWidth - widths[i][j]
			// Center and fractional positions share the default branch.
			//exhaustive:ignore
			switch pos {
			case lipgloss.Left:
				b.WriteString(line)
				pad(&b, w)
			case lipgloss.Right:
				pad(&b, w)
				b.WriteString(line)
			default:
				if w < 1 {
					b.WriteString(line)
					break
				}
				split := int(math.Round(float64(w) * value(pos)))
				right := w - split
				pad(&b, w-right)
				b.WriteString(line)
				pad(&b, right)
			}
			if i != len(blocks)-1 || j != len(block)-1 {
				b.WriteByte('\n')
			}
		}
	}
	return b.String()
}

// JoinHorizontal returns the same string as lipgloss.JoinHorizontal.
func JoinHorizontal(pos lipgloss.Position, strs ...string) string {
	if len(strs) == 0 {
		return ""
	}
	if len(strs) == 1 {
		return strs[0]
	}
	blocks := make([][]string, len(strs))
	widths := make([][]int, len(strs))
	maxWidths := make([]int, len(strs))
	maxHeight, size := 0, 0
	for i, s := range strs {
		blocks[i], widths[i], maxWidths[i] = lines(s)
		maxHeight = max(maxHeight, len(blocks[i]))
		size += len(s)
	}
	for i := range blocks {
		n := maxHeight - len(blocks[i])
		if n <= 0 {
			continue
		}
		top := 0 // blank lines above the block
		// Center and fractional positions share the default branch.
		//exhaustive:ignore
		switch pos {
		case lipgloss.Top:
		case lipgloss.Bottom:
			top = n
		default:
			top = int(math.Round(float64(n) * value(pos)))
		}
		blocks[i] = append(append(make([]string, top, maxHeight), blocks[i]...), make([]string, n-top)...)
		widths[i] = append(append(make([]int, top, maxHeight), widths[i]...), make([]int, n-top)...)
	}
	var b strings.Builder
	b.Grow(size + maxHeight*len(strs)*8)
	for i := range maxHeight {
		for j, block := range blocks {
			b.WriteString(block[i])
			pad(&b, maxWidths[j]-widths[j][i])
		}
		if i < maxHeight-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}
