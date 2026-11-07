package sections

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yunus/safina/internal/tui/theme"
	"github.com/yunus/safina/internal/tui/tuictx"
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

func TestLogsStartWithoutWrapping(t *testing.T) {
	l := NewLogs(&tuictx.Ctx{Theme: theme.Default()})
	require.False(t, l.vp.SoftWrap)
	require.False(t, l.wrap)
}
