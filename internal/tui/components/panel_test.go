package components

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/YunusAndreasson/safina/internal/tui/theme"
)

func TestPanelContentWidth(t *testing.T) {
	assert.Equal(t, 118, PanelContentWidth(120))
	assert.Equal(t, 1, PanelContentWidth(2))
	assert.Equal(t, 1, PanelContentWidth(0))
	assert.Equal(t, 1, PanelContentWidth(3))
}

func TestPanelWithAccent_RendersTitle(t *testing.T) {
	tm := theme.Default()
	got := PanelWithAccent(tm, "Logs", 80, 10, "line1\nline2", true, tm.P.Blue, tm.P.Cyan)
	assert.Contains(t, got, "Logs")
	assert.Contains(t, got, "line1")
	assert.Contains(t, got, "line2")
}

func TestPanelWithAccent_FocusedVsBlurred(t *testing.T) {
	tm := theme.Default()
	focused := PanelWithAccent(tm, "Test", 80, 10, "content", true, tm.P.Blue, tm.P.Cyan)
	blurred := PanelWithAccent(tm, "Test", 80, 10, "content", false, tm.P.Blue, tm.P.Cyan)
	assert.NotEqual(t, focused, blurred, "focused and blurred panels should differ")
}

func TestPanelWithAccent_RespectsAccent(t *testing.T) {
	tm := theme.Default()
	got := PanelWithAccent(tm, "Modal", 60, 8, "body", true, tm.P.Pink, tm.P.Green)
	assert.Contains(t, got, "Modal")
	assert.Contains(t, got, "body")
}

func TestPanelWithAccent_MinWidth(t *testing.T) {
	tm := theme.Default()
	got := PanelWithAccent(tm, "X", 4, 4, "", false, tm.P.Blue, tm.P.Cyan)
	assert.NotEmpty(t, got)
}

func TestPanelWithAccent_ContentOverflow(t *testing.T) {
	tm := theme.Default()
	longLine := strings.Repeat("x", 200)
	got := PanelWithAccent(tm, "Overflow", 40, 5, longLine, false, tm.P.Blue, tm.P.Cyan)
	assert.NotEmpty(t, got)
	assert.NotContains(t, got, strings.Repeat("x", 200))
}

func TestPanelWithAccent_MinDimensions(t *testing.T) {
	tm := theme.Default()
	got := PanelWithAccent(tm, "Tiny", 0, 0, "", false, tm.P.Blue, tm.P.Cyan)
	assert.NotEmpty(t, got)
}
