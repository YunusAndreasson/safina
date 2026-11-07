package components

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
	"github.com/yunus/safina/internal/tui/theme"
)

func TestYAMLHighlightPreservesSource(t *testing.T) {
	for _, source := range []string{
		"", "---\napiVersion: v1\nkind: Pod\n", "ready: true\ncount: 2\nratio: 1.2e-3",
		"url: https://example.org/path#fragment\nimage: registry:5000/app:1",
		"message: \"a: b # text\" # comment", "text: 'it''s quoted'\n",
		"data:\n  script: |\n    # literal content\n    echo true\n  next: false\n",
		"defaults: &defaults\n  retry: 3\ncopy: *defaults\n",
		"labels: {app: web, version: 2}\nports: [80, 443]\n",
		"name: café-東京\n", "key:\tvalue\n", "key: value\r\n", "key: \"incomplete",
	} {
		require.Equal(t, source, ansi.Strip(HighlightYAML(source, theme.Default())))
	}
}

func TestYAMLBlockScalarUsesStringStyleRatherThanComment(t *testing.T) {
	result := HighlightYAML("script: |\n  # shell comment\n  echo true\nnext: true", theme.Default())
	lines := strings.Split(result, "\n")
	require.Contains(t, lines[1], "\x1b[38;2;158;206;106m") // palette green
	require.NotContains(t, lines[1], "\x1b[3m")             // not a YAML comment
	require.Contains(t, lines[3], "\x1b[1m")
}

func BenchmarkHighlightManifest(b *testing.B) {
	source := strings.Repeat("metadata:\n  name: web\n  labels:\n    app: web\n", 75)
	th := theme.Default()
	for b.Loop() {
		HighlightYAML(source, th)
	}
}
