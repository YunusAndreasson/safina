package components

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"

	"github.com/yunus/safina/internal/tui/theme"
)

func newTestFilter() Filter {
	return NewFilter(theme.Default())
}

func TestFilter_OpenClose(t *testing.T) {
	f := newTestFilter()
	assert.False(t, f.Active())

	f.Open()
	assert.True(t, f.Active())

	f.Close()
	assert.False(t, f.Active())
}

func TestFilter_SnapshotOnOpen(t *testing.T) {
	f := newTestFilter()
	// Set value before opening
	f.input.SetValue("production")
	f.Open()
	assert.Equal(t, "production", f.Value())

	// Change value while active
	f.input.SetValue("prod-filter")
	assert.Equal(t, "prod-filter", f.Value())

	// Cancel reverts to snapshot
	f.Cancel()
	assert.Equal(t, "production", f.Value())
	assert.False(t, f.Active())
}

func TestFilter_Clear(t *testing.T) {
	f := newTestFilter()
	f.input.SetValue("some-query")
	assert.Equal(t, "some-query", f.Value())

	f.Clear()
	assert.Empty(t, f.Value())
}

func TestFilter_HandleKeyEsc(t *testing.T) {
	f := newTestFilter()
	f.Open()
	f.input.SetValue("test")

	_, action := f.HandleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	assert.Equal(t, FilterCancel, action)
	assert.False(t, f.Active())
}

func TestFilter_HandleKeyEnter(t *testing.T) {
	f := newTestFilter()
	f.Open()

	_, action := f.HandleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.Equal(t, FilterCommit, action)
	assert.False(t, f.Active())
}

func TestFilter_HandleKeyEdit(t *testing.T) {
	f := newTestFilter()
	f.Open()

	_, action := f.HandleKey(tea.KeyPressMsg{Text: "a", Code: 'a'})
	assert.Equal(t, FilterEdit, action)
	assert.True(t, f.Active())
}

func TestFilter_ValueTrimsSpace(t *testing.T) {
	f := newTestFilter()
	f.input.SetValue("  hello world  ")
	assert.Equal(t, "hello world", f.Value())
}

func TestFilter_SetWidth(t *testing.T) {
	f := newTestFilter()
	f.SetWidth(50)
	// should not panic
}
