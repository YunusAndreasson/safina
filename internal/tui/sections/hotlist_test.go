package sections

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yunus/safina/internal/k8s"
)

func TestHot_ResetClearsItemsAndArmsLoading(t *testing.T) {
	h := NewHot()
	h.SetItems([]k8s.PodInfo{{Name: "stale"}}, nil)
	h.CursorDown()
	h.Reset()
	assert.Nil(t, h.items, "Reset must drop the previous list")
	assert.Equal(t, 0, h.cursor)
	assert.True(t, h.loading, "Reset arms loading=true so the modal shows the spinner until the fresh data arrives")
	assert.NoError(t, h.err)
}

func TestHot_SetItemsClearsLoading(t *testing.T) {
	h := NewHot()
	h.Reset()
	require.True(t, h.loading)
	h.SetItems([]k8s.PodInfo{{Name: "p"}}, nil)
	assert.False(t, h.loading, "SetItems must clear loading — otherwise the modal spins forever after data arrives")
}

func TestHot_SetItemsClampsCursorWhenListShrinks(t *testing.T) {
	// User had cursor=2 on the previous result set. New result is shorter
	// (refresh found fewer hot pods). Cursor must clamp or Selected()
	// would return a phantom row.
	h := NewHot()
	h.SetItems([]k8s.PodInfo{
		{Name: "a"}, {Name: "b"}, {Name: "c"},
	}, nil)
	h.CursorDown()
	h.CursorDown()
	require.Equal(t, 2, h.cursor)
	h.SetItems([]k8s.PodInfo{{Name: "a"}}, nil) // shrunk to 1
	assert.Equal(t, 0, h.cursor, "cursor must clamp when the new list is shorter")
}

func TestHot_SetItemsPreservesCursorWhenInRange(t *testing.T) {
	h := NewHot()
	h.SetItems([]k8s.PodInfo{{Name: "a"}, {Name: "b"}, {Name: "c"}}, nil)
	h.CursorDown()
	require.Equal(t, 1, h.cursor)
	// Refresh returns same length — cursor should not jump back to 0,
	// otherwise the operator loses their place every 5s auto-refresh.
	h.SetItems([]k8s.PodInfo{{Name: "x"}, {Name: "y"}, {Name: "z"}}, nil)
	assert.Equal(t, 1, h.cursor)
}

func TestHot_CursorUpDownClampToBounds(t *testing.T) {
	h := NewHot()
	h.SetItems([]k8s.PodInfo{{Name: "a"}, {Name: "b"}}, nil)
	// Up at top: no-op.
	h.CursorUp()
	assert.Equal(t, 0, h.cursor)
	h.CursorDown()
	h.CursorDown()
	h.CursorDown() // past the end
	assert.Equal(t, 1, h.cursor, "cursor must clamp at last index")
	h.CursorUp()
	h.CursorUp()
	h.CursorUp() // past the top
	assert.Equal(t, 0, h.cursor, "cursor must clamp at 0")
}

func TestHot_SelectedReturnsFalseWhenEmpty(t *testing.T) {
	h := NewHot()
	_, ok := h.Selected()
	assert.False(t, ok, "no items → no selection")
}

func TestHot_SelectedReturnsHighlightedRow(t *testing.T) {
	h := NewHot()
	h.SetItems([]k8s.PodInfo{
		{Name: "a"}, {Name: "b"}, {Name: "c"},
	}, nil)
	h.CursorDown()
	got, ok := h.Selected()
	require.True(t, ok)
	assert.Equal(t, "b", got.Name)
}

func TestHot_SetItemsWithErrorClearsLoading(t *testing.T) {
	// Loader failure must still flip loading=false, otherwise the user
	// sees the spinner instead of the error message.
	h := NewHot()
	h.Reset()
	require.True(t, h.loading)
	h.SetItems(nil, errors.New("metrics-server down"))
	assert.False(t, h.loading)
	require.Error(t, h.err)
	assert.Contains(t, h.err.Error(), "metrics-server")
}

func TestHot_PaneTitleIsStableForWorkspaceTab(t *testing.T) {
	// The workspace tab persists this string — it must be deterministic
	// across the section's lifetime so tabs don't rename themselves
	// between renders.
	h := NewHot()
	a := h.PaneTitle()
	h.SetItems([]k8s.PodInfo{{Name: "p"}}, nil)
	b := h.PaneTitle()
	h.Reset()
	c := h.PaneTitle()
	assert.Equal(t, a, b)
	assert.Equal(t, b, c)
	assert.NotEmpty(t, a)
}
