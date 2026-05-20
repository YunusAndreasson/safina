package tui

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yunus/safina/internal/tui/theme"
)

// stripANSI returns s with all CSI sequences removed so tests can assert
// against the rendered text content without coupling to lipgloss's exact
// styling output. Diffs may be colored cyan/green/red, but we only care
// about which lines made it through and in what order.
func stripANSI(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	i := 0
	for i < len(s) {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
				j++
			}
			if j < len(s) {
				j++
			}
			i = j
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

func TestRenderUnifiedDiff_EqualReturnsEmpty(t *testing.T) {
	// No-op edits should never reach the modal, but the renderer is the
	// safety net — returning empty means callers can suppress the diff
	// block without an extra branch.
	got := renderUnifiedDiff(theme.Default(), []byte("same\n"), []byte("same\n"))
	assert.Empty(t, got, "identical bytes must produce empty output")
}

func TestRenderUnifiedDiff_StripsFileHeaders(t *testing.T) {
	// The modal title already shows the resource path; restating it via
	// "--- a" / "+++ b" lines is noise. The renderer drops both.
	out := stripANSI(renderUnifiedDiff(theme.Default(), []byte("a\n"), []byte("b\n")))
	require.NotEmpty(t, out)
	for _, line := range strings.Split(out, "\n") {
		assert.False(t, strings.HasPrefix(line, "--- "), "stray file header: %q", line)
		assert.False(t, strings.HasPrefix(line, "+++ "), "stray file header: %q", line)
	}
}

func TestRenderUnifiedDiff_IncludesPlusMinusLines(t *testing.T) {
	out := stripANSI(renderUnifiedDiff(theme.Default(),
		[]byte("first\nsecond\nthird\n"),
		[]byte("first\nCHANGED\nthird\n")))
	require.NotEmpty(t, out)
	assert.Contains(t, out, "-second")
	assert.Contains(t, out, "+CHANGED")
	// Hunk header must come through.
	assert.Contains(t, out, "@@")
}

func TestRenderUnifiedDiff_TruncatesPastMaxLines(t *testing.T) {
	// Build a giant diff: 50 changed lines. Renderer caps at diffMaxLines
	// and emits a "… N more line(s)" trailer. Without the cap a malicious
	// or accidental huge YAML edit would push the modal off-screen.
	var orig, edited strings.Builder
	for range 60 {
		orig.WriteString("a\n")
		edited.WriteString("b\n")
	}
	out := stripANSI(renderUnifiedDiff(theme.Default(),
		[]byte(orig.String()), []byte(edited.String())))
	require.NotEmpty(t, out)
	lines := strings.Split(out, "\n")
	// diffMaxLines rendered + one trailer line.
	assert.LessOrEqual(t, len(lines), diffMaxLines+1, "diff must be capped")
	last := lines[len(lines)-1]
	assert.Contains(t, last, "more line", "last line must be the truncation trailer")
	assert.Regexp(t, `\d+ more`, last, "trailer must include the missing count")
}

func TestRenderUnifiedDiff_NoTruncationTrailerWhenUnderCap(t *testing.T) {
	out := stripANSI(renderUnifiedDiff(theme.Default(),
		[]byte("x\n"), []byte("y\n")))
	assert.NotContains(t, out, "more line",
		"short diffs must not get the truncation trailer")
}

func TestRenderUnifiedDiff_PreservesLineOrdering(t *testing.T) {
	// Order is the operator's mental model. Verify lines come out in the
	// order they appear in the input, not sorted or reshuffled.
	orig := "alpha\nbeta\ngamma\n"
	edited := "alpha\nBETA\ngamma\n"
	out := stripANSI(renderUnifiedDiff(theme.Default(), []byte(orig), []byte(edited)))
	betaIdx := strings.Index(out, "-beta")
	upperIdx := strings.Index(out, "+BETA")
	require.NotEqual(t, -1, betaIdx)
	require.NotEqual(t, -1, upperIdx)
	assert.Less(t, betaIdx, upperIdx, "unified diff convention: - line precedes + line within a hunk")
}

func TestRenderUnifiedDiff_HandlesEmptyOrigInsertion(t *testing.T) {
	// Confirming a brand-new file: orig is empty, edited has content.
	out := stripANSI(renderUnifiedDiff(theme.Default(),
		[]byte(""), []byte("new line 1\nnew line 2\n")))
	require.NotEmpty(t, out)
	assert.Contains(t, out, "+new line 1")
	assert.Contains(t, out, "+new line 2")
}

func TestRenderUnifiedDiff_HandlesEmptyEditedDeletion(t *testing.T) {
	out := stripANSI(renderUnifiedDiff(theme.Default(),
		[]byte("doomed line\n"), []byte("")))
	require.NotEmpty(t, out)
	assert.Contains(t, out, "-doomed line")
}

func TestRenderUnifiedDiff_NonASCIIContentSurvives(t *testing.T) {
	// k8s names commonly include CJK / accented chars in annotations.
	// Renderer must not mangle multi-byte runes.
	out := stripANSI(renderUnifiedDiff(theme.Default(),
		[]byte("hej världen\n"), []byte("hello 世界\n")))
	require.NotEmpty(t, out)
	assert.Contains(t, out, "hej världen")
	assert.Contains(t, out, "hello 世界")
}

func TestRenderUnifiedDiff_ColorsApplied(t *testing.T) {
	// The unstripped output must carry ANSI escape codes — otherwise the
	// rendering pipeline silently lost styling. We don't pin specific
	// colors; just confirm SGR sequences are present.
	raw := renderUnifiedDiff(theme.Default(),
		[]byte("one\n"), []byte("two\n"))
	require.NotEmpty(t, raw)
	assert.Contains(t, raw, "\x1b[", "expected ANSI styling in rendered diff")
}
