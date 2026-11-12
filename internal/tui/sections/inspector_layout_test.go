package sections

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/YunusAndreasson/safina/internal/k8s"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestInspectorLayoutUsesSpareRowsForEventMessages(t *testing.T) {
	c := newEventsCtx()
	inspector := NewInspector()
	inspector.snap = &inspectorSnapshot{title: "Deployment · api", statusText: "Healthy", statusKind: "good"}
	activity := NewActivity()
	activity.kind = "Pod"
	for _, tail := range []string{"first ending", "second ending", "third ending"} {
		activity.items = append(activity.items, k8s.EventInfo{Age: "1m", Type: "Normal", Reason: "Started", Message: "Container started successfully and is now accepting requests: " + tail})
	}
	connected := NewConnected()
	frame := RenderDetails(c, 50, 18, &inspector, &activity, &connected)
	plain := ansi.Strip(frame)
	// The identity line moves to the topbar; Details keeps the facts.
	require.NotContains(t, plain, "Deployment · Healthy")
	full, short := inspector.Status(c)
	require.Contains(t, ansi.Strip(full), "Deployment · Healthy")
	require.Equal(t, "Deployment · Healthy", ansi.Strip(short))
	for _, tail := range []string{"first ending", "second ending", "third ending"} {
		require.Contains(t, strings.Join(strings.Fields(strings.ReplaceAll(plain, "│", " ")), " "), tail)
	}
	require.Equal(t, 18, lipgloss.Height(frame))
}

func TestConnectedShowsBothDirectionsWithoutOrphanHeadings(t *testing.T) {
	c := newEventsCtx()
	connected := NewConnected()
	connected.snap = &connectedSnapshot{
		uses:   []k8s.ConnectedRef{{Kind: "Secret", Name: "credentials"}, {Kind: "ConfigMap", Name: "settings"}},
		usedBy: []k8s.ConnectedRef{{Kind: "Service", Name: "frontend"}},
	}
	body := ansi.Strip(connected.renderBody(c, 60, 2))
	require.Len(t, strings.Split(body, "\n"), 2)
	require.Contains(t, body, "Uses (+1) Secret credentials")
	require.Contains(t, body, "Used by Service frontend")
	require.False(t, strings.HasPrefix(body, "\n"))
	inspector := NewInspector()
	inspector.snap = &inspectorSnapshot{title: "Pod · api", statusText: "Ready", statusKind: "good"}
	activity := NewActivity()
	for _, w := range []int{28, 48, 90, 120, 160} {
		for h := 6; h <= 40; h++ {
			frame := RenderDetails(c, w, h, &inspector, &activity, &connected)
			require.Equal(t, h, lipgloss.Height(frame), "height %d width %d", h, w)
			for _, row := range strings.Split(frame, "\n") {
				require.LessOrEqual(t, ansi.StringWidth(row), w)
			}
		}
	}
}

func TestInspectorSummaryWrapsLongHealthInsteadOfDroppingIt(t *testing.T) {
	c := newEventsCtx()
	inspector := NewInspector()
	inspector.snap = &inspectorSnapshot{title: "Deployment · api", statusText: "Progress deadline exceeded while waiting for updated replicas", statusKind: "bad"}
	body := ansi.Strip(inspector.renderBody(c, 28, 8))
	require.Contains(t, strings.Join(strings.Fields(body), " "), inspector.snap.statusText)
	for _, row := range strings.Split(body, "\n") {
		require.LessOrEqual(t, ansi.StringWidth(row), 28)
	}
}

func TestReadinessLivesInSummaryWithoutRepeatedReplicasRow(t *testing.T) {
	c := newEventsCtx()
	inspector := NewInspector()
	inspector.snap = &inspectorSnapshot{title: "Deployment · api", statusText: "Progressing", statusKind: "warn", sections: []inspectorSection{{rows: []inspectorRow{{label: "replicas", value: "3 updated · 2 ready · 3 desired"}, {label: "strategy", value: "RollingUpdate"}}}}}
	body := ansi.Strip(inspector.renderBody(c, 90, 12))
	require.Contains(t, strings.Split(body, "\n")[0], "Deployment · Progressing · 3 updated · 2 ready · 3 desired")
	require.Equal(t, 1, strings.Count(body, "2 ready"))
	require.NotContains(t, body, "replicas")
	require.Contains(t, body, "strategy")
}
