package main

import (
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/YunusAndreasson/safina/internal/k8s/bench"
)

const (
	bootstrapIters = 4000
	confidence     = 0.95
)

// Verdict compares safina (a) with k9s (b) on one metric where lower is
// better. Only an interval that excludes 1 produces a winner.
type Verdict struct {
	Op       string
	Title    string
	N        map[string]int
	P50, P95 map[string]float64
	Settle   map[string]float64
	Bytes    map[string]float64
	CI       bench.RatioCI
	Result   string
}

// A difference must also be large enough to matter. The driver resolves
// fractions of a millisecond, so two runs of the same program can differ
// "significantly" by 1%; such a result supports no claim.
const (
	minEffect  = 0.05 // the whole interval at least 5% away from 1
	minLatency = 1.0  // and medians at least 1 ms apart, for latencies
)

// verdictWithin judges a latency ratio a/b. minAbs, when positive, also
// requires the medians a50 and b50 to differ by that much.
func verdictWithin(ci bench.RatioCI, a50, b50, minAbs float64) string {
	return judge(ci, a50, b50, minAbs, "faster")
}

// judge names the side whose value is lower ("safina faster", "k9s lower").
func judge(ci bench.RatioCI, a50, b50, minAbs float64, word string) string {
	big := minAbs <= 0 || abs(a50-b50) >= minAbs
	switch {
	// A zero median leaves no ratio; callers rule out empty sides first.
	case ci.Ratio == 0 && a50 == b50:
		return "no difference"
	case ci.Ratio == 0 && b50 == 0:
		return "k9s " + word
	case ci.Ratio == 0 && a50 == 0:
		return "safina " + word
	case ci.Hi < 1-minEffect && big:
		return "safina " + word
	case ci.Lo > 1+minEffect && big:
		return "k9s " + word
	default:
		return "no difference"
	}
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

func Compare(res *Results) []Verdict {
	var out []Verdict
	for _, op := range opCatalog {
		v := Verdict{
			Op: op.ID, Title: op.Title, N: map[string]int{}, P50: map[string]float64{}, P95: map[string]float64{},
			Settle: map[string]float64{}, Bytes: map[string]float64{},
		}
		lat := map[string][]float64{}
		for _, tool := range []string{"safina", "k9s"} {
			var l, st, by []float64
			for _, s := range res.Samples {
				if s.Tool == tool && s.Op == op.ID {
					l, st, by = append(l, s.LatencyMs), append(st, s.SettleMs), append(by, float64(s.Bytes))
				}
			}
			lat[tool] = l
			v.N[tool] = len(l)
			v.P50[tool] = bench.Percentile(l, 50)
			v.P95[tool] = bench.Percentile(l, 95)
			v.Settle[tool] = bench.Percentile(st, 50)
			v.Bytes[tool] = bench.Percentile(by, 50)
		}
		if v.N["safina"] == 0 && v.N["k9s"] == 0 {
			continue
		}
		v.CI = bench.MedianRatioCI(lat["safina"], lat["k9s"], bootstrapIters, confidence, res.Env.Seed)
		v.Result = "no data"
		if v.N["safina"] > 0 && v.N["k9s"] > 0 {
			v.Result = verdictWithin(v.CI, v.P50["safina"], v.P50["k9s"], minLatency)
		}
		out = append(out, v)
	}
	return out
}

// sessionMetric compares a per-launch number (memory, idle CPU …).
type sessionMetric struct {
	Title string
	Unit  string
	Get   func(SessionStat) float64
}

var sessionMetrics = []sessionMetric{
	{"Resident memory after the run", "MB", func(s SessionStat) float64 { return float64(s.RSS) / (1 << 20) }},
	{"Peak resident memory (sum of process peaks)", "MB", func(s SessionStat) float64 { return float64(s.HWM) / (1 << 20) }},
	{"API requests to reach the pod list", "", func(s SessionStat) float64 { return float64(s.Startup.Requests) }},
	{"API bytes to reach the pod list", "KB", func(s SessionStat) float64 { return float64(s.Startup.Bytes) / 1024 }},
	{"Idle CPU", "% of a core", func(s SessionStat) float64 { return pct(s.IdleCPUMs, s.IdleSeconds) }},
	{"Idle API requests", "/min", func(s SessionStat) float64 { return perMin(float64(s.IdleTraffic.Requests), s.IdleSeconds) }},
	{"Idle terminal output", "KB/min", func(s SessionStat) float64 { return perMin(float64(s.IdleOutBytes)/1024, s.IdleSeconds) }},
}

func pct(cpuMs, seconds float64) float64 {
	if seconds == 0 {
		return -1
	}
	return cpuMs / (seconds * 1000) * 100
}

func perMin(n, seconds float64) float64 {
	if seconds == 0 {
		return -1
	}
	return n / seconds * 60
}

func sessionValues(res *Results, tool string, m sessionMetric) []float64 {
	var xs []float64
	for _, s := range res.Sessions {
		if s.Tool == tool && !s.Warmup && s.Error == "" {
			if v := m.Get(s); v >= 0 {
				xs = append(xs, v)
			}
		}
	}
	return xs
}

func writeResults(res *Results, dir string) error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	stamp := res.Env.Date.Format("20060102-150405")
	jsonPath := filepath.Join(dir, "versus-"+stamp+".json")
	mdPath := filepath.Join(dir, "versus-"+stamp+".md")
	b, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		return err
	}
	if err = os.WriteFile(jsonPath, b, 0o600); err != nil {
		return err
	}
	f, err := os.Create(mdPath) // #nosec G304 -- The report goes to the operator's --out directory.
	if err != nil {
		return err
	}
	WriteMarkdown(io.MultiWriter(f, os.Stdout), res)
	if err = f.Close(); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(os.Stderr, "\nwrote %s\n      %s\n", mdPath, jsonPath)
	return nil
}

