package sections

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestYAMLHighlightingKeepsDocumentContextWhenFiltering(t *testing.T) {
	s := NewDescribe(newEventsCtx())
	source := "data:\n  script: |\n    # a script comment\n    echo true\n  enabled: true\n"
	s.SetYAML(source, nil)
	require.Equal(t, strings.TrimRight(source, "\n"), ansi.Strip(strings.Join(s.highlighted, "\n")))
	original := s.highlighted[2]
	s.filter.Open()
	s.filter.Update(tea.PasteMsg{Content: "script comment"})
	s.recomputeFilter()
	s.refreshContent()
	require.Equal(t, []int{2}, s.filtered)
	require.Equal(t, original, s.highlighted[2])
	require.Contains(t, s.YankPayload(), "# a script comment")
}
