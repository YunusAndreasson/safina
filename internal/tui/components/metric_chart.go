package components

import (
	"fmt"
	"math"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/NimbleMarkets/ntcharts/v2/canvas"
	"github.com/NimbleMarkets/ntcharts/v2/canvas/graph"
	"github.com/NimbleMarkets/ntcharts/v2/linechart"
	"github.com/YunusAndreasson/safina/internal/metrics"
	"github.com/YunusAndreasson/safina/internal/tui/theme"
)

func MetricValue(value int64, memory bool) string {
	if !memory {
		return fmt.Sprintf("%dm", value)
	}
	switch {
	case value >= 1<<30:
		return fmt.Sprintf("%.1fGi", float64(value)/(1<<30))
	case value >= 1<<20:
		return fmt.Sprintf("%dMi", value/(1<<20))
	case value >= 1<<10:
		return fmt.Sprintf("%dKi", value/(1<<10))
	default:
		return fmt.Sprintf("%dB", value)
	}
}

func MetricMagnitude(p metrics.Point, memory bool) int64 {
	if memory {
		return p.Memory
	}
	return p.CPU
}

func MetricBudget(p metrics.Point, memory bool) (int64, int64) {
	if memory {
		return p.MemRequest, p.MemLimit
	}
	return p.CPURequest, p.CPULimit
}

func MetricCeiling(points []metrics.Point, memory bool) int64 {
	var top int64 = 1000
	if memory {
		top = 256 << 20
	}
	for _, p := range points {
		if !p.Valid() {
			continue
		}
		req, lim := MetricBudget(p, memory)
		v := max(MetricMagnitude(p, memory), req, lim)
		for top < v && top < math.MaxInt64/2 {
			top *= 2
		}
	}
	return top
}

// ChartPoints keeps chronological extrema within each display bucket. Invalid
// points and large time gaps split buckets; spikes are never averaged away.
func ChartPoints(points []metrics.Point, start, end time.Time, width int, memory bool) []metrics.Point {
	if len(points) == 0 || !end.After(start) {
		return nil
	}
	out := make([]metrics.Point, 0, min(len(points), width*4))
	for lo := 0; lo < len(points); {
		if lo > 0 && points[lo].At.Sub(points[lo-1].At) > max(points[lo].GapAfter(), points[lo-1].GapAfter()) {
			// Preserve an actual source gap explicitly. Distances between reduced
			// extrema may be large even when the original observations are continuous.
			out = append(out, metrics.Point{At: points[lo].At, Reading: metrics.Reading{State: metrics.Unavailable}})
		}
		if !points[lo].Valid() || points[lo].Stale(points[lo].At) {
			out = append(out, points[lo])
			lo++
			continue
		}
		bucket := int(float64(points[lo].At.Sub(start)) / float64(end.Sub(start)) * float64(max(1, width*2)))
		hi := lo + 1
		low, high := lo, lo
		for hi < len(points) {
			p := points[hi]
			prev := points[hi-1]
			b := int(float64(p.At.Sub(start)) / float64(end.Sub(start)) * float64(max(1, width*2)))
			if b != bucket || !p.Valid() || p.Stale(p.At) || p.At.Sub(prev.At) > max(prev.GapAfter(), p.GapAfter()) {
				break
			}
			if MetricMagnitude(p, memory) < MetricMagnitude(points[low], memory) {
				low = hi
			}
			if MetricMagnitude(p, memory) > MetricMagnitude(points[high], memory) {
				high = hi
			}
			hi++
		}
		indexes := []int{lo, min(low, high), max(low, high), hi - 1}
		last := -1
		for _, i := range indexes {
			if i != last {
				out = append(out, points[i])
				last = i
			}
		}
		lo = hi
	}
	return out
}

