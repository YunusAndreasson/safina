package components

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yunus/safina/internal/tui/theme"
)

func TestModalFitSize_Normal(t *testing.T) {
	w, h := ModalFitSize(120, 40)
	assert.Equal(t, 116, w) // 120 - 4 = 116
	assert.Equal(t, 38, h)  // 40 - 2 = 38
}

func TestModalFitSize_ClampsMaxWidth(t *testing.T) {
	w, h := ModalFitSize(200, 40)
	assert.Equal(t, 160, w)
	assert.Equal(t, 38, h)
}

func TestModalFitSize_ClampsMinWidth(t *testing.T) {
	w, h := ModalFitSize(30, 40)
	assert.Equal(t, 40, w)
	assert.Equal(t, 38, h)
}

func TestModalFitSize_ClampsMinHeight(t *testing.T) {
	w, h := ModalFitSize(120, 5)
	assert.Equal(t, 116, w)
	assert.Equal(t, 12, h)
}

func TestModalFitSize_TinyTerminal(t *testing.T) {
	w, h := ModalFitSize(10, 5)
	assert.Equal(t, 40, w) // min width
	assert.Equal(t, 12, h) // min height
}

func TestModalFitSize_ZeroSizes(t *testing.T) {
	w, h := ModalFitSize(0, 0)
	assert.Equal(t, 40, w) // clamped to min
	assert.Equal(t, 12, h) // clamped to min
}

func TestModalFilterWidth_Normal(t *testing.T) {
	// PanelContentWidth(116) = 116 - 2 = 114, then 114 - 36 = 78
	w := ModalFilterWidth(116)
	assert.Equal(t, 78, w)
}

func TestModalFilterWidth_Minimum(t *testing.T) {
	w := ModalFilterWidth(40)
	assert.Equal(t, 8, w, "should clamp to minimum 8")
}

func TestModalFilterWidth_Large(t *testing.T) {
	w := ModalFilterWidth(160)
	assert.Greater(t, w, 100)
}

func TestRenderModalKeysRow_Empty(t *testing.T) {
	tm := theme.Default()
	got := RenderModalKeysRow(tm, nil)
	assert.Empty(t, got)
}

func TestRenderModalKeysRow_Single(t *testing.T) {
	tm := theme.Default()
	got := RenderModalKeysRow(tm, []KeyDesc{{Key: "esc", Desc: "back"}})
	assert.Contains(t, got, "esc")
	assert.Contains(t, got, "back")
}

func TestRenderModalKeysRow_Multiple(t *testing.T) {
	tm := theme.Default()
	pairs := []KeyDesc{
		{Key: "esc", Desc: "back"},
		{Key: "/", Desc: "filter"},
		{Key: "↑/↓", Desc: "scroll"},
	}
	got := RenderModalKeysRow(tm, pairs)
	assert.Contains(t, got, "esc")
	assert.Contains(t, got, "filter")
	assert.Contains(t, got, "scroll")
}

func TestDefaultStr(t *testing.T) {
	assert.Equal(t, "custom", defaultStr("custom", "fallback"))
	assert.Equal(t, "fallback", defaultStr("", "fallback"))
}

func TestRenderModalStatusBar_DefaultHint(t *testing.T) {
	tm := theme.Default()
	f := NewFilter(tm)
	got := RenderModalStatusBar(tm, 120, f, nil)
	assert.Contains(t, got, "/ filter")
}

func TestRenderModalStatusBar_WithFilterValue(t *testing.T) {
	tm := theme.Default()
	f := NewFilter(tm)
	f.input.SetValue("production")
	f.Close()
	got := RenderModalStatusBar(tm, 120, f, nil)
	assert.Contains(t, got, "production")
}
