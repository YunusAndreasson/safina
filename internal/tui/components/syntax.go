package components

import (
	"fmt"
	"image/color"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/charmbracelet/x/ansi"
	"github.com/yunus/safina/internal/tui/theme"
)

// HighlightYAML delegates parsing and terminal formatting to Chroma. The only
// application-specific policy is the palette. Its formatter resets each line,
// allowing viewports to filter and scroll without losing token colors.
// Call on content changes, not on every frame or filter keystroke.
func HighlightYAML(source string, t *theme.Theme) string {
	if source == "" || t == nil {
		return source
	}
	iterator, err := chroma.Coalesce(lexers.Get("yaml")).Tokenise(&chroma.TokeniseOptions{State: "root"}, source)
	if err != nil {
		return source
	}
	hex := func(c color.Color) string {
		r, g, b, _ := c.RGBA()
		return fmt.Sprintf("#%02x%02x%02x", r>>8, g>>8, b>>8)
	}
	style, err := chroma.NewStyle("safina", chroma.StyleEntries{
		chroma.NameTag:        "bold " + hex(t.P.Cyan),
		chroma.LiteralString:  hex(t.P.Green),
		chroma.LiteralNumber:  hex(t.P.Pink),
		chroma.LiteralDate:    hex(t.P.Pink),
		chroma.Keyword:        "bold " + hex(t.P.Yellow),
		chroma.Comment:        "italic " + hex(t.P.Dim),
		chroma.CommentPreproc: hex(t.P.Blue),
		chroma.Punctuation:    hex(t.P.Dim),
		chroma.NameNamespace:  hex(t.P.Dim),
	})
	if err != nil {
		return source
	}
	var rendered strings.Builder
	if err := formatters.TTY16m.Format(&rendered, style, iterator); err != nil {
		return source
	}
	// Highlighting must never change the manifest's text or line structure.
	result := rendered.String()
	if ansi.Strip(result) != source {
		return source
	}
	return result
}