func WriteMarkdown(w io.Writer, res *Results) {
	e := res.Env
	p := func(format string, a ...any) { _, _ = fmt.Fprintf(w, format, a...) }
	p("# safina %s vs k9s %s\n\n", e.Versions["safina"], e.Versions["k9s"])
	p("%s · %s · Linux %s · Kubernetes %s · context `%s`\n\n", e.Date.Format("2006-01-02 15:04"), e.CPU, e.Kernel, e.K8s, e.Context)
	fixture := make([]string, 0, len(e.Fixture))
	for _, k := range slices.Sorted(maps.Keys(e.Fixture)) {
		fixture = append(fixture, fmt.Sprintf("%d %s", e.Fixture[k], k))
	}
	p("Cluster: %s. Terminal %d×%d. Added API latency %.0f ms. %d measured rounds after %d warmup, seed %d.\n\n",
		strings.Join(fixture, ", "), e.Cols, e.Rows, e.RTTMs, e.Rounds, e.Warmup, e.Seed)

	p("## Interactions\n\n")
	p("Latency to the first correct frame, median and p95 in ms. Ratio is safina/k9s medians with a %.0f%% bootstrap interval. A side wins only when the whole interval is at least %.0f%% from 1 and the medians differ by at least %.0f ms.\n\n", confidence*100, minEffect*100, minLatency)
	p("| Operation | n | safina p50 | p95 | k9s p50 | p95 | ratio [95%% CI] | verdict | settle p50 s/k | bytes p50 s/k |\n")
	p("|---|---|---|---|---|---|---|---|---|---|\n")
	for _, v := range Compare(res) {
		num := func(tool string, x float64, format string) string {
			if v.N[tool] == 0 {
				return "—"
			}
			return fmt.Sprintf(format, x)
		}
		ratio := "—"
		if v.CI.Ratio > 0 {
			ratio = fmt.Sprintf("%.2f [%.2f–%.2f]", v.CI.Ratio, v.CI.Lo, v.CI.Hi)
		}
		p("| %s | %d/%d | %s | %s | %s | %s | %s | **%s** | %s/%s | %s/%s |\n",
			v.Title, v.N["safina"], v.N["k9s"],
			num("safina", v.P50["safina"], "%.1f"), num("safina", v.P95["safina"], "%.1f"),
			num("k9s", v.P50["k9s"], "%.1f"), num("k9s", v.P95["k9s"], "%.1f"),
			ratio, v.Result, num("safina", v.Settle["safina"], "%.0f"), num("k9s", v.Settle["k9s"], "%.0f"),
			bytesOr(v.N["safina"], v.Bytes["safina"]), bytesOr(v.N["k9s"], v.Bytes["k9s"]))
	}

	p("\n## Cost\n\nMedian per launch; lower is better. A side wins when the whole interval is at least %.0f%% from 1.\n\n", minEffect*100)
	p("| Metric | safina | k9s | ratio [95%% CI] | verdict |\n|---|---|---|---|---|\n")
	for _, m := range sessionMetrics {
		a, b := sessionValues(res, "safina", m), sessionValues(res, "k9s", m)
		if len(a) == 0 && len(b) == 0 {
			continue
		}
		ci := bench.MedianRatioCI(a, b, bootstrapIters, confidence, e.Seed)
		val := func(xs []float64) string {
			if len(xs) == 0 {
				return "—"
			}
			if m.Unit == "" {
				return fmt.Sprintf("%.1f", bench.Percentile(xs, 50))
			}
			return fmt.Sprintf("%.1f %s", bench.Percentile(xs, 50), m.Unit)
		}
		ratio := "—"
		if ci.Ratio > 0 {
			ratio = fmt.Sprintf("%.2f [%.2f–%.2f]", ci.Ratio, ci.Lo, ci.Hi)
		}
		verdict := "no data"
		if len(a) > 0 && len(b) > 0 {
			verdict = judge(ci, bench.Percentile(a, 50), bench.Percentile(b, 50), 0, "lower")
		}
		p("| %s | %s | %s | %s | **%s** |\n", m.Title, val(a), val(b), ratio, verdict)
	}

	writeEndpoints(p, res, "API requests by endpoint while idle", "per minute, all measured launches pooled",
		func(s SessionStat) (map[string]int64, float64) { return s.IdleEndpoints, s.IdleSeconds / 60 })
	writeEndpoints(p, res, "API requests by endpoint per launch", "mean per launch, including interactions and idle",
		func(s SessionStat) (map[string]int64, float64) { return s.Endpoints, 1 })

	busy := map[string]int{}
	for _, s := range res.Samples {
		if s.Busy {
			busy[s.Tool]++
		}
	}
	if busy["safina"]+busy["k9s"] > 0 {
		p("\nSteps still drawing %v after they were done (a spinner; settle capped there): safina %d, k9s %d.\n",
			settleCap, busy["safina"], busy["k9s"])
	}

	var failed []string
	for _, s := range res.Sessions {
		if s.Error != "" {
			failed = append(failed, fmt.Sprintf("- %s round %d: %s", s.Tool, s.Round, s.Error))
		}
	}
	if len(failed) > 0 {
		p("\n## Failed launches\n\nA failed launch contributes the samples it recorded before the failure.\n\n%s\n", strings.Join(failed, "\n"))
	}
	p("\n## Method\n\n")
	for _, n := range e.Notes {
		p("- %s\n", n)
	}
}

