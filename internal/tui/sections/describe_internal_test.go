package sections

import (
	"strings"
	"testing"

	"github.com/yunus/safina/internal/tui/theme"
)

func TestFindKeyColon(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"", -1},
		{"key: value", 3},
		{"key:", 3},       // colon at EOL
		{"key:value", -1}, // no space after colon → not a separator
		{"image: registry:5000/x", 5},
		{"url: http://example.com", 3},
		{`"key": value`, 5},    // quoted key
		{`'key': value`, 5},    // single-quoted key
		{`'a:b': value`, 5},    // colon inside single quotes
		{`"a:b": value`, 5},    // colon inside double quotes
		{`"#hash": value`, 7},  // hash inside double quotes
		{"# pure comment", -1}, // no colon at all
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := findKeyColon(tc.in); got != tc.want {
				t.Errorf("findKeyColon(%q) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

func TestFindKeyColon_BackslashEscape(t *testing.T) {
	// `"a\":b": value` — the inner \" is an escape; the real string ends at
	// the second unescaped ". The colon at index 7 is the separator.
	in := `"a\":b": value`
	got := findKeyColon(in)
	want := 7
	if got != want {
		t.Errorf("findKeyColon(%q) = %d, want %d (backslash-escaped quote not honored)", in, got, want)
	}
}

func TestIsYAMLNumber(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"", false},
		{"0", true},
		{"123", true},
		{"-1", true},
		{"+1", true},
		{"-", false},
		{"+", false},
		{"1.5", true},
		{"-1.5", true},
		{".5", true},
		{"1.", true},
		{"1.2.3", false},
		{"abc", false},
		{"0x10", false},
		{"1e10", false}, // scientific not handled — fine for kubectl output
		{"--1", false},
		{"-.", false},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := isYAMLNumber(tc.in); got != tc.want {
				t.Errorf("isYAMLNumber(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestSplitTrailingYAMLComment(t *testing.T) {
	cases := []struct {
		in          string
		wantBefore  string
		wantComment string
	}{
		{"", "", ""},
		{"key: value", "key: value", ""},
		{"key: value # note", "key: value", "# note"},
		{"# whole line", "", "# whole line"},
		{"key: 'val#hash'", "key: 'val#hash'", ""},
		{"key: \"val#hash\"", "key: \"val#hash\"", ""},
		{"key: val # comment with # hash", "key: val", "# comment with # hash"},
		{"foo#bar", "foo#bar", ""}, // # not preceded by space, not a comment
		{"foo #bar", "foo", "#bar"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			before, comment := splitTrailingYAMLComment(tc.in)
			if before != tc.wantBefore || comment != tc.wantComment {
				t.Errorf("splitTrailingYAMLComment(%q) = (%q, %q), want (%q, %q)",
					tc.in, before, comment, tc.wantBefore, tc.wantComment)
			}
		})
	}
}

// TestColorizeYAMLLine_NoPanic just exercises the renderer with a wide range
// of inputs that have shown up in real kubectl output. We don't assert on
// styled output (the ANSI bytes are theme-dependent), only that the function
// produces some output and doesn't crash.
func TestColorizeYAMLLine_NoPanic(t *testing.T) {
	th := theme.Default()
	inputs := []string{
		"",
		"   ",
		"---",
		"...",
		"-",
		"- item",
		"key: value",
		"key:",
		"key: |",
		"key: >-",
		"  - name: foo",
		`  - args: ["--flag", "value"]`,
		"  url: https://example.com:8443/path",
		"# whole-line comment",
		"  inline: value # trailing comment",
		"  num: 1.5",
		"  bool: true",
		"  nullish: null",
		"  empty: ''",
		"  weird: \"contains: colon\"",
		strings.Repeat("a", 5000), // very long line
	}
	for _, in := range inputs {
		t.Run(in, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("colorizeYAMLLine(%q) panicked: %v", in, r)
				}
			}()
			_ = colorizeYAMLLine(in, th)
		})
	}
}
