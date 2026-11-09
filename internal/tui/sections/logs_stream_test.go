package sections

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"

	"github.com/YunusAndreasson/safina/internal/tui/theme"
	"github.com/YunusAndreasson/safina/internal/tui/tuictx"
)

func TestLogsPreserveStreamAcrossReadBoundaries(t *testing.T) {
	payload := "first record\nsecond café record\n\nlast record"
	for _, chunkSize := range []int{1, 7, len(payload)} {
		l := NewLogs(&tuictx.Ctx{Theme: theme.Default()})
		l.SetPaneSize(40, 12)
		for start := 0; start < len(payload); start += chunkSize {
			l.Append(payload[start:min(start+chunkSize, len(payload))])
		}
		require.Equal(t, strings.Split(payload, "\n"), l.Lines())
		require.Equal(t, payload, l.YankPayload())
		l.Reset("another", "default")
		l.Append("fresh\n")
		l.Append("stream\n")
		require.Equal(t, "fresh\nstream\n", l.YankPayload())
	}
}

// zap's console encoder separates fields with tabs. Each record must still
// take one row, or follow scrolls to a bottom the pane never shows.
func TestLogsFollowTabbedRecordsToTheNewest(t *testing.T) {
	ctx := &tuictx.Ctx{Theme: theme.Default()}
	l := NewLogs(ctx)
	l.SetPaneSize(60, 12)
	for i := range 30 {
		l.Append(fmt.Sprintf("2026-11-09T16:%02d:00.000Z\tinfo\tMetrics\t{\"resource\": {\"host.name\": \"node-a-workers-0f3c9b2e71d45a86\"}}\n", i))
	}
	rows := strings.Split(ansi.Strip(l.Body(ctx)), "\n")
	require.Len(t, rows, 12)
	require.Contains(t, strings.Join(rows, "\n"), "16:29:00")
}

func TestLogsStartWithoutWrapping(t *testing.T) {
	l := NewLogs(&tuictx.Ctx{Theme: theme.Default()})
	require.False(t, l.vp.SoftWrap)
	require.False(t, l.wrap)
}

// Compare every intermediate streamed frame to a fresh full render. This
// covers partial timestamps, split UTF-8, matches appearing,
// empty lines and headers across midnight, not just the final payload.
func TestLogsIncrementalRenderMatchesFullRender(t *testing.T) {
	payload := "2026-11-07T23:59:59.123Z INFO café\n\n2026-11-08T00:00:00.456Z ERROR failed\n2026-11-08T00:00:01Z info recovered\nplain café\n"
	ctx := &tuictx.Ctx{Theme: theme.Default()}
	for _, query := range []string{"", "info", "INFO", "café", ".123", "missing"} {
		t.Run(query, func(t *testing.T) {
			for _, full := range []bool{false, true} {
				for _, chunk := range []int{1, 7, 43, len(payload)} {
					logs := NewLogs(ctx)
					logs.SetPaneSize(40, 12)
					logs.fullTimestamps = full
					logs.filter.Open()
					logs.filter.Update(tea.PasteMsg{Content: query})
					require.Equal(t, query, logs.filter.Value())
					logs.recomputeFilter()
					logs.refreshContent()
					for start := 0; start < len(payload); start += chunk {
						end := min(start+chunk, len(payload))
						logs.Append(payload[start:end])
						fresh := NewLogs(ctx)
						fresh.SetPaneSize(40, 12)
						fresh.fullTimestamps = full
						fresh.filter.Open()
						fresh.filter.Update(tea.PasteMsg{Content: query})
						fresh.lines = strings.Split(payload[:end], "\n")
						fresh.recomputeFilter()
						fresh.refreshContent()
						require.Equal(t, fresh.filtered, logs.filtered)
						require.Equal(t, fresh.rendered, logs.rendered, "chunk=%d end=%d full=%v", chunk, end, full)
						require.Equal(t, fresh.compactTimeShown, logs.compactTimeShown)
						require.Equal(t, fresh.YankPayload(), logs.YankPayload())
					}
					// Invalidate cached formatting, then resume streaming.
					logs.fullTimestamps = !full
					logs.filter.Clear()
					logs.recomputeFilter()
					logs.refreshContent()
					logs.Append("2026-11-09T12:00:00Z INFO next day")
					got := logs.rendered
					logs.refreshContent()
					require.Equal(t, got, logs.rendered)
					logs.Reset("other", "default")
					logs.Append("2026-11-10T12:00:00Z INFO new stream")
					got = logs.rendered
					logs.refreshContent()
					require.Equal(t, got, logs.rendered)
				}
			}
		})
	}
}

func TestLogsIncrementalRenderPreservesScroll(t *testing.T) {
	for _, wrap := range []bool{false, true} {
		logs := NewLogs(&tuictx.Ctx{Theme: theme.Default()})
		logs.SetPaneSize(40, 12)
		logs.vp.SoftWrap = wrap
		logs.Append(strings.Repeat("INFO a long log record that wraps in a narrow viewport\n", 30))
		require.True(t, logs.vp.AtBottom())
		logs.Append("INFO another record\n")
		require.True(t, logs.vp.AtBottom())
		logs.follow = false
		logs.vp.SetYOffset(3)
		before := logs.vp.View()
		logs.Append("INFO output arriving while scrolled up\n")
		require.Equal(t, 3, logs.vp.YOffset())
		require.Equal(t, before, logs.vp.View())
		logs.follow = true
		logs.vp.GotoBottom()
		logs.Append("INFO resumed\n")
		require.True(t, logs.vp.AtBottom())
	}
}

func TestLogsNonmatchingChunksPreserveViewAndResume(t *testing.T) {
	for _, wrap := range []bool{false, true} {
		logs := NewLogs(&tuictx.Ctx{Theme: theme.Default()})
		logs.SetPaneSize(40, 12)
		logs.vp.SoftWrap = wrap
		logs.filter.Open()
		logs.filter.Update(tea.PasteMsg{Content: "ERROR"})
		logs.Append(strings.Repeat("2026-11-07T23:59:59Z ERROR a long record for wrapping and scrolling\n", 30))
		logs.follow = false
		logs.vp.SetYOffset(3)
		before := logs.vp.View()
		rendered := logs.rendered
		for _, chunk := range []string{"2026-11-08T00:00:00Z INFO healthy\n", "partial ", "record\n", "2026-11-09T12:00:00Z ERR"} {
			logs.Append(chunk)
			require.Equal(t, rendered, logs.rendered)
			require.Equal(t, before, logs.vp.View())
			require.Equal(t, 3, logs.vp.YOffset())
		}
		logs.Append("OR failed again\n")
		require.Contains(t, logs.rendered, "09 Nov 2026")
		require.NotContains(t, logs.rendered, "08 Nov 2026")
		got := logs.rendered
		logs.refreshContent()
		require.Equal(t, got, logs.rendered, "a partial line may become a match after skipped chunks")
		logs.filter.Clear()
		logs.recomputeFilter()
		logs.refreshContent()
		require.Contains(t, logs.rendered, "healthy", "nonmatching records must remain available")
	}
}
