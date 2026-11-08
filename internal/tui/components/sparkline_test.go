package components

import (
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yunus/safina/internal/metrics"
	"github.com/yunus/safina/internal/tui/theme"
)

func TestSparklineCeiling(t *testing.T) {
	points := make([]metrics.Point, 0, 4)
	for i, v := range []int64{500, 1500, 3000, 100} {
		points = append(points, metrics.Point{Reading: metrics.Reading{CPU: v, State: metrics.Available}})
		assert.Equal(t, []int64{1000, 2000, 4000, 4000}[i], MetricCeiling(points, false))
	}
	assert.Equal(t, int64(256<<20), MetricCeiling(nil, true))
}

func TestSparklineLibraryRendering(t *testing.T) {
	th, bg := theme.Default(), lipgloss.NewStyle()
	b := &MetricBuffer{Series: &metrics.Series{}}
	push := func(v int64) {
		p := metrics.Point{At: time.Now().Add(time.Duration(len(b.Series.Points)-202) * 5 * time.Second), Reading: metrics.Reading{CPU: v, State: metrics.Available}}
		b.Series.Points = append(b.Series.Points, p)
		b.Series.Latest = p
		b.Series.Revision++
	}
	assert.Equal(t, "····", ansi.Strip(Sparkline(th, b, 4, bg)))
	push(1)
	assert.Equal(t, "····", ansi.Strip(Sparkline(th, b, 4, bg)))
	for range 200 {
		push(1)
	}
	idle := ansi.Strip(SparklineFull(th, b, 4, bg))
	assert.Equal(t, 4, lipgloss.Width(idle))
	push(950)
	busy := ansi.Strip(SparklineFull(th, b, 4, bg))
	assert.NotEqual(t, idle, busy, "a spike must survive reduction")
	assert.Equal(t, 4, lipgloss.Width(busy))
	assert.Equal(t, busy, ansi.Strip(SparklineFull(th, b, 4, bg)), "cached render agrees")
}

func TestChartPointsPreserveExtremaAndGaps(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	points := make([]metrics.Point, 200)
	for i := range points {
		points[i] = metrics.Point{At: start.Add(time.Duration(i) * time.Second), Reading: metrics.Reading{CPU: 20, State: metrics.Available}}
	}
	points[80].CPU = 999
	points[81].CPU = 0
	points[100].State = metrics.Partial
	reduced := ChartPoints(points, start, start.Add(200*time.Second), 4, false)
	var peak, zero, gap bool
	for _, p := range reduced {
		peak = peak || p.CPU == 999
		zero = zero || p.CPU == 0
		gap = gap || p.State == metrics.Partial
	}
	assert.True(t, peak)
	assert.True(t, zero)
	assert.True(t, gap)
	for i := 1; i < len(reduced); i++ {
		assert.False(t, reduced[i].At.Before(reduced[i-1].At))
	}
	assert.Equal(t, "0m", MetricValue(0, false))
	assert.Equal(t, "0B", MetricValue(0, true))
}

func TestChartDoesNotBridgeMissingInterval(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	p := []metrics.Point{
		{At: start, Reading: metrics.Reading{CPU: 500, State: metrics.Available}},
		{At: start.Add(time.Minute), Reading: metrics.Reading{CPU: 500, State: metrics.Available}},
	}
	out := ansi.Strip(RenderMetricChart(theme.Default(), p, start, start.Add(time.Minute), 40, 6, false, false, false, false, 1000, lipgloss.NewStyle()))
	for _, line := range strings.Split(out, "\n") {
		assert.Equal(t, "                    ", string([]rune(line)[10:30]), "unobserved middle stays blank")
	}
}

func TestChartReductionDoesNotInventGaps(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	points := make([]metrics.Point, 4320)
	for i := range points {
		points[i] = metrics.Point{At: start.Add(time.Duration(i) * 5 * time.Second), Reading: metrics.Reading{CPU: 500, State: metrics.Available}}
	}
	out := ansi.Strip(RenderMetricChart(theme.Default(), points, start, points[len(points)-1].At, 40, 1, false, false, false, false, 1000, lipgloss.NewStyle()))
	require.NotContains(t, out, " ", "downsampling continuous observations must not create gaps")
}