func bytesOr(n int, b float64) string {
	if n == 0 {
		return "—"
	}
	return humanBytes(b)
}

func humanBytes(n float64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1fM", n/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1fK", n/(1<<10))
	default:
		return fmt.Sprintf("%.0f", n)
	}
}

const endpointRows = 12

// writeEndpoints pools a per-launch endpoint count over the measured launches
// of each tool and divides by the pooled denominator (minutes idle, or
// launches), listing the endpoints with the highest rate on either side.
func writeEndpoints(p func(string, ...any), res *Results, title, unit string, get func(SessionStat) (map[string]int64, float64)) {
	rate := map[string]map[string]float64{"safina": {}, "k9s": {}}
	for tool, r := range rate {
		counts, denom := map[string]int64{}, 0.0
		for _, s := range res.Sessions {
			if s.Tool != tool || s.Warmup || s.Error != "" {
				continue
			}
			m, d := get(s)
			if d <= 0 {
				continue
			}
			denom += d
			for k, v := range m {
				counts[k] += v
			}
		}
		for k, v := range counts {
			r[k] = float64(v) / denom
		}
	}
	keys := slices.Collect(maps.Keys(rate["safina"]))
	for k := range rate["k9s"] {
		if _, ok := rate["safina"][k]; !ok {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return
	}
	top := func(k string) float64 { return max(rate["safina"][k], rate["k9s"][k]) }
	slices.SortFunc(keys, func(a, b string) int {
		if c := cmp.Compare(top(b), top(a)); c != 0 {
			return c
		}
		return strings.Compare(a, b)
	})
	p("\n## %s\n\n%s; the %d busiest endpoints.\n\n| Endpoint | safina | k9s |\n|---|---|---|\n", title, unit, min(endpointRows, len(keys)))
	for _, k := range keys[:min(endpointRows, len(keys))] {
		p("| `%s` | %.1f | %.1f |\n", k, rate["safina"][k], rate["k9s"][k])
	}
}
