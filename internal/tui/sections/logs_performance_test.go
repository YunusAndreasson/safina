package sections

import (
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/YunusAndreasson/safina/internal/tui/theme"
	"github.com/YunusAndreasson/safina/internal/tui/tuictx"
)

// BenchmarkLogsStreaming includes the cost of successive stream arrivals,
// rather than just formatting a completed buffer once.
func BenchmarkLogsStreaming(b *testing.B) {
	ctx := &tuictx.Ctx{Theme: theme.Default()}
	chunk := strings.Repeat("2026-11-07T12:34:56.123Z INFO request completed status=200 duration=12ms\n", 10)
	for _, lines := range []int{100, 1000} {
		b.Run(strconv.Itoa(lines), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				logs := NewLogs(ctx)
				logs.SetPaneSize(100, 30)
				for range lines / 10 {
					logs.Append(chunk)
				}
			}
		})
	}
}

// A filtered stream often receives many chunks without a new matching record.
// Include an existing matching backlog so redundant viewport scans are visible.
func BenchmarkLogsFilteredStreaming(b *testing.B) {
	ctx := &tuictx.Ctx{Theme: theme.Default()}
	backlog := strings.Repeat("2026-11-07T12:34:56Z ERROR failed request\n", 1000)
	chunk := strings.Repeat("2026-11-07T12:34:57Z INFO healthy request\n", 10)
	for b.Loop() {
		logs := NewLogs(ctx)
		logs.SetPaneSize(100, 30)
		logs.filter.Open()
		logs.filter.Update(tea.PasteMsg{Content: "ERROR"})
		logs.Append(backlog)
		for range 100 {
			logs.Append(chunk)
		}
	}
}
