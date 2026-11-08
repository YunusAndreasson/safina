package sections

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/YunusAndreasson/safina/internal/tui/theme"
	"github.com/YunusAndreasson/safina/internal/tui/tuictx"
	"github.com/stretchr/testify/require"
)

func retentionLogs() Logs {
	l := NewLogs(&tuictx.Ctx{Theme: theme.Default()})
	// Measure raw storage independently of formatting and viewport copies.
	l.theme = nil
	return l
}

func TestLogsRetentionBoundsLongRunningStreams(t *testing.T) {
	l := retentionLogs()
	for i := range maxLogLines * 3 {
		l.Append(fmt.Sprintf("record-%d\n", i))
	}
	require.LessOrEqual(t, len(l.lines), maxLogLines)
	require.Contains(t, l.YankPayload(), fmt.Sprintf("record-%d", maxLogLines*3-1))
	require.NotContains(t, l.YankPayload(), "record-0\n")
}

func TestLogsRetentionBoundsOversizedRecords(t *testing.T) {
	l := retentionLogs()
	for range 5 {
		l.Append(strings.Repeat("x", maxLogLineBytes))
	}
	require.LessOrEqual(t, len(l.lines[0]), maxLogLineBytes)
	for range 40 {
		l.Append("\n" + strings.Repeat("y", maxLogLineBytes))
	}
	rawBytes := 0
	for _, line := range l.lines {
		rawBytes += len(line) + 1
	}
	require.LessOrEqual(t, rawBytes, maxLogBytes+1)
}

func TestLogsResetReleasesStorage(t *testing.T) {
	l := retentionLogs()
	l.Append(strings.Repeat("old data\n", 1000))
	l.Reset("next", "shop")
	require.Nil(t, l.lines, "the previous stream's backing storage must be released")
	require.Nil(t, l.filtered)
	require.Empty(t, l.rendered)
}

func TestLogsRetentionReleasesDiscardedChunkBackingMemory(t *testing.T) {
	l := retentionLogs()
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	func() { l.Append(strings.Repeat("x", 24<<20) + "\nsmall retained record") }()
	runtime.GC()
	runtime.ReadMemStats(&after)
	runtime.KeepAlive(&l)
	growth := uint64(0)
	if after.HeapAlloc > before.HeapAlloc {
		growth = after.HeapAlloc - before.HeapAlloc
	}
	t.Logf("live heap growth after a 24 MiB record: %d bytes", growth)
	require.Less(t, growth, uint64(4<<20), "discarded chunk bytes must not be pinned by a small retained substring")
}

func TestLogsRetentionPreservesUTF8AndPausedFiltering(t *testing.T) {
	c := &tuictx.Ctx{Theme: theme.Default()}
	l := NewLogs(c)
	l.SetPaneSize(80, 12)
	l.filter.Restore("keep")
	l.recomputeFilter()
	l.Append(strings.Repeat("keep café\n", maxLogLines))
	l.follow = false
	l.Append("keep newest café\n")
	require.False(t, l.follow)
	require.Contains(t, l.YankPayload(), "keep newest café")
	for _, idx := range l.filtered {
		require.Less(t, idx, len(l.lines))
	}
	l.theme = nil
	l.filter.Clear()
	l.Append(strings.Repeat("界", maxLogLineBytes))
	require.True(t, utf8.ValidString(l.lines[len(l.lines)-1]))
	require.LessOrEqual(t, len(l.lines[len(l.lines)-1]), maxLogLineBytes)
	l.Reset("next", "shop")
	require.Zero(t, l.discardedLogBytes)
	require.Zero(t, l.logBytes)
}

func TestLogsRetentionKeepsPausedScrollAnchorWhenOldRowsExpire(t *testing.T) {
	l := NewLogs(&tuictx.Ctx{Theme: theme.Default()})
	l.SetPaneSize(80, 12)
	var initial strings.Builder
	for n := range maxLogLines {
		fmt.Fprintf(&initial, "INFO record-%d\n", n)
	}
	l.Append(initial.String())
	l.follow = false
	l.vp.SetYOffset(3000)
	before := strings.Split(l.vp.View(), "\n")[0]
	l.Append(strings.Repeat("INFO new record\n", 2000))
	require.False(t, l.follow)
	require.Equal(t, before, strings.Split(l.vp.View(), "\n")[0])
}
