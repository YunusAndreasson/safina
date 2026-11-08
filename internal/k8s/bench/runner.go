package bench

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/YunusAndreasson/safina/internal/debug"
)

// Config controls one harness run. Construct it in the CLI and hand it to
// Run; Result captures the per-(variant,scenario) Summary and raw records.
type Config struct {
	// Variants and Scenarios are the cells to exercise. Each (V, S) is run
	// Iterations times after WarmupIters discarded warm-up calls.
	Variants  []VariantID
	Scenarios []ScenarioID

	Iterations  int
	WarmupIters int

	// Seed controls iteration ordering (Latin-square shuffle within each
	// variant block). Same seed → same execution order across runs.
	Seed int64

	// IterTimeout caps each iteration; the scenario context is cancelled at
	// this deadline. Use a value comfortably above expected p99.
	IterTimeout time.Duration

	// BaseConfigFactory builds a fresh *rest.Config per variant — the runner
	// wraps the transport with MeteringTransport before handing it to the
	// variant's Build. The returned config MUST be a fresh instance (Variants
	// mutate ContentType in place).
	BaseConfigFactory func() (*rest.Config, error)
}

// Cell is one (variant, scenario) result with raw per-iteration data plus
// the computed Summary. Raw data is retained for downstream stats / CSV.
type Cell struct {
	Variant  VariantID
	Scenario ScenarioID
	DurMs    []float64
	Bytes    []float64
	Calls    []float64
	// ContentTypes aggregates the negotiated response Content-Types observed
	// across all iterations of this cell, mapped to occurrence count. Used by
	// the report to surface "did protobuf engage" / "did all requests get the
	// same encoding back."
	ContentTypes map[string]int
	Summary      Summary
}

// Result is the full output of one Run.
type Result struct {
	StartedAt time.Time
	Seed      int64
	Cells     []Cell
}

// Run executes the configured grid. Errors fail the whole run — partial
// results aren't useful for the comparative analysis the report does.
func Run(ctx context.Context, cfg Config) (*Result, error) {
	if cfg.Iterations <= 0 {
		cfg.Iterations = 30
	}
	if cfg.WarmupIters < 0 {
		cfg.WarmupIters = 0
	}
	if cfg.IterTimeout <= 0 {
		cfg.IterTimeout = 30 * time.Second
	}
	if cfg.BaseConfigFactory == nil {
		return nil, errors.New("BaseConfigFactory is required")
	}
	if len(cfg.Variants) == 0 {
		return nil, errors.New("at least one variant required")
	}
	if len(cfg.Scenarios) == 0 {
		return nil, errors.New("at least one scenario required")
	}

	res := &Result{StartedAt: time.Now(), Seed: cfg.Seed}
	// PCG with the user's seed — deterministic, suitable for ordering. We
	// don't need cryptographic randomness here; the seed is for reproducibility.
	rng := rand.New(rand.NewPCG(uint64(cfg.Seed), uint64(cfg.Seed)^0x9E3779B97F4A7C15)) //nolint:gosec // not crypto

	// Randomize variant ORDER so the apiserver-cache warmup penalty doesn't
	// systematically fall on the first variant in the user-supplied list. The
	// iteration-level Latin square inside each variant block defeats per-
	// scenario ordering bias, but the variants themselves still need a coin
	// flip to be a fair test. Different --seed values produce different
	// variant orderings; the same seed reproduces.
	variantOrder := make([]VariantID, len(cfg.Variants))
	copy(variantOrder, cfg.Variants)
	rng.Shuffle(len(variantOrder), func(i, j int) {
		variantOrder[i], variantOrder[j] = variantOrder[j], variantOrder[i]
	})

	for _, vid := range variantOrder {
		variant, err := Lookup(vid)
		if err != nil {
			return nil, err
		}

		baseCfg, err := cfg.BaseConfigFactory()
		if err != nil {
			return nil, fmt.Errorf("build base config for %s: %w", vid, err)
		}

		meter := debug.NewMeter()
		baseCfg.WrapTransport = chainWrap(baseCfg.WrapTransport, debug.WrapWithMeter(meter))

		cs, opts, cleanup, err := variant.Build(ctx, baseCfg)
		if err != nil {
			return nil, fmt.Errorf("build variant %s: %w", vid, err)
		}

		cells := make(map[ScenarioID]*Cell, len(cfg.Scenarios))
		for _, sid := range cfg.Scenarios {
			cells[sid] = &Cell{Variant: vid, Scenario: sid, ContentTypes: map[string]int{}}
		}

		// Warmup: run the first scenario WarmupIters times, results discarded.
		// Process-level warmup (TCP, JIT, informer initial sync) is what we
		// care about; per-scenario warmup would just inflate run time.
		if cfg.WarmupIters > 0 {
			s0, err := LookupScenario(cfg.Scenarios[0])
			if err != nil {
				cleanup()
				return nil, err
			}
			for i := range cfg.WarmupIters {
				if err := runOnce(ctx, cfg.IterTimeout, cs, opts, s0); err != nil {
					cleanup()
					return nil, fmt.Errorf("warmup %s/%s iter %d: %w", vid, s0.ID, i, err)
				}
			}
		}

		// Latin-square-ish schedule: every scenario runs Iterations times,
		// but never N in a row, so transient API-server cache state can't
		// bias one scenario over another.
		schedule := make([]ScenarioID, 0, cfg.Iterations*len(cfg.Scenarios))
		for range cfg.Iterations {
			schedule = append(schedule, cfg.Scenarios...)
		}
		rng.Shuffle(len(schedule), func(i, j int) {
			schedule[i], schedule[j] = schedule[j], schedule[i]
		})

		for idx, sid := range schedule {
			scen, _ := LookupScenario(sid)
			meter.Start()
			start := time.Now()
			err := runOnce(ctx, cfg.IterTimeout, cs, opts, scen)
			elapsed := time.Since(start)
			meter.Stop()
			if err != nil {
				cleanup()
				return nil, fmt.Errorf("%s/%s iter %d: %w", vid, sid, idx, err)
			}
			recs := meter.Snapshot()
			cell := cells[sid]
			// Wall-clock duration is what the user perceives — use it as the
			// canonical latency, with the per-call byte sum as the secondary
			// signal.
			cell.DurMs = append(cell.DurMs, float64(elapsed.Milliseconds()))
			cell.Bytes = append(cell.Bytes, scenarioBytes(recs))
			cell.Calls = append(cell.Calls, float64(len(recs)))
			for _, r := range recs {
				ct := simplifyContentType(r.ContentType)
				cell.ContentTypes[ct]++
			}
		}

		for _, sid := range cfg.Scenarios {
			c := cells[sid]
			c.Summary = Summarize(c.DurMs, c.Bytes, c.Calls)
			res.Cells = append(res.Cells, *c)
		}

		cleanup()
	}

	return res, nil
}

