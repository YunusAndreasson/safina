package components

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yunus/safina/internal/tui/theme"
)

func TestPanelContentWidth(t *testing.T) {
	assert.Equal(t, 118, PanelContentWidth(120))
	assert.Equal(t, 1, PanelContentWidth(2))
	assert.Equal(t, 1, PanelContentWidth(0))
	assert.Equal(t, 1, PanelContentWidth(3))
}

func TestPanel_RendersTitle(t *testing.T) {
	tm := theme.Default()
	got := Panel(tm, "Logs", 80, 10, "line1\nline2", true)
	assert.Contains(t, got, "Logs")
	assert.Contains(t, got, "line1")
	assert.Contains(t, got, "line2")
}

func TestPanel_FocusedVsBlurred(t *testing.T) {
	tm := theme.Default()
	focused := Panel(tm, "Test", 80, 10, "content", true)
	blurred := Panel(tm, "Test", 80, 10, "content", false)
	assert.NotEqual(t, focused, blurred, "focused and blurred panels should differ")
}

func TestPanelWithAccent_RespectsAccent(t *testing.T) {
	tm := theme.Default()
	got := PanelWithAccent(tm, "Modal", 60, 8, "body", true, tm.P.Pink, tm.P.Green)
	assert.Contains(t, got, "Modal")
	assert.Contains(t, got, "body")
}

func TestPanel_MinWidth(t *testing.T) {
	tm := theme.Default()
	// Should not panic with tiny width
	got := Panel(tm, "X", 4, 4, "", false)
	assert.NotEmpty(t, got)
}

func TestPanel_ContentOverflow(t *testing.T) {
	tm := theme.Default()
	// Content wider than panel should be truncated, not overflow
	longLine := strings.Repeat("x", 200)
	got := Panel(tm, "Overflow", 40, 5, longLine, false)
	assert.NotEmpty(t, got)
	// The rendered content line should be truncated within the panel
	assert.NotContains(t, got, strings.Repeat("x", 200))
}

func TestPanelWithAccent_MinDimensions(t *testing.T) {
	tm := theme.Default()
	// Should clamp to minimum dimensions
	got := PanelWithAccent(tm, "Tiny", 0, 0, "", false, tm.P.Blue, tm.P.Cyan)
	assert.NotEmpty(t, got)
}
