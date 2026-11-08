package components

import (
	"fmt"
	"image/color"
	"strings"
	"time"

	"github.com/YunusAndreasson/safina/internal/metrics"

	"charm.land/lipgloss/v2"

	"github.com/YunusAndreasson/safina/internal/tui/theme"
)

// MetricBuffer is a cached compact view of a shared observation series.
// Samples are owned by metrics.Store, not copied into per-metric buffers.
type MetricBuffer struct {
	Series     *metrics.Series
	Memory     bool
	cached     string
	cacheKey   string
	cacheTheme *theme.Theme
	cacheBG    color.Color
}

func Sparkline(t *theme.Theme, b *MetricBuffer, width int, bg lipgloss.Style) string {
	return metricSpark(t, b, width, bg, false, false)
}

func SparklineDim(t *theme.Theme, b *MetricBuffer, width int, bg lipgloss.Style) string {
	return metricSpark(t, b, width, bg, true, false)
}

func SparklineFull(t *theme.Theme, b *MetricBuffer, width int, bg lipgloss.Style) string {
	return metricSpark(t, b, width, bg, false, true)
}

func metricSpark(t *theme.Theme, b *MetricBuffer, width int, bg lipgloss.Style, dim, full bool) string {
	width = max(2, width)
	if b == nil || b.Series == nil || len(b.Series.Points) < 2 {
		return bg.Foreground(t.P.Dim).Render(strings.Repeat("·", width))
	}
	now := time.Now().Truncate(5 * time.Second)
	rev := b.Series.Revision
	dim = dim || b.Series.Latest.Status(now) != metrics.Available
	cacheKey := fmt.Sprintf("%d/%d/%d/%t/%t", width, rev, now.Unix(), dim, full)
	if b.cacheKey == cacheKey && b.cacheTheme == t && b.cacheBG == bg.GetBackground() {
		return b.cached
	}
	points := b.Series.Points
	if !full && len(points) > 2*width {
		points = points[len(points)-2*width:]
	}
	start, end := points[0].At, points[len(points)-1].At
	if full {
		end = maxTime(end, now)
	}
	ceiling := MetricCeiling(b.Series.Points, b.Memory)
	b.cached = RenderMetricChart(t, points, start, end, width, 1, b.Memory, false, dim, false, ceiling, bg)
	b.cacheKey, b.cacheTheme, b.cacheBG = cacheKey, t, bg.GetBackground()
	return b.cached
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}
