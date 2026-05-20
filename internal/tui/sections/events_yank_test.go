package sections

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yunus/safina/internal/k8s"
	"github.com/yunus/safina/internal/tui/keys"
	"github.com/yunus/safina/internal/tui/theme"
	"github.com/yunus/safina/internal/tui/tuictx"
)

func newEventsCtx() *tuictx.Ctx {
	return tuictx.New(theme.Default(), keys.Default(), nil, nil)
}

func sampleEvents() []k8s.EventInfo {
	return []k8s.EventInfo{
		{Age: "5s", Type: "Warning", Reason: "BackOff", Message: "Back-off pulling image"},
		{Age: "12s", Type: "Normal", Reason: "Scheduled", Message: "Successfully assigned pod"},
		{Age: "2m", Type: "Warning", Reason: "FailedMount", Message: "MountVolume.SetUp failed"},
	}
}

// TestEvents_YankPayload_EmptyWhenNoFiltered pins the documented zero
// value: an empty filter set yields "" (not "\n" or random whitespace),
// so clipboard contents reflect "nothing matched" precisely.
func TestEvents_YankPayload_EmptyWhenNoFiltered(t *testing.T) {
	s := NewEvents(newEventsCtx())
	assert.Empty(t, s.YankPayload(), "freshly constructed events modal must yank an empty payload")
}

// TestEvents_YankPayload_TabSeparatedNewestFirst pins the line format:
// type<TAB>reason<TAB>message, one event per line, top-down matching
// the visible order. Tabs (not commas) so a paste into a spreadsheet
// gets three columns automatically.
func TestEvents_YankPayload_TabSeparatedNewestFirst(t *testing.T) {
	s := NewEvents(newEventsCtx())
	s.SetItems(sampleEvents(), nil)
	payload := s.YankPayload()

	lines := strings.Split(payload, "\n")
	require.Len(t, lines, 3)
	for _, line := range lines {
		assert.Equal(t, 2, strings.Count(line, "\t"), "every yank line must have exactly two tab separators: %q", line)
	}
	assert.Equal(t, "Warning\tBackOff\tBack-off pulling image", lines[0])
}

// TestEvents_SetItems_PopulatesFilteredWithAllByDefault pins that with
// no active filter, every loaded item is visible — otherwise a fresh
// open of the modal would show nothing until the user typed.
func TestEvents_SetItems_PopulatesFilteredWithAllByDefault(t *testing.T) {
	s := NewEvents(newEventsCtx())
	s.SetItems(sampleEvents(), nil)
	assert.Len(t, s.filtered, 3)
	assert.Len(t, strings.Split(s.YankPayload(), "\n"), 3)
}

// TestEvents_Reset_ClearsLoadState pins the Reset contract: switching
// the modal to a new resource must wipe items, filter, and error so a
// stale loading frame from the previous target never bleeds through.
func TestEvents_Reset_ClearsLoadState(t *testing.T) {
	s := NewEvents(newEventsCtx())
	s.SetItems(sampleEvents(), nil)
	require.NotEmpty(t, s.YankPayload())

	s.Reset("Pod", "newpod", "ns")
	assert.Empty(t, s.YankPayload(), "Reset must clear previous items so the new resource starts dry")
	assert.True(t, s.loading, "Reset must arm the loading state for the upcoming fetch")
	assert.Equal(t, "events · pod · newpod", s.Title())
}
