package components

import (
	"image/color"
	"math"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/yunus/safina/internal/tui/theme"
)

// MetricBuffer is a fixed-capacity ring of the last N samples of a single
// metric. Callers append point-in-time values; the renderer reads the most
// recent up-to-cap samples in chronological order.
//
// The buffer also owns its y-axis ceiling (Ceiling()). The previous design
// scaled to the local-max of the buffer, which makes a pod stable at 1
// millicore render as max-height red bars (1/1 = 100%). The right model
// is a domain-aware floor (1 core for CPU, 256 MiB for memory) that
// ratchets upward in 2× steps when observed samples exceed it. The bars
// then encode "% of a sensible ceiling" — exactly what every operator
// already knows from Grafana, k9s, btop.
type MetricBuffer struct {
	samples    []int64
	cap        int
	minCeiling int64 // smallest ceiling we'll ever report; 0 means "auto"
	highWater  int64 // sticky: max value ever pushed (not just within window)
}

// NewMetricBuffer constructs an autoscaled buffer with no floor — the
// ceiling tracks high-water + 20%. Use this for unknown-magnitude series.
// For Kubernetes CPU/memory prefer NewMetricBufferWithFloor.
func NewMetricBuffer(capN int) *MetricBuffer {
	if capN < 2 {
		capN = 2
	}
	return &MetricBuffer{cap: capN}
}

// NewMetricBufferWithFloor sets a domain-aware minimum ceiling. Pods
// stable below the floor render as a faint floor line rather than max
// red bars — matches k9s's "grayC for currentCPU == 0" pattern.
func NewMetricBufferWithFloor(capN int, minCeiling int64) *MetricBuffer {
	b := NewMetricBuffer(capN)
	b.minCeiling = minCeiling
	return b
}

// Push appends a new sample and trims the head if full. Updates the
// sticky high-water mark used by Ceiling().
func (b *MetricBuffer) Push(v int64) {
	if b == nil {
		return
	}
	b.samples = append(b.samples, v)
	if len(b.samples) > b.cap {
		b.samples = b.samples[len(b.samples)-b.cap:]
	}
	if v > b.highWater {
		b.highWater = v
	}
}

// Ceiling returns the y-axis cap used to render this buffer's sparkline.
// When a floor is configured, the ceiling is at least the floor and
// ratchets upward in 2× steps as observed samples cross each multiple.
// When no floor is configured, ceiling = high-water + 20% headroom (or 1
// when nothing's been pushed yet — avoids div-by-zero in the renderer).
func (b *MetricBuffer) Ceiling() int64 {
	if b == nil {
		return 1
	}
	if b.minCeiling <= 0 {
		c := b.highWater + b.highWater/5
		if c < 1 {
			c = 1
		}
		return c
	}
	c := b.minCeiling
	for c < b.highWater {
		c *= 2
	}
	return c
}