// RenderMetricChart delegates axes, scaling and glyph rasterization to ntcharts.
// Safina owns observation semantics, gaps, budgets and its palette.
func RenderMetricChart(t *theme.Theme, points []metrics.Point, start, end time.Time, w, h int, memory, axes, dim, budgets bool, ceiling int64, bg lipgloss.Style) string {
	if w < 2 || h < 1 {
		return ""
	}
	if !end.After(start) {
		start = end.Add(-time.Second)
	}
	x0, x1 := float64(start.UnixMilli())/1000, float64(end.UnixMilli())/1000
	xstep, ystep := 0, 0
	if axes && h >= 4 && w >= 20 {
		xstep = max(1, w/3)
		ystep = max(1, h/2)
	}
	chart := linechart.New(w, h, x0, x1, 0, float64(max(1, ceiling)), linechart.WithXYSteps(xstep, ystep))
	chart.Canvas.SetStyle(bg)
	chart.AxisStyle = bg.Foreground(t.P.Dim)
	chart.LabelStyle = bg.Foreground(t.P.Dim)
	chart.XLabelFormatter = func(_ int, v float64) string { return time.Unix(int64(v), 0).UTC().Format("15:04") }
	chart.YLabelFormatter = func(_ int, v float64) string { return fmt.Sprintf("%7s", MetricValue(int64(v), memory)) }
	chart.UpdateGraphSizes()
	chart.DrawXYAxisAndLabel()
	ink := t.P.Cyan
	if memory {
		ink = t.P.Mauve
	}
	if dim {
		ink = t.P.Dim
	}
	style := bg.Foreground(ink)
	reduced := ChartPoints(points, start, end, max(1, chart.GraphWidth()), memory)
	// Rasterize the whole series once with the library grid. The linechart
	// convenience method allocates a new full grid per segment.
	grid := graph.NewBrailleGrid(chart.GraphWidth(), chart.GraphHeight(), x0, x1, 0, float64(max(1, ceiling)))
	var previous *metrics.Point
	for i := range reduced {
		p := &reduced[i]
		if p.At.Before(start) || p.At.After(end) || !p.Valid() || p.Stale(p.At) {
			previous = nil
			continue
		}
		pos := canvas.Float64Point{X: float64(p.At.UnixMilli()) / 1000, Y: float64(MetricMagnitude(*p, memory))}
		grid.Set(grid.GridPoint(pos))
		if previous != nil {
			old := canvas.Float64Point{X: float64(previous.At.UnixMilli()) / 1000, Y: float64(MetricMagnitude(*previous, memory))}
			for _, dot := range graph.GetLinePointsWithLimit(grid.GridPoint(old), grid.GridPoint(pos), chart.MaxInterpolationPoints) {
				grid.Set(dot)
			}
		}
		previous = p
	}
	offset := 0
	if ystep > 0 {
		offset = chart.Origin().X + 1
	}
	// References are steps over observed contiguous intervals, never projected
	// backwards from the latest configuration or across an observation gap.
	if budgets {
		refs := [2]*graph.BrailleGrid{
			graph.NewBrailleGrid(chart.GraphWidth(), chart.GraphHeight(), x0, x1, 0, float64(max(1, ceiling))),
			graph.NewBrailleGrid(chart.GraphWidth(), chart.GraphHeight(), x0, x1, 0, float64(max(1, ceiling))),
		}
		for i := 1; i < len(points); i++ {
			a, b := points[i-1], points[i]
			if !a.Valid() || !b.Valid() || a.Stale(a.At) || b.Stale(b.At) || b.At.Sub(a.At) > max(a.GapAfter(), b.GapAfter()) {
				continue
			}
			req, lim := MetricBudget(a, memory)
			for j, v := range []int64{req, lim} {
				if v <= 0 {
					continue
				}
				g := refs[j]
				from := g.GridPoint(canvas.Float64Point{X: float64(a.At.UnixMilli()) / 1000, Y: float64(v)})
				to := g.GridPoint(canvas.Float64Point{X: float64(b.At.UnixMilli()) / 1000, Y: float64(v)})
				for _, dot := range graph.GetLinePointsWithLimit(from, to, chart.MaxInterpolationPoints) {
					if dot.X%4 == 0 {
						g.Set(dot)
					}
				}
			}
		}
		for j, g := range refs {
			ink := t.P.Dim
			if j == 1 {
				ink = t.P.Yellow
			}
			graph.DrawBraillePatterns(&chart.Canvas, canvas.Point{X: offset}, g.BraillePatterns(), bg.Foreground(ink))
		}
	}
	// Usage takes precedence when a reference occupies the same terminal cell.
	graph.DrawBraillePatterns(&chart.Canvas, canvas.Point{X: offset}, grid.BraillePatterns(), style)
	return chart.View()
}

// MetricPlotColumns uses the library's layout for the action marker strip.
func MetricPlotColumns(w, h int, ceiling int64, memory bool) (int, int) {
	c := linechart.New(w, h, 0, 1, 0, float64(max(1, ceiling)), linechart.WithXYSteps(max(1, w/3), max(1, h/2)))
	c.YLabelFormatter = func(_ int, v float64) string { return fmt.Sprintf("%7s", MetricValue(int64(v), memory)) }
	c.UpdateGraphSizes()
	return c.Origin().X + 1, c.GraphWidth()
}
