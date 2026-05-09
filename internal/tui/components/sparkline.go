package components

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/yunus/safina/internal/tui/theme"
)

// MetricBuffer is a fixed-capacity ring of the last N samples of a single
// metric. Callers append point-in-time values; the renderer reads the most
// recent up-to-cap samples in chronological order.
type MetricBuffer struct {
	samples []int64
	cap     int
}

// NewMetricBuffer constructs a buffer that keeps the most recent cap
// samples. Cap below 2 is clamped to 2 — a sparkline needs at least two
// points to hint at a trend.
func NewMetricBuffer(capN int) *MetricBuffer {
	if capN < 2 {
		capN = 2
	}
	return &MetricBuffer{cap: capN}
}

// Push appends a new sample and trims the head if full.
func (b *MetricBuffer) Push(v int64) {
	if b == nil {
		return
	}
	b.samples = append(b.samples, v)
	if len(b.samples) > b.cap {
		b.samples = b.samples[len(b.samples)-b.cap:]
	}
}

// Samples returns the buffered values, oldest first.
func (b *MetricBuffer) Samples() []int64 {
	if b == nil {
		return nil
	}
	out := make([]int64, len(b.samples))
	copy(out, b.samples)
	return out
}

// Len reports how many samples are buffered.
func (b *MetricBuffer) Len() int {
	if b == nil {
		return 0
	}
	return len(b.samples)
}

// sparkBlocks is the 1-cell vertical bar set used for the sparkline body.
// Eight levels lets the renderer encode a 0..7 range per column. The empty
// "no data" cell is intentionally a normal space (not a block) so a fresh
// resource reads as truly empty rather than as a flat zero line.
var sparkBlocks = []rune{'▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}

// Sparkline renders an inline sparkline showing the trend of the last N
// samples in width cells. The coloring is a smooth green→yellow→red ramp
// driven by the cell's height relative to the buffer's max — high values
// get warm colors so the eye spots saturated workloads at a glance.
//
// When the buffer has fewer than 2 samples we render width cells of dim
// "─" so the column still occupies its layout slot but signals "no trend
// yet" rather than a misleading flat bar.
func Sparkline(t *theme.Theme, buf *MetricBuffer, width int) string {
	if width < 2 {
		width = 2
	}
	if buf == nil || buf.Len() < 2 {
		dim := lipgloss.NewStyle().Foreground(t.P.Dim)
		return dim.Render(strings.Repeat("─", width))
	}

	samples := buf.Samples()
	// Take the last `width` samples (one per cell). Older samples are
	// dropped silently — the sparkline is a small moving window, not the
	// full history.
	if len(samples) > width {
		samples = samples[len(samples)-width:]
	}

	// Pad on the left so the most recent value lands in the right-most
	// cell — operators read time left-to-right, so "now" should sit at
	// the trailing edge.
	if len(samples) < width {
		pad := make([]int64, width-len(samples))
		samples = append(pad, samples...)
	}

	maxV := int64(0)
	for _, v := range samples {
		if v > maxV {
			maxV = v
		}
	}
	if maxV == 0 {
		dim := lipgloss.NewStyle().Foreground(t.P.Dim)
		return dim.Render(strings.Repeat("▁", width))
	}

	var b strings.Builder
	for i, v := range samples {
		if i < width-len(buf.samples) {
			// Pad cell — render as a space so the bar doesn't visually
			// extend backwards into the no-data range.
			b.WriteRune(' ')
			continue
		}
		// Map sample to one of 8 height buckets.
		level := int(v * int64(len(sparkBlocks)-1) / maxV)
		if level < 0 {
			level = 0
		}
		if level >= len(sparkBlocks) {
			level = len(sparkBlocks) - 1
		}
		ch := string(sparkBlocks[level])
		col := sparkColor(t, v, maxV)
		b.WriteString(lipgloss.NewStyle().Foreground(col).Render(ch))
	}
	return b.String()
}

// sparkColor maps a sample's height fraction to a color. Cool green at the
// bottom, warm red at the top — same convention as load gauges across
// every modern monitoring tool. Uses the theme's palette where it has
// direct hits, blends manually elsewhere via lipgloss.LightDarkFunc.
func sparkColor(t *theme.Theme, v, maxV int64) color.Color {
	if maxV == 0 {
		return t.P.Dim
	}
	frac := float64(v) / float64(maxV)
	switch {
	case frac < 0.34:
		return t.P.Green
	case frac < 0.67:
		return t.P.Yellow
	default:
		return t.P.Red
	}
}