// HighWater exposes the sticky max for the idle-detection branch in
// Sparkline. Lives on the buffer (not recomputed at render time) so it
// stays cheap even when the renderer fires every 5 seconds.
func (b *MetricBuffer) HighWater() int64 {
	if b == nil {
		return 0
	}
	return b.highWater
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

// Braille blocks (U+2800–U+28FF) encode 2 columns × 4 rows of dots per
// character. The renderer packs 2 samples per cell — the left dots show
// the older sample, the right dots the newer one — so a `width`-cell
// sparkline carries 2×width data points at a single cell's footprint.
// This is the same trick btop uses to look smooth at small sizes.
//
// Dot bit layout per Unicode UAX #11 / DIN 32982:
//
//	1 4
//	2 5
//	3 6
//	7 8
//
// "Bar from the bottom at height h (0..4)" patterns:
const brailleBase = rune(0x2800)

var (
	brailleLeft = [5]int{
		0,                         // h=0 : empty
		0x40,                      // h=1 : dot 7
		0x40 | 0x04,               // h=2 : 7+3
		0x40 | 0x04 | 0x02,        // h=3 : 7+3+2
		0x40 | 0x04 | 0x02 | 0x01, // h=4 : 7+3+2+1
	}
	brailleRight = [5]int{
		0,                         // h=0
		0x80,                      // h=1 : dot 8
		0x80 | 0x20,               // h=2 : 8+6
		0x80 | 0x20 | 0x10,        // h=3 : 8+6+5
		0x80 | 0x20 | 0x10 | 0x08, // h=4 : 8+6+5+4
	}
)

// idleCeilingFraction is the threshold below which the sparkline gives up
// on representing magnitude and just shows a faint flat floor. At ≤ 2%
// of the configured ceiling, the bar quanta (1/4 = 25% per dot row) can't
// render the value honestly — and an idle pod painted bright is the bug
// the user actually noticed.
const idleCeilingFraction = 0.02

// Sparkline renders an inline braille sparkline of the last `2*width`
// samples, scaled against the buffer's Ceiling(). Two samples per cell:
// left dots = older, right dots = newer, so the time axis still reads
// left-to-right and "now" sits at the rightmost cell.
//
// Color is driven by each cell's louder sample fraction — green under
// 50%, yellow 50–80%, red ≥ 80% — so a busy pod looks busy and an idle
// pod looks idle. Same scheme every operator already knows from Grafana
// / k9s / btop.
//
// bg is the row's background style (alternating stripe, selected blue,
// etc.) — every cell in the spark inherits it so the sparkline blends
// into the row's column block instead of leaving a mismatched-bg seam.
//
// Three special cases:
//   - < 2 samples: render width cells of "·" (data forthcoming).
//   - Idle (high-water < 2% of ceiling): a faint braille-floor line in
//     Dim. The numeric value renders next to the spark, so the operator
//     still reads the absolute number; the sparkline correctly shows "flat".
//   - All-zero buffer (a fresh resource with no usage yet): same flat-and-dim.
func Sparkline(t *theme.Theme, buf *MetricBuffer, width int, bg lipgloss.Style) string {
	if width < 2 {
		width = 2
	}
	if buf == nil || buf.Len() < 2 {
		return bg.Foreground(t.P.Dim).Render(strings.Repeat("·", width))
	}

	ceiling := buf.Ceiling()
	if ceiling <= 0 {
		ceiling = 1
	}

	// Idle case: if the highest sample we've ever seen is ≤ 2% of the
	// configured ceiling, the buffer is encoding "near-zero" — render a
	// flat floor of bottom dots. The previous design painted this case
	// full-height red because it normalised to local max.
	if float64(buf.HighWater())/float64(ceiling) < idleCeilingFraction {
		floor := string(brailleBase + rune(brailleLeft[1]+brailleRight[1]))
		return bg.Foreground(t.P.Dim).Render(strings.Repeat(floor, width))
	}

	const samplesPerCell = 2
	sampleCap := width * samplesPerCell
	samples := buf.Samples()
	if len(samples) > sampleCap {
		samples = samples[len(samples)-sampleCap:]
	}
	// Pad on the left so the most recent value lands in the right-most
	// cell — operators read time left-to-right, so "now" should sit at
	// the trailing edge.
	padN := sampleCap - len(samples)
	if padN < 0 {
		padN = 0
	}

	var b strings.Builder
	for cell := range width {
		lIdx := cell * samplesPerCell
		rIdx := cell*samplesPerCell + 1

		// Padding produces a level-0 (empty) half. We can't represent
		// "missing" inside a braille cell distinctly from "zero", but
		// during pad the magnitude *is* zero from the user's POV — they
		// just see an empty leading run that fills in over time.
		lFrac, lHas := sampleFrac(samples, lIdx, padN, ceiling)
		rFrac, rHas := sampleFrac(samples, rIdx, padN, ceiling)

		if !lHas && !rHas {
			b.WriteString(bg.Render(" "))
			continue
		}

		lLevel := levelFor(lFrac)
		rLevel := levelFor(rFrac)
		if !lHas {
			lLevel = 0
		}
		if !rHas {
			rLevel = 0
		}

		ch := brailleBase + rune(brailleLeft[lLevel]+brailleRight[rLevel])

		// Color by the louder side so the warning palette tracks the
		// peak — that's where the operator's eye should land.
		cellFrac := lFrac
		if rFrac > cellFrac {
			cellFrac = rFrac
		}
		b.WriteString(bg.Foreground(sparkColor(t, cellFrac)).Render(string(ch)))
	}
	return b.String()
}

// SparklineDim renders the same shape as Sparkline but with every cell
// painted in P.Dim — no green/yellow/red gradient. Used when the data on
// screen is stale (terminal blurred / refresh-age past the yellow tier)
// so the row still shows trend shape, but the urgency-color signal is
// suppressed until fresh samples arrive. The braille glyphs themselves
// are unchanged — only the color register shifts.
func SparklineDim(t *theme.Theme, buf *MetricBuffer, width int, bg lipgloss.Style) string {
	if width < 2 {
		width = 2
	}
	if buf == nil || buf.Len() < 2 {
		return bg.Foreground(t.P.Dim).Render(strings.Repeat("·", width))
	}
	ceiling := buf.Ceiling()
	if ceiling <= 0 {
		ceiling = 1
	}
	if float64(buf.HighWater())/float64(ceiling) < idleCeilingFraction {
		floor := string(brailleBase + rune(brailleLeft[1]+brailleRight[1]))
		return bg.Foreground(t.P.Dim).Render(strings.Repeat(floor, width))
	}
	const samplesPerCell = 2
	sampleCap := width * samplesPerCell
	samples := buf.Samples()
	if len(samples) > sampleCap {
		samples = samples[len(samples)-sampleCap:]
	}
	padN := sampleCap - len(samples)
	if padN < 0 {
		padN = 0
	}
	var b strings.Builder
	dim := bg.Foreground(t.P.Dim)
	for cell := range width {
		lIdx := cell * samplesPerCell
		rIdx := cell*samplesPerCell + 1
		lFrac, lHas := sampleFrac(samples, lIdx, padN, ceiling)
		rFrac, rHas := sampleFrac(samples, rIdx, padN, ceiling)
		if !lHas && !rHas {
			b.WriteString(bg.Render(" "))
			continue
		}
		lLevel := levelFor(lFrac)
		rLevel := levelFor(rFrac)
		if !lHas {
			lLevel = 0
		}
		if !rHas {
			rLevel = 0
		}
		ch := brailleBase + rune(brailleLeft[lLevel]+brailleRight[rLevel])
		b.WriteString(dim.Render(string(ch)))
	}
	return b.String()
}

// SparklineFull renders the *entire* buffered history into `width` cells by
// max-bucketing samples. Where Sparkline shows only the latest 2×width raw
// samples (recency window — what you want in a tree row), SparklineFull
// spreads however many samples are present across the full cell width
// (history window — what you want in the Inspector). Each cell-half holds
// the max of its bucket so transient spikes don't get averaged away.
//
// Falls back to the recency renderer when the buffer holds fewer samples
// than would fill width×2 halves — until enough history has accrued, the
// "wide view" and "recent view" coincide anyway.
func SparklineFull(t *theme.Theme, buf *MetricBuffer, width int, bg lipgloss.Style) string {
	if width < 2 {
		width = 2
	}
	if buf == nil || buf.Len() < 2 {
		return bg.Foreground(t.P.Dim).Render(strings.Repeat("·", width))
	}
	samples := buf.Samples()
	const halvesPerCell = 2
	totalHalves := width * halvesPerCell
	if len(samples) <= totalHalves {
		return Sparkline(t, buf, width, bg)
	}
	ceiling := buf.Ceiling()
	if ceiling <= 0 {
		ceiling = 1
	}
	if float64(buf.HighWater())/float64(ceiling) < idleCeilingFraction {
		floor := string(brailleBase + rune(brailleLeft[1]+brailleRight[1]))
		return bg.Foreground(t.P.Dim).Render(strings.Repeat(floor, width))
	}

	// Bucket samples into totalHalves slots, taking the max value per
	// bucket — preserves peak shape under heavy downsampling rather than
	// smearing spikes into the mean. Bucket boundaries are computed in
	// float so the residue distributes evenly across the row.
	buckets := make([]int64, totalHalves)
	step := float64(len(samples)) / float64(totalHalves)
	for h := range totalHalves {
		lo := int(float64(h) * step)
		hi := int(float64(h+1) * step)
		if hi <= lo {
			hi = lo + 1
		}
		if hi > len(samples) {
			hi = len(samples)
		}
		var peak int64
		for j := lo; j < hi; j++ {
			if samples[j] > peak {
				peak = samples[j]
			}
		}
		buckets[h] = peak
	}

	var b strings.Builder
	for cell := range width {
		lFrac := float64(buckets[cell*halvesPerCell]) / float64(ceiling)
		rFrac := float64(buckets[cell*halvesPerCell+1]) / float64(ceiling)
		if lFrac > 1 {
			lFrac = 1
		}
		if rFrac > 1 {
			rFrac = 1
		}
		lLevel := levelFor(lFrac)
		rLevel := levelFor(rFrac)
		ch := brailleBase + rune(brailleLeft[lLevel]+brailleRight[rLevel])
		cellFrac := lFrac
		if rFrac > cellFrac {
			cellFrac = rFrac
		}
		b.WriteString(bg.Foreground(sparkColor(t, cellFrac)).Render(string(ch)))
	}
	return b.String()
}

// sampleFrac returns the fraction (sample/ceiling) at slot `idx` in the
// padded sample stream, clamped to [0,1]. Returns ok=false when the slot
// is in the leading pad (no real sample yet).
func sampleFrac(samples []int64, idx, padN int, ceiling int64) (float64, bool) {
	if idx < padN {
		return 0, false
	}
	v := samples[idx-padN]
	frac := float64(v) / float64(ceiling)
	if frac < 0 {
		frac = 0
	}
	if frac > 1 {
		frac = 1
	}
	return frac, true
}

// levelFor maps a [0,1] fraction to a braille height level (0..4) using
// ceiling rounding. Any non-zero fraction lands on at least h=1 so a
// tiny-but-real sample still shows a bottom dot rather than vanishing
// into the empty cell, and a full-magnitude sample reaches h=4.
func levelFor(frac float64) int {
	if frac <= 0 {
		return 0
	}
	l := int(math.Ceil(frac * 4))
	if l > 4 {
		l = 4
	}
	return l
}

// sparkColor maps an absolute fraction-of-ceiling to a color. Thresholds
// match the convention every operator already knows (Grafana defaults,
// k9s alerting, common monitoring dashboards): under half is fine, the
// last fifth is alarming.
func sparkColor(t *theme.Theme, frac float64) color.Color {
	switch {
	case frac < 0.50:
		return t.P.Green
	case frac < 0.80:
		return t.P.Yellow
	default:
		return t.P.Red
	}
}