func runOnce(ctx context.Context, timeout time.Duration, cs kubernetes.Interface, opts *Options, scen Scenario) error {
	iterCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return scen.Run(iterCtx, cs, opts)
}

func scenarioBytes(recs []debug.CallRecord) float64 {
	var sum int64
	for _, r := range recs {
		sum += r.Bytes
	}
	return float64(sum)
}

// simplifyContentType trims charset / boundary parameters so "application/
// json; charset=utf-8" and "application/json" aggregate into one bucket.
// Empty input (no header sent — e.g. cached informer reads) maps to "cache".
func simplifyContentType(s string) string {
	if s == "" {
		return "cache"
	}
	if i := indexByte(s, ';'); i >= 0 {
		s = s[:i]
	}
	// Strip the verbose "application/" prefix Kubernetes uses everywhere so
	// the cell column fits on one line in the markdown table.
	const prefix = "application/"
	if len(s) > len(prefix) && s[:len(prefix)] == prefix {
		s = s[len(prefix):]
	}
	// "vnd.kubernetes.protobuf" → "k8s-proto" for table sanity.
	if s == "vnd.kubernetes.protobuf" {
		return "k8s-proto"
	}
	return s
}

func indexByte(s string, c byte) int {
	for i := range len(s) {
		if s[i] == c {
			return i
		}
	}
	return -1
}

// chainWrap composes two rest.Config.WrapTransport functions. The outer
// wrapper sees the final bytes after any auth/decoding wrapper has touched
// them — which is what the meter needs.
func chainWrap(existing, outer func(http.RoundTripper) http.RoundTripper) func(http.RoundTripper) http.RoundTripper {
	return func(rt http.RoundTripper) http.RoundTripper {
		if existing != nil {
			rt = existing(rt)
		}
		return outer(rt)
	}
}

// Compile-time guard: every registered ID resolves.
func init() {
	for _, id := range AllVariants() {
		if _, err := Lookup(id); err != nil {
			panic(fmt.Sprintf("bench registry mismatch: %v", err))
		}
	}
	for _, id := range AllScenarios() {
		if _, err := LookupScenario(id); err != nil {
			panic(fmt.Sprintf("bench scenario registry mismatch: %v", err))
		}
	}
}
