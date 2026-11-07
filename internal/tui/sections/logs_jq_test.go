package sections

import (
	"context"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
	"github.com/yunus/safina/internal/tui/theme"
	"github.com/yunus/safina/internal/tui/tuictx"
)

func TestLogJQPredicates(t *testing.T) {
	for _, tt := range []struct {
		query, line string
		match       bool
	}{
		{`jq: .level == "error"`, `2026-11-07T12:34:56.123Z {"level":"error"}`, true},
		{`jq: select(.duration > 10)`, `{"duration":12}`, true},
		{`jq: .id == 9007199254740993`, `{"id":9007199254740993}`, true},
		{`jq: .level == "error"`, `{"level":"info"}`, false},
		{`jq: .`, `not JSON`, false},
		{`jq: .`, `{} {}`, false},
		{`jq: .`, `{"partial":`, false},
		{`jq: empty`, `{}`, false},
		{`jq: false, null, true`, `{}`, true},
		{`jq: env | length > 0`, `{}`, false},
	} {
		t.Run(tt.query+tt.line, func(t *testing.T) {
			var j logJQ
			j.prepare(tt.query)
			require.NoError(t, j.err)
			got, err := j.matches(context.Background(), tt.line)
			require.NoError(t, err)
			require.Equal(t, tt.match, got)
		})
	}
}

func TestLogJQBoundsAndErrors(t *testing.T) {
	for _, q := range []string{`jq: repeat(false)`, `jq: error("bad")`, `jq: def f: f; f`} {
		var j logJQ
		j.prepare(q)
		require.NoError(t, j.err)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		_, err := j.matches(ctx, `{}`)
		cancel()
		require.Error(t, err)
	}
	var j logJQ
	j.prepare(`jq: .[`)
	require.Error(t, j.err)
}

func TestLogJQStreamingKeepsRawLinesAndResets(t *testing.T) {
	ctx := &tuictx.Ctx{Theme: theme.Default()}
	l := NewLogs(ctx)
	l.SetPaneSize(100, 30)
	l.filter.Open()
	l.filter.Update(tea.PasteMsg{Content: `jq: .level == "error"`})
	l.Append("{\"level\":\"info\"}\n2026-11-07T12:34:56Z {\"level\":")
	require.Empty(t, l.filtered)
	l.Append("\"error\"}\n")
	require.Equal(t, []int{1}, l.filtered)
	require.Equal(t, "2026-11-07T12:34:56Z {\"level\":\"error\"}", l.YankPayload())
	l.Reset("pod", "ns")
	require.Empty(t, l.jq.query)
	require.NoError(t, l.jq.err)
}

func TestLogJQErrorClearsPreviouslyMatchedOutput(t *testing.T) {
	ctx := &tuictx.Ctx{Theme: theme.Default()}
	l := NewLogs(ctx)
	l.SetPaneSize(100, 30)
	l.filter.Open()
	l.filter.Update(tea.PasteMsg{Content: `jq: if .fail then error("bad") else true end`})
	l.Append("{}\n")
	require.Equal(t, "{}", l.YankPayload())
	l.Append("{\"fail\":true}\n")
	require.Error(t, l.jq.err)
	require.Empty(t, l.YankPayload())
	require.Empty(t, l.rendered)
	l.filter.Clear()
	l.filter.Update(tea.PasteMsg{Content: `jq: true`})
	l.recomputeFilter()
	l.refreshContent()
	require.NoError(t, l.jq.err)
	require.Len(t, l.filtered, 2)
}
