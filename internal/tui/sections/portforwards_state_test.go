package sections

import (
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/stretchr/testify/assert"

	"github.com/YunusAndreasson/safina/internal/k8s"
	"github.com/YunusAndreasson/safina/internal/tui/theme"
)

func TestTruncMid(t *testing.T) {
	cases := map[string]struct {
		in    string
		width int
		want  string
	}{
		"under width returns input verbatim":       {in: "short", width: 10, want: "short"},
		"exact width returns input verbatim":       {in: "exactly20charactersno", width: 21, want: "exactly20charactersno"},
		"narrow width below 3 returns hard prefix": {in: "abcdef", width: 2, want: "ab"},
		"odd width keeps balanced halves":          {in: "abcdefghij", width: 5, want: "ab…ij"},
		"even width biases left half":              {in: "abcdefghij", width: 6, want: "ab…hij"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := truncMid(tc.in, tc.width)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestHumanShort(t *testing.T) {
	cases := map[string]struct {
		d    time.Duration
		want string
	}{
		"zero seconds":  {d: 0, want: "0s"},
		"seconds":       {d: 7 * time.Second, want: "7s"},
		"just under 1m": {d: 59 * time.Second, want: "59s"},
		"exactly 1m":    {d: time.Minute, want: "1m"},
		"minutes":       {d: 17 * time.Minute, want: "17m"},
		"just under 1h": {d: 59 * time.Minute, want: "59m"},
		"exactly 1h":    {d: time.Hour, want: "1h"},
		"many hours":    {d: 73 * time.Hour, want: "73h"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, humanShort(tc.d))
		})
	}
}

func snaps(ids ...string) []k8s.PortForwardSnapshot {
	out := make([]k8s.PortForwardSnapshot, len(ids))
	for i, id := range ids {
		out[i] = k8s.PortForwardSnapshot{
			ID:          id,
			ContextName: "ctx",
			Namespace:   "ns",
			TargetKind:  "Pod",
			TargetName:  "p" + id,
			LocalPort:   8000 + i,
			RemotePort:  80,
			StartedAt:   time.Now(),
		}
	}
	return out
}

// TestForwards_Refresh_ResetsCursorOnShrink pins the invariant that
// shrinking the row set must not leave the cursor dangling past the
// new last index — CursorRowID would otherwise return "" or, worse,
// index off the end of the slice in the View path.
func TestForwards_Refresh_ResetsCursorOnShrink(t *testing.T) {
	f := Forwards{}
	f.Refresh(snaps("a", "b", "c"))
	f.MoveCursor(2)
	assert.Equal(t, "c", f.CursorRowID())

	f.Refresh(snaps("x"))
	assert.Equal(t, "x", f.CursorRowID(), "cursor must reset when refresh leaves it past the last row")
}

// TestForwards_Refresh_KeepsCursorWhenStillInBounds preserves the
// operator's row selection across a refresh that didn't shrink past
// it — important for the "kill, refresh, kill again" cadence.
func TestForwards_Refresh_KeepsCursorWhenStillInBounds(t *testing.T) {
	f := Forwards{}
	f.Refresh(snaps("a", "b", "c"))
	f.MoveCursor(1)
	assert.Equal(t, "b", f.CursorRowID())

	f.Refresh(snaps("a", "b", "c", "d"))
	assert.Equal(t, "b", f.CursorRowID(), "refresh that grows the list must not move the cursor")
}

func TestForwards_MoveCursor_WrapsBothDirections(t *testing.T) {
	f := Forwards{}
	f.Refresh(snaps("a", "b", "c"))

	f.MoveCursor(-1) // wrap to last
	assert.Equal(t, "c", f.CursorRowID())

	f.MoveCursor(1) // wrap back to first
	assert.Equal(t, "a", f.CursorRowID())

	f.MoveCursor(5) // 5 % 3 == 2
	assert.Equal(t, "c", f.CursorRowID())
}

func TestForwards_MoveCursor_NoOpOnEmpty(t *testing.T) {
	f := Forwards{}
	f.MoveCursor(1)
	f.MoveCursor(-1)
	assert.Empty(t, f.CursorRowID())
	assert.True(t, f.Empty())
}

func TestForwards_CursorRowID_EmptyOnUnpopulated(t *testing.T) {
	f := Forwards{}
	assert.Empty(t, f.CursorRowID(), "fresh Forwards has no rows; CursorRowID must report empty, not crash")
}

// TestForwards_View_EmptyHasHelpfulHint pins the no-forwards body so a
// future refactor doesn't accidentally render a blank panel and leave
// the user wondering whether the modal opened at all.
func TestForwards_View_EmptyHasHelpfulHint(t *testing.T) {
	f := Forwards{theme: theme.Default()}
	body := f.View(80)
	assert.Contains(t, body, "no active forwards")
	assert.Contains(t, ansi.Strip(body), "f start on a pod or service")
}
