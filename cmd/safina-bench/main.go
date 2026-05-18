// safina-bench is the structured benchmark harness for the safina k8s client.
// See .claude/plans/create-a-plan-for-mighty-storm.md for the full design.
//
// Usage:
//
//	safina-bench --kubeconfig ~/.kube/config --context minikube-safina-bench \
//	    --variants V0-baseline,V1-protobuf --scenarios S1-cold-list-pods \
//	    --n 30 --seed 42 --out bench/out
//
// The default --variants=all --scenarios=all runs the full grid.
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/yunus/safina/internal/debug"
	"github.com/yunus/safina/internal/k8s/bench"
)

func main() {
	var (
		kubeconfig     string
		kubeContext    string
		variantsArg    string
		scenariosArg   string
		n              int
		warmup         int
		seed           int64
		iterTimeout    time.Duration
		outDir         string
		quick          bool
		baselinePath   string
		recordBaseline bool
		tolerancePct   float64
	)

	flag.StringVar(&kubeconfig, "kubeconfig", defaultKubeconfig(), "path to kubeconfig")
	flag.StringVar(&kubeContext, "context", "", "kubeconfig context to target (required)")
	flag.StringVar(&variantsArg, "variants", "all", "comma-separated variant IDs, or 'all'")
	flag.StringVar(&scenariosArg, "scenarios", "all", "comma-separated scenario IDs, or 'all'")
	flag.IntVar(&n, "n", 30, "iterations per (variant, scenario)")
	flag.IntVar(&warmup, "warmup", 5, "warmup iterations per variant (discarded)")
	flag.Int64Var(&seed, "seed", 42, "RNG seed for iteration ordering")
	flag.DurationVar(&iterTimeout, "iter-timeout", 30*time.Second, "per-iteration deadline")
	flag.StringVar(&outDir, "out", "bench/out", "output directory for CSV + report")
	flag.BoolVar(&quick, "quick", false, "quick mode: V0+V11 × S1+S2 with --n=10, exits non-zero on regression")
	flag.StringVar(&baselinePath, "baseline", "bench/baseline.json", "baseline file (read for --quick, written by --record-baseline)")
	flag.BoolVar(&recordBaseline, "record-baseline", false, "overwrite --baseline with this run's p95 numbers")
	flag.Float64Var(&tolerancePct, "tolerance-pct", 15.0, "regression budget (p95 % over baseline)")
	flag.Parse()

	if kubeContext == "" {
		fail("--context is required (e.g. --context minikube-safina-bench)")
	}

	if quick {
		variantsArg = string(bench.V0Baseline) + "," + string(bench.V11ComboBest)
		scenariosArg = string(bench.S1ColdListPods) + "," + string(bench.S2RepeatListPods)
		if n == 30 {
			n = 10 // --quick default unless overridden
		}
	}

	variants, err := resolveVariants(variantsArg)
	if err != nil {
		fail("%v", err)
	}
	scenarios, err := resolveScenarios(scenariosArg)
	if err != nil {
		fail("%v", err)
	}

	debug.Init()
	defer debug.Close()

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	factory := func() (*rest.Config, error) {
		return buildRestConfig(kubeconfig, kubeContext)
	}

	// Sanity check: confirm the cluster is reachable BEFORE wasting compute on
	// the variant loop. The first BaseConfigFactory call is sufficient.
	if _, err := factory(); err != nil {
		fail("cannot build rest config: %v", err)
	}

	cfg := bench.Config{
		Variants:          variants,
		Scenarios:         scenarios,
		Iterations:        n,
		WarmupIters:       warmup,
		Seed:              seed,
		IterTimeout:       iterTimeout,
		BaseConfigFactory: factory,
	}

	fmt.Fprintf(os.Stderr, "running %d variants × %d scenarios × %d iters (seed=%d)\n",
		len(variants), len(scenarios), n, seed)
	t0 := time.Now()
	res, err := bench.Run(ctx, cfg)
	if err != nil {
		fail("run failed: %v", err)
	}
	fmt.Fprintf(os.Stderr, "done in %s\n", time.Since(t0).Round(time.Millisecond))

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		fail("mkdir %s: %v", outDir, err)
	}
	ts := res.StartedAt.Format("20060102-150405")
	csvPath := filepath.Join(outDir, "results-"+ts+".csv")
	mdPath := filepath.Join(outDir, "report-"+ts+".md")

	if err := writeFile(csvPath, func(f *os.File) error { return bench.WriteCSV(f, res) }); err != nil {
		fail("write csv: %v", err)
	}
	if err := writeFile(mdPath, func(f *os.File) error { return bench.WriteMarkdown(f, res, tolerancePct) }); err != nil {
		fail("write report: %v", err)
	}
	fmt.Fprintf(os.Stderr, "wrote %s\nwrote %s\n", csvPath, mdPath)

	if recordBaseline {
		if err := writeFile(baselinePath, func(f *os.File) error { return bench.WriteBaseline(f, bench.NewBaseline(res)) }); err != nil {
			fail("write baseline: %v", err)
		}
		fmt.Fprintf(os.Stderr, "recorded baseline to %s\n", baselinePath)
	}

	if quick {
		os.Exit(quickGate(res, baselinePath, tolerancePct))
	}
}

