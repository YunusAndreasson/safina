package sections

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/YunusAndreasson/safina/internal/k8s"
	"github.com/YunusAndreasson/safina/internal/tui/components"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestLogTimesCompactButRawCopyAndPrecisionSearchRemain(t *testing.T) {
	c := newEventsCtx()
	l := NewLogs(c)
	l.SetPaneSize(60, 16)
	raw := "2026-11-07T11:31:31.277055537Z INFO cart.session id=730a\n2026-11-08T00:00:00Z INFO next day"
	l.Append(raw)
	text := ansi.Strip(l.rendered)
	require.Contains(t, text, "11:31:31 INFO cart.session")
	require.Contains(t, text, "07 Nov 2026 · UTC")
	require.Contains(t, text, "08 Nov 2026 · UTC")
	require.NotContains(t, text, "277055537")
	require.Equal(t, raw, l.YankPayload())
	l.Update(tea.KeyPressMsg{Code: 't', Text: "t"}, c)
	require.Contains(t, ansi.Strip(l.rendered), "277055537")
	l.Update(tea.KeyPressMsg{Code: 't', Text: "t"}, c)
	l.filter.Open()
	l.Update(tea.PasteMsg{Content: "277055537"}, c)
	require.Contains(t, ansi.Strip(l.rendered), "277055537")
}

func TestCompactTimeNormalizesTimezoneAndPreservesMessages(t *testing.T) {
	line, day := compactLogTimestamp("2026-11-08T00:30:00+02:00 message at 2026-11-07T22:00:00Z")
	require.Equal(t, "22:30:00 message at 2026-11-07T22:00:00Z", line)
	require.Equal(t, "07 Nov 2026", day)
	raw := "plain message without a timestamp"
	line, day = compactLogTimestamp(raw)
	require.Equal(t, raw, line)
	require.Empty(t, day)
}

func TestMouseWheelPausesLogFollow(t *testing.T) {
	c := newEventsCtx()
	l := NewLogs(c)
	l.SetPaneSize(50, 10)
	l.Append(strings.Repeat("line\n", 100))
	l.vp.GotoBottom()
	l.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp}, c)
	require.False(t, l.follow)
	offset := l.vp.YOffset()
	l.Append("another line\n")
	require.Equal(t, offset, l.vp.YOffset())
}

func TestEventMessageReadableAtNarrowWidth(t *testing.T) {
	s := NewEvents(newEventsCtx())
	e := k8s.EventInfo{Age: "3m", Type: "Warning", Reason: "FailedScheduling", Count: 7, Message: "0/1 nodes are available: insufficient memory. Preemption is not helpful for scheduling."}
	require.False(t, s.wrap)
	require.Len(t, strings.Split(s.renderRow(e, 42), "\n"), 1)
	s.wrap = true
	row := ansi.Strip(s.renderRow(e, 42))
	require.Contains(t, row, "FailedScheduling")
	require.Contains(t, row, "×7")
	for _, line := range strings.Split(row, "\n") {
		require.LessOrEqual(t, ansi.StringWidth(line), 42)
	}
	require.Contains(t, strings.Join(strings.Fields(row), " "), e.Message)
	s.wrap = false
	require.Len(t, strings.Split(s.renderRow(e, 42), "\n"), 1)
	s.wrap = true
	require.Contains(t, strings.Join(strings.Fields(ansi.Strip(s.renderRow(e, 42))), " "), e.Message)
}

func TestNarrowWorkloadListPrioritizesReadinessAndNames(t *testing.T) {
	tr := newTestTree(t)
	tr.children = &Children{Kind: k8s.KindApp, Apps: []k8s.AppInfo{{Kind: k8s.AppKindDeployment, Name: "recommendations", Namespace: "shop", Ready: "0/2", Health: k8s.HealthFailing, CPU: 100, Memory: 1000, Age: "2d"}}}
	text := ansi.Strip(tr.renderAppRows(newEventsCtx(), 36, 10))
	require.Contains(t, text, "recommendations")
	require.Contains(t, text, "READY")
	require.Contains(t, text, "0/2")
	require.NotContains(t, text, "CPU")
}

func TestSpacePausesAndResumesFollowing(t *testing.T) {
	c := newEventsCtx()
	l := NewLogs(c)
	l.Update(tea.KeyPressMsg{Code: ' ', Text: " "}, c)
	require.False(t, l.follow)
	l.Update(tea.KeyPressMsg{Code: ' ', Text: " "}, c)
	require.True(t, l.follow)
}

func TestScheduledWorkloadsKeepHealthWithoutReplicaCounts(t *testing.T) {
	tr := newTestTree(t)
	tr.children = &Children{Kind: k8s.KindApp, Apps: []k8s.AppInfo{{Kind: k8s.AppKindCronJob, Name: "nightly", Health: k8s.HealthDegraded}}}
	require.Contains(t, ansi.Strip(tr.renderAppRows(newEventsCtx(), 36, 10)), k8s.HealthDegraded.Glyph())
}

func TestEndedLogsRemainReadableButNotLabeledFollowing(t *testing.T) {
	c := newEventsCtx()
	l := NewLogs(c)
	l.SetPaneSize(80, 12)
	l.Append("existing output")
	l.EndStream(nil)
	require.Contains(t, ansi.Strip(l.Body(c)), "existing output")
	state := ansi.Strip(components.RenderIndicators(c.Theme, l.Indicators(c)))
	require.Contains(t, state, "stream ended")
	require.NotContains(t, state, "● follow")
}

func TestEventRowsAreCompactAndBounded(t *testing.T) {
	s := NewEvents(newEventsCtx())
	e := k8s.EventInfo{Age: "12m", Type: "Warning", Reason: "FailedScheduling", Count: 12, Message: "node unavailable\n\n  retry later 東京"}
	for _, width := range []int{24, 42, 80, 120} {
		row := ansi.Strip(s.renderRow(e, width))
		require.NotContains(t, row, "\n")
		require.LessOrEqual(t, ansi.StringWidth(row), width)
	}
	s.wrap = true
	row := ansi.Strip(s.renderRow(e, 42))
	require.NotContains(t, row, "\n\n")
	require.False(t, strings.HasSuffix(row, "\n"))
	require.Contains(t, strings.Join(strings.Fields(row), " "), "node unavailable retry later 東京")
}
