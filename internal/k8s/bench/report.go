package bench

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"time"
)

// Baseline is the on-disk schema for bench/baseline.json. The quick-run
// regression gate reads this and compares the candidate p95 against it.
type Baseline struct {
	RecordedAt time.Time `json:"recorded_at"`
	Seed       int64     `json:"seed"`
	// Cells maps "variant|scenario" → recorded p95 in ms. Flat for stable
	// diff-on-Git review; nested structs reorder keys on every marshal.
	Cells map[string]float64 `json:"cells"`
}

func NewBaseline(res *Result) *Baseline {
	b := &Baseline{
		RecordedAt: res.StartedAt,
		Seed:       res.Seed,
		Cells:      make(map[string]float64, len(res.Cells)),
	}
	for _, c := range res.Cells {
		b.Cells[string(c.Variant)+"|"+string(c.Scenario)] = c.Summary.P95Ms
	}
	return b
}

func (b *Baseline) Lookup(v VariantID, s ScenarioID) (float64, bool) {
	x, ok := b.Cells[string(v)+"|"+string(s)]
	return x, ok
}

// WriteCSV writes one row per (variant, scenario, iteration) to w. The wide
// format is friendlier to downstream tools (pandas, R) than per-cell aggregates.
func WriteCSV(w io.Writer, res *Result) error {
	cw := csv.NewWriter(w)
	defer cw.Flush()
	if err := cw.Write([]string{"variant", "scenario", "iter", "duration_ms", "bytes", "api_calls", "content_type"}); err != nil {
		return err
	}
	for _, c := range res.Cells {
		ct := dominantContentType(c.ContentTypes)
		for i := range c.DurMs {
			row := []string{
				string(c.Variant),
				string(c.Scenario),
				strconv.Itoa(i),
				strconv.FormatFloat(c.DurMs[i], 'f', 3, 64),
				strconv.FormatFloat(c.Bytes[i], 'f', 0, 64),
				strconv.FormatFloat(c.Calls[i], 'f', 0, 64),
				ct,
			}
			if err := cw.Write(row); err != nil {
				return err
			}
		}
	}
	return nil
}

// WriteMarkdown emits a human-readable report: one table per scenario,
// variants ranked by p95 ascending, with delta-vs-baseline + Wilcoxon p
// against V0. Cells > tolerancePct slower than V0 are flagged.
func WriteMarkdown(w io.Writer, res *Result, tolerancePct float64) error {
	bw := &errWriter{w: w}
	bw.printf("# Kubernetes API Benchmark — %s\n\n", res.StartedAt.Format(time.RFC3339))
	bw.printf("Seed: `%d` · iterations per cell: `%d` · regression tolerance: `%.1f%% p95`\n\n",
		res.Seed, defaultN(res), tolerancePct)

	scenarios := orderedScenarios(res)
	byScenario := groupByScenario(res)
	baselineByScenario := baselineCellsByScenario(res)

	for _, sid := range scenarios {
		bw.printf("## %s\n\n", sid)
		bw.println("| variant | n | mean ms | p50 | p95 | p99 | bytes | calls | ct | Δ vs V0 (p95) | Wilcoxon p | verdict |")
		bw.println("|---|---:|---:|---:|---:|---:|---:|---:|---|---:|---:|---|")

		cells := byScenario[sid]
		sort.Slice(cells, func(i, j int) bool { return cells[i].Summary.P95Ms < cells[j].Summary.P95Ms })

		baseline := baselineByScenario[sid]
		for _, c := range cells {
			deltaStr := "—"
			pStr := "—"
			verdict := "•"
			if baseline != nil && c.Variant != V0Baseline {
				regressed, delta := RegressionVerdict(baseline.Summary, c.Summary, tolerancePct)
				deltaStr = fmt.Sprintf("%+.1f%%", delta)
				p := WilcoxonSignedRank(c.DurMs, baseline.DurMs)
				pStr = fmt.Sprintf("%.4f", p)
				switch {
				case regressed && p < 0.05:
					verdict = "🔴 regression"
				case regressed:
					// Tolerance exceeded but not statistically significant —
					// could be a real regression masked by noise, or just a
					// noisy run. Flag it without claiming significance.
					verdict = "⚠️ noisy"
				case delta < -10 && p < 0.01:
					verdict = "🟢 improvement"
				case p >= 0.05:
					verdict = "🟡 no signal"
				default:
					verdict = "•"
				}
			} else if c.Variant == V0Baseline {
				verdict = "baseline"
			}
			bw.printf(
				"| %s | %d | %.1f | %.1f | %.1f | %.1f | %s | %.0f | %s | %s | %s | %s |\n",
				c.Variant, c.Summary.N,
				c.Summary.MeanMs, c.Summary.MedianMs, c.Summary.P95Ms, c.Summary.P99Ms,
				humanBytes(c.Summary.BytesMean), c.Summary.CallsMean,
				dominantContentType(c.ContentTypes),
				deltaStr, pStr, verdict,
			)
		}
		bw.println("")
	}
	return bw.err
}