func quickGate(res *bench.Result, baselinePath string, tolerancePct float64) int {
	f, err := os.Open(baselinePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "no baseline at %s — record one with --record-baseline\n", baselinePath)
		return 0
	}
	defer func() { _ = f.Close() }()
	baseline, err := bench.ReadBaseline(f)
	if err != nil {
		fmt.Fprintf(os.Stderr, "baseline read failed: %v\n", err)
		return 1
	}
	regressed := false
	for _, c := range res.Cells {
		bp95, ok := baseline.Lookup(c.Variant, c.Scenario)
		if !ok || bp95 == 0 {
			continue
		}
		delta := (c.Summary.P95Ms - bp95) / bp95 * 100
		flag := "ok"
		if delta > tolerancePct {
			flag = "REGRESSION"
			regressed = true
		}
		fmt.Fprintf(os.Stderr, "  %s / %s p95=%.1fms baseline=%.1fms Δ=%+.1f%% [%s]\n",
			c.Variant, c.Scenario, c.Summary.P95Ms, bp95, delta, flag)
	}
	if regressed {
		return 1
	}
	return 0
}

// --- helpers -----------------------------------------------------------------

func defaultKubeconfig() string {
	if v := os.Getenv("KUBECONFIG"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".kube", "config")
}

func buildRestConfig(kubeconfig, kubeContext string) (*rest.Config, error) {
	loader := &clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeconfig}
	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		loader,
		&clientcmd.ConfigOverrides{CurrentContext: kubeContext},
	).ClientConfig()
	if err != nil {
		return nil, err
	}
	// Mirror the safina production config (client.go:71-73). Variants may
	// override ContentType in place.
	cfg.Timeout = 30 * time.Second
	cfg.QPS = 50
	cfg.Burst = 100
	// Make sure WrapTransport is non-nil so the runner can chain without a
	// special case — and audit while we're here, so SAFINA_DEBUG users see
	// per-request trails during benchmark runs too.
	existing := cfg.WrapTransport
	cfg.WrapTransport = func(rt http.RoundTripper) http.RoundTripper {
		if existing != nil {
			rt = existing(rt)
		}
		return debug.WrapTransport(rt)
	}
	return cfg, nil
}

func resolveVariants(s string) ([]bench.VariantID, error) {
	if s == "all" {
		return bench.AllVariants(), nil
	}
	parts := strings.Split(s, ",")
	out := make([]bench.VariantID, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if _, err := bench.Lookup(bench.VariantID(p)); err != nil {
			return nil, err
		}
		out = append(out, bench.VariantID(p))
	}
	return out, nil
}

func resolveScenarios(s string) ([]bench.ScenarioID, error) {
	if s == "all" {
		return bench.AllScenarios(), nil
	}
	parts := strings.Split(s, ",")
	out := make([]bench.ScenarioID, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if _, err := bench.LookupScenario(bench.ScenarioID(p)); err != nil {
			return nil, err
		}
		out = append(out, bench.ScenarioID(p))
	}
	return out, nil
}

func writeFile(path string, fn func(f *os.File) error) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return fn(f)
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "safina-bench: "+format+"\n", args...)
	os.Exit(2)
}
