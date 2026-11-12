package sections

import (
	"fmt"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/YunusAndreasson/safina/internal/history"
	"github.com/YunusAndreasson/safina/internal/k8s"
	"github.com/YunusAndreasson/safina/internal/metrics"
	"github.com/YunusAndreasson/safina/internal/tui/components"
	"github.com/YunusAndreasson/safina/internal/tui/theme"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func metricsFixture() (Metrics, time.Time) {
	now := time.Date(2026, 11, 7, 10, 30, 0, 0, time.UTC)
	var store metrics.Store
	k := metrics.Key{Cluster: "production", Kind: "Deployment", Namespace: "shop", Name: "api", UID: "api-uid"}
	var s *metrics.Series
	for i := range 180 {
		at := now.Add(time.Duration(i-179) * 5 * time.Second)
		r := metrics.Reading{CPU: 120, Memory: 90 << 20, State: metrics.Available, SourceAt: at, Window: 15 * time.Second, Covered: 3, Expected: 3, CPURequest: 300, CPULimit: 1000, MemRequest: 128 << 20, MemLimit: 256 << 20}
		if i > 60 && i < 85 {
			r.CPU = 900
			r.Memory = 220 << 20
		}
		if i > 110 && i < 120 {
			r.State = metrics.Partial
			r.Covered = 2
		}
		s = store.Add(k, r, at)
	}
	var m Metrics
	m.Select("production", NodeRef{Kind: k8s.KindApp, SubKind: "Deployment", Name: "api", Namespace: "shop", UID: "api-uid"}, s)
	m.ShowActivity = true
	m.ApplyHistory(history.Result{Records: []history.Record{
		{ID: "request", Operation: "rollout", Time: now.Add(-8 * time.Minute), Target: history.Target{UID: "api-uid"}, Action: "restart", Outcome: "requested"},
		{ID: "done", Operation: "rollout", Time: now.Add(-7 * time.Minute), Target: history.Target{UID: "api-uid"}, Action: "restart", Outcome: "accepted"},
		{ID: "unrelated", Time: now, Target: history.Target{UID: "recreated-uid"}, Action: "restart", Outcome: "completed"},
	}}, nil)
	return m, now
}

func TestMetricsLayoutsAndFreeze(t *testing.T) {
	m, now := metricsFixture()
	require.Len(t, m.Actions, 1)
	require.Len(t, m.Actions[0].Phases, 2)
	assert.Equal(t, "done", m.SelectedAction().ID)
	th := theme.Default()
	for _, size := range [][2]int{{80, 24}, {120, 40}, {160, 50}, {35, 10}, {40, 12}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			frame := m.Body(th, size[0], size[1], now)
			if size[0] == 80 {
				t.Log("\n" + ansi.Strip(frame))
			}
			assert.LessOrEqual(t, lipgloss.Height(frame), size[1])
			assert.Equal(t, size[0], lipgloss.Width(frame))
			assert.Contains(t, ansi.Strip(components.RenderIndicators(th, m.Indicators(now))), "locally observed")
			assert.NotContains(t, frame, "NaN")
			if size[1] >= 24 {
				assert.Contains(t, frame, "Memory")
				assert.Contains(t, frame, "CPU")
				assert.Contains(t, frame, "restart")
			}
			assert.Equal(t, frame, m.Body(th, size[0], size[1], now))
		})
	}
	reload, leave := m.Key("space", now)
	assert.True(t, reload)
	assert.False(t, leave)
	_, end := m.Range(now.Add(time.Minute))
	assert.Equal(t, now, end)
	assert.Contains(t, m.Summary(now.Add(time.Minute)), "900m")
	m.Key("]", now)
	assert.Equal(t, 2, m.Preset)
	m.Key("space", now)
	assert.True(t, m.Frozen.IsZero())
	m.Select("production", NodeRef{Kind: k8s.KindPod, UID: "replacement"}, nil)
	assert.Empty(t, m.Actions)
	assert.Nil(t, m.Series)
}

func TestMetricsPartialAndZero(t *testing.T) {
	m, now := metricsFixture()
	m.Series.Latest.State = metrics.Partial
	summary := m.Summary(now)
	assert.Contains(t, summary, "partial")
	assert.Contains(t, summary, "current —")
	m.Series.Latest.State = metrics.Available
	m.Series.Latest.CPU = 0
	assert.Contains(t, m.Summary(now), "current 0m")
	m.Series.Latest.SourceAt = now.Add(-2 * time.Minute)
	assert.Contains(t, m.Summary(now), "stale")
	assert.Contains(t, m.Body(theme.Default(), 80, 24, now), "source")
}

func BenchmarkMetricsSixHours(b *testing.B) {
	m, now := metricsFixture()
	var s metrics.Store
	for i := range metrics.PerResource {
		at := now.Add(time.Duration(i-metrics.PerResource+1) * 5 * time.Second)
		m.Series = s.Add(metrics.Key{Name: "api"}, metrics.Reading{CPU: int64(i % 1000), Memory: 128 << 20, SourceAt: at, State: metrics.Available}, at)
	}
	m.Preset = 3
	th := theme.Default()
	b.ResetTimer()
	for i := range b.N {
		m.Body(th, 120, 40, now.Add(time.Duration(i)*time.Second))
	}
}

func BenchmarkMetricsCachedFrame(b *testing.B) {
	m, now := metricsFixture()
	th := theme.Default()
	m.Body(th, 120, 40, now)
	b.ResetTimer()
	for range b.N {
		m.Body(th, 120, 40, now)
	}
}
