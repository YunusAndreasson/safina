package sections

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
	"github.com/yunus/safina/internal/k8s"
	"github.com/yunus/safina/internal/tui/theme"
	"github.com/yunus/safina/internal/tui/tuictx"
)

func TestForwardColumnsBoundWideNamesAndLongKinds(t *testing.T) {
	for _, name := range []string{"api", strings.Repeat("東京", 20)} {
		row := forwardColumns("cluster/shop", "StatefulSetWithLongKind", name, "65535→65535", "UPTIME")
		require.Equal(t, 72, ansi.StringWidth(strings.SplitN(row, "UPTIME", 2)[0]))
	}
	f := Forwards{theme: theme.Default(), rows: []k8s.PortForwardSnapshot{{ContextName: "cluster", Namespace: "shop", TargetKind: "Pod", TargetName: "api", LocalPort: 8080, RemotePort: 80}}}
	rows := strings.Split(ansi.Strip(f.View(100)), "\n")
	require.Equal(t, ansi.StringWidth(strings.SplitN(rows[0], "context/namespace", 2)[0]), ansi.StringWidth(strings.SplitN(rows[1], "cluster/shop", 2)[0]))
}

func TestRecentEventContinuationsKeepTimeGutterClear(t *testing.T) {
	a := NewActivity()
	a.kind = "Pod"
	a.items = []k8s.EventInfo{{Age: "5s", Reason: "Started", Message: strings.Repeat("container ready ", 8)}}
	rows := strings.Split(ansi.Strip(a.renderBody(&tuictx.Ctx{Theme: theme.Default()}, 48, 20)), "\n")
	require.Greater(t, len(rows), 1)
	for _, row := range rows[1:] {
		require.True(t, strings.HasPrefix(row, strings.Repeat(" ", 8)), row)
		require.LessOrEqual(t, ansi.StringWidth(row), 48)
	}
}

func TestEventMessageColumnHandlesLargeRepeatCounts(t *testing.T) {
	s := Events{theme: theme.Default()}
	for _, count := range []int32{1, 10, 100000} {
		row := ansi.Strip(s.renderRowWithCountWidth(k8s.EventInfo{Age: "5s", Type: "Normal", Reason: "Started", Count: count, Message: "MESSAGE"}, 100, 8))
		require.Equal(t, 52, ansi.StringWidth(strings.SplitN(row, "MESSAGE", 2)[0]))
	}
}