// errWriter is the standard "stop after first error" wrapper around io.Writer.
// Lets the markdown emitter avoid checking every Fprintf call.
type errWriter struct {
	w   io.Writer
	err error
}

func (e *errWriter) printf(format string, args ...any) {
	if e.err != nil {
		return
	}
	_, e.err = fmt.Fprintf(e.w, format, args...)
}

func (e *errWriter) println(s string) {
	if e.err != nil {
		return
	}
	_, e.err = fmt.Fprintln(e.w, s)
}

// WriteBaseline persists a Baseline as JSON. Used by --record-baseline.
func WriteBaseline(w io.Writer, b *Baseline) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(b)
}

// ReadBaseline parses a Baseline JSON. Empty/missing file → empty Baseline,
// not an error: the first run has nothing to compare against.
func ReadBaseline(r io.Reader) (*Baseline, error) {
	b := &Baseline{Cells: map[string]float64{}}
	dec := json.NewDecoder(r)
	if err := dec.Decode(b); err != nil {
		if err == io.EOF {
			return b, nil
		}
		return nil, err
	}
	if b.Cells == nil {
		b.Cells = map[string]float64{}
	}
	return b, nil
}

// --- helpers -----------------------------------------------------------------

func orderedScenarios(res *Result) []ScenarioID {
	seen := map[ScenarioID]bool{}
	out := []ScenarioID{}
	for _, c := range res.Cells {
		if !seen[c.Scenario] {
			seen[c.Scenario] = true
			out = append(out, c.Scenario)
		}
	}
	return out
}

func groupByScenario(res *Result) map[ScenarioID][]Cell {
	out := map[ScenarioID][]Cell{}
	for _, c := range res.Cells {
		out[c.Scenario] = append(out[c.Scenario], c)
	}
	return out
}

func baselineCellsByScenario(res *Result) map[ScenarioID]*Cell {
	out := map[ScenarioID]*Cell{}
	for i := range res.Cells {
		c := &res.Cells[i]
		if c.Variant == V0Baseline {
			out[c.Scenario] = c
		}
	}
	return out
}

func defaultN(res *Result) int {
	if len(res.Cells) == 0 {
		return 0
	}
	return res.Cells[0].Summary.N
}

// dominantContentType returns the most-observed Content-Type for a cell,
// suffixed with the fraction it represents when not unanimous. Lets the
// reader see at a glance "did protobuf actually engage" vs "did the server
// downgrade to JSON" without scrolling the CSV.
func dominantContentType(cts map[string]int) string {
	if len(cts) == 0 {
		return "—"
	}
	var top string
	var topN, total int
	for k, n := range cts {
		total += n
		if n > topN {
			top, topN = k, n
		}
	}
	if topN == total {
		return top
	}
	return fmt.Sprintf("%s (%d/%d)", top, topN, total)
}

func humanBytes(n float64) string {
	const k = 1024.0
	switch {
	case n < k:
		return fmt.Sprintf("%.0f B", n)
	case n < k*k:
		return fmt.Sprintf("%.1f KiB", n/k)
	case n < k*k*k:
		return fmt.Sprintf("%.1f MiB", n/(k*k))
	default:
		return fmt.Sprintf("%.1f GiB", n/(k*k*k))
	}
}
