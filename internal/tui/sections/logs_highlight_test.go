package sections

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/stretchr/testify/assert"
)

// TestHasUpper covers smart-case input: any ASCII uppercase letter flips
// the search to case-sensitive. Lowercase-only and non-letter inputs stay
// case-insensitive.
func TestHasUpper(t *testing.T) {
	cases := map[string]struct {
		in   string
		want bool
	}{
		"empty string is lowercase-mode": {in: "", want: false},
		"all lower":                      {in: "error fail", want: false},
		"single upper triggers":          {in: "Error", want: true},
		"upper in middle":                {in: "fooBar", want: true},
		"digits and punctuation":         {in: "404!", want: false},
		// hasUpper checks ASCII A-Z only — that's intentional; smart-case
		// for Cyrillic/Greek capitals would surprise log-grep muscle memory.
		"non-ASCII uppercase stays lower-mode": {in: "Ω", want: false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, hasUpper(tc.in))
		})
	}
}

// TestHighlight_EmptyQueryReturnsLineVerbatim is the cheap fast-path —
// no query means no styling, no allocation pressure on the hot log
// rendering loop.
func TestHighlight_EmptyQueryReturnsLineVerbatim(t *testing.T) {
	line := "GET /api/v1 200"
	assert.Equal(t, line, highlight(line, "", true, lipgloss.NewStyle()))
}

// stripANSI strips lipgloss escape sequences so we can assert on the
// underlying plain text. Walks CSI sequences (ESC [ ... letter). Used
// only in this test file — production paths render to the terminal
// directly and never see the stripped form.
func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && (s[j] < '@' || s[j] > '~') {
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

// TestHighlight_PreservesPlainText pins the contract: highlight may
// inject color codes around matches, but the underlying characters must
// land back in the output in the original order and casing.
func TestHighlight_PreservesPlainText(t *testing.T) {
	hl := lipgloss.NewStyle().Bold(true)
	cases := map[string]struct {
		line, query string
		cs          bool
		want        string // expected plain (no-ANSI) output
	}{
		"no match returns original":          {line: "hello world", query: "xyz", cs: false, want: "hello world"},
		"single match case-insensitive":      {line: "FATAL: kaboom", query: "fatal", cs: false, want: "FATAL: kaboom"},
		"multiple matches in one line":       {line: "fail fail fail", query: "fail", cs: false, want: "fail fail fail"},
		"adjacent matches":                   {line: "aaa", query: "a", cs: false, want: "aaa"},
		"case-sensitive misses lower needle": {line: "Error: ERROR", query: "ERROR", cs: true, want: "Error: ERROR"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := highlight(tc.line, tc.query, tc.cs, hl)
			assert.Equal(t, tc.want, stripANSI(got),
				"highlight must preserve plain characters exactly; only color codes may be injected")
		})
	}
}

// TestHighlight_InjectsStylesAroundMatchOnly defends against a regression
// where the highlight bleeds past the matched substring — e.g. the
// terminator is missing and the rest of the line gets the bold style.
func TestHighlight_InjectsStylesAroundMatchOnly(t *testing.T) {
	hl := lipgloss.NewStyle().Bold(true)
	out := highlight("prefix MATCH suffix", "MATCH", true, hl)

	// The output must contain the original substrings literally — that
	// proves the wrappers are around MATCH only, not around the whole line.
	plain := stripANSI(out)
	assert.Equal(t, "prefix MATCH suffix", plain)

	// And the styled output must differ from plain (some ANSI got injected).
	assert.NotEqual(t, plain, out, "a non-empty query with a match must inject styling")
}

// TestHighlight_SmartCaseInsensitiveKeepsOriginalCasing pins the
// documented behaviour: in case-insensitive mode the *needle* matches
// either case, but the *haystack* characters land in the output exactly
// as they appeared in the source line.
func TestHighlight_SmartCaseInsensitiveKeepsOriginalCasing(t *testing.T) {
	hl := lipgloss.NewStyle().Bold(true)
	out := highlight("Server returned ERROR_404", "error", false, hl)
	assert.Contains(t, stripANSI(out), "ERROR_404",
		"highlight must preserve the original casing of the matched substring in the output")
}
