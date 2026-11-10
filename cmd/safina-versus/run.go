package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Sample is one timed interaction.
type Sample struct {
	Tool      string  `json:"tool"`
	Op        string  `json:"op"`
	Round     int     `json:"round"`
	LatencyMs float64 `json:"latency_ms"`
	SettleMs  float64 `json:"settle_ms"`
	Bytes     int64   `json:"bytes"`
	Busy      bool    `json:"busy,omitempty"`
}

// SessionStat describes one launch of a tool, from start to quit.
type SessionStat struct {
	Tool    string  `json:"tool"`
	Round   int     `json:"round"`
	Warmup  bool    `json:"warmup"`
	Error   string  `json:"error,omitempty"`
	RSS     int64   `json:"rss_bytes"`
	HWM     int64   `json:"hwm_bytes"`
	CPUMs   float64 `json:"cpu_ms"`
	Startup Traffic `json:"startup_traffic"`
	Total   Traffic `json:"total_traffic"`

	IdleSeconds  float64 `json:"idle_seconds,omitempty"`
	IdleCPUMs    float64 `json:"idle_cpu_ms,omitempty"`
	IdleTraffic  Traffic `json:"idle_traffic"`
	IdleOutBytes int64   `json:"idle_output_bytes,omitempty"`

	// Requests by endpoint (see endpointKey), for the launch and while idle.
	Endpoints     map[string]int64 `json:"endpoints,omitempty"`
	IdleEndpoints map[string]int64 `json:"idle_endpoints,omitempty"`
}

type Environment struct {
	Date     time.Time         `json:"date"`
	Host     string            `json:"host"`
	CPU      string            `json:"cpu"`
	Kernel   string            `json:"kernel"`
	GoArch   string            `json:"goarch"`
	Versions map[string]string `json:"versions"`
	Context  string            `json:"context"`
	K8s      string            `json:"kubernetes"`
	Fixture  map[string]int    `json:"fixture"`
	RTTMs    float64           `json:"rtt_ms"`
	Cols     int               `json:"cols"`
	Rows     int               `json:"rows"`
	Rounds   int               `json:"rounds"`
	Warmup   int               `json:"warmup"`
	Seed     uint64            `json:"seed"`
	Notes    []string          `json:"notes"`
}

type Results struct {
	Env      Environment   `json:"environment"`
	Samples  []Sample      `json:"samples"`
	Sessions []SessionStat `json:"sessions"`
}

// Rec is the handle a script uses to drive its session and record samples.
type Rec struct {
	s       *Session
	tool    string
	round   int
	record  bool
	reps    int
	timeout time.Duration
	res     *Results
	outDir  string
	last    map[string]Step
}

func (r *Rec) add(op string, st Step) {
	if r.last == nil {
		r.last = map[string]Step{}
	}
	r.last[op] = st
	if r.record {
		r.res.Samples = append(r.res.Samples, Sample{
			Tool: r.tool, Op: op, Round: r.round,
			LatencyMs: ms(st.Latency), SettleMs: ms(st.Settle), Bytes: st.Bytes, Busy: st.Busy,
		})
	}
}

func (r *Rec) lastStep(op string) Step { return r.last[op] }

// Reps runs an operation that yields one sample several times per launch.
func (r *Rec) Reps(fn func() error) error {
	for range max(r.reps, 1) {
		if err := fn(); err != nil {
			return err
		}
	}
	return nil
}

// Measure sends keys, waits for until, and records the interaction as op.
func (r *Rec) Measure(op string, keys []Key, until Predicate) error {
	st, err := r.s.Do(keys, until, r.timeout)
	if err != nil {
		return r.fail(op, err)
	}
	r.add(op, st)
	return nil
}

// MeasureEach records one sample per key, each timed to the first frame that
// differs from the one before it.
func (r *Rec) MeasureEach(op string, keys []Key) error {
	for _, k := range keys {
		if err := r.Measure(op, []Key{k}, Differs(r.s.Styled())); err != nil {
			return err
		}
	}
	return nil
}

// Go navigates without recording: setup between measured steps.
func (r *Rec) Go(keys []Key, until Predicate) error {
	if _, err := r.s.Do(keys, until, r.timeout); err != nil {
		return r.fail("navigation", err)
	}
	return nil
}

func (r *Rec) fail(op string, err error) error {
	dir := filepath.Join(r.outDir, "screens")
	_ = os.MkdirAll(dir, 0o750)
	path := filepath.Join(dir, fmt.Sprintf("%s-%s-round%d.txt", r.tool, op, r.round))
	_ = os.WriteFile(path, []byte(r.s.Dump()+"\n"), 0o600)
	return fmt.Errorf("%s %s: %w (screen saved to %s)", r.tool, op, err, path)
}

func ms(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }

func runCmd(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	var c common
	c.register(fs)
	rounds := fs.Int("rounds", 10, "measured launches per tool")
	warmup := fs.Int("warmup", 1, "unrecorded launches per tool first (fills each tool's caches)")
	seed := fs.Uint64("seed", 42, "seed for the tool order in each round and the bootstrap")
	idle := fs.Duration("idle", 30*time.Second, "idle time on the pod list per launch (0 skips)")
	ops := fs.String("ops", "all", "comma-separated operation IDs, or all")
	reps := fs.Int("reps", 3, "repetitions per launch of single-sample operations")
	timeout := fs.Duration("timeout", 30*time.Second, "longest wait for one screen")
	pause := fs.Duration("pause", 2*time.Second, "wait after the pod list appears before timing interactions")
	out := fs.String("out", "bench/versus/out", "output directory")
	_ = fs.Parse(args)

	tools, err := c.tools()
	if err != nil {
		return err
	}
	selected, err := selectOps(*ops)
	if err != nil {
		return err
	}
	work, err := os.MkdirTemp("", "safina-versus-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(work) }()
	proxy, err := StartProxy(c.kubeconfig, c.context, c.rtt)
	if err != nil {
		return err
	}
	defer proxy.Close()
	kc, err := proxy.WriteKubeconfig(work, c.context)
	if err != nil {
		return err
	}

	res := &Results{Env: environment(&c, tools, *rounds, *warmup, *seed)}
	res.Env.Notes = append(res.Env.Notes,
		"k9s runs --readonly --logoless --splashless, default 2s refresh, --command pods --all-namespaces.",
		"safina runs --context. Its broker daemon (safina serve) counts toward its CPU and memory and is stopped after each launch. Its launch includes the two Enter presses from the namespace list to all-namespace pods, sent as soon as each screen is ready.",
		fmt.Sprintf("Interactions start %v after the pod list appears. safina's filter is fuzzy and ranks exact matches first; k9s keeps exact matches only. Filtering counts as done when the ten app-12 pods are the top rows.", *pause),
		"Latency is the time from writing the key to the PTY until the first output chunk after which the screen satisfies the step; settle is until the tool went quiet.",
		fmt.Sprintf("Quiet window %v. Added API latency %v per request (watch events after the first byte are not delayed).", c.quiet, c.rtt),
	)
	order := []string{"safina", "k9s"}
	rng := rand.New(rand.NewPCG(*seed, 1)) // #nosec G404 -- Seeded for a reproducible tool order, not for secrecy.
	for round := range *warmup + *rounds {
		rng.Shuffle(len(order), func(i, j int) { order[i], order[j] = order[j], order[i] })
		for _, name := range order {
			t := tools[name]
			env := Env{Kubeconfig: kc, Context: c.context, Home: filepath.Join(work, name)}
			if err := os.MkdirAll(env.Home, 0o700); err != nil {
				return err
			}
			warm := round < *warmup
			label := fmt.Sprintf("round %d", round-*warmup+1)
			if warm {
				label = "warmup"
			}
			fmt.Fprintf(os.Stderr, "%-8s %-6s ", label, name)
			stat := runSession(&c, t, env, proxy, res, *out, round-*warmup, !warm, selected, *reps, *pause, *idle, *timeout)
			stat.Warmup = warm
			res.Sessions = append(res.Sessions, stat)
			if stat.Error != "" {
				fmt.Fprintln(os.Stderr, "FAILED:", stat.Error)
			} else {
				fmt.Fprintf(os.Stderr, "ok  rss %s  requests %d\n", humanBytes(float64(stat.RSS)), stat.Total.Requests)
			}
		}
	}
	return writeResults(res, *out)
}

func runSession(c *common, t *Tool, env Env, proxy *Proxy, res *Results, outDir string, round int, record bool, ops []string, reps int, pause, idle, timeout time.Duration) SessionStat {
	stat := SessionStat{Tool: t.Name, Round: round}
	before, beforeEP := proxy.Traffic(), proxy.Endpoints()
	start := time.Now()
	s, err := StartSession(t.Name, t.Command(env), c.cols, c.rows, c.quiet)
	if err != nil {
		stat.Error = err.Error()
		return stat
	}
	s.Marker = env.Home
	defer s.Close()
	r := &Rec{s: s, tool: t.Name, round: round, record: record, reps: reps, timeout: timeout, res: res, outDir: outDir}

	err = t.script.Launch(r, start)
	stat.Startup = proxy.Traffic().Sub(before)
	if err == nil {
		// Background loading after the first screen belongs to startup, not
		// to the first interaction that happens to be timed during it.
		time.Sleep(pause)
		s.Quiet()
	}
	for _, id := range ops {
		if err != nil {
			break
		}
		if op, ok := t.script.Ops[id]; ok {
			err = op(r)
		}
	}
	if err == nil && idle > 0 {
		s.Quiet()
		p0, t0, o0, e0 := TreeStat(s.Pid(), s.Marker), proxy.Traffic(), s.OutputBytes(), proxy.Endpoints()
		time.Sleep(idle)
		p1, t1, o1 := TreeStat(s.Pid(), s.Marker), proxy.Traffic(), s.OutputBytes()
		stat.IdleEndpoints = SubEndpoints(proxy.Endpoints(), e0)
		stat.IdleSeconds = idle.Seconds()
		stat.IdleCPUMs = ms(p1.CPU - p0.CPU)
		stat.IdleTraffic = t1.Sub(t0)
		stat.IdleOutBytes = o1 - o0
	}
	if err != nil {
		stat.Error = err.Error()
	}
	p := TreeStat(s.Pid(), s.Marker)
	stat.RSS, stat.HWM, stat.CPUMs = p.RSS, p.HWM, ms(p.CPU)
	stat.Total = proxy.Traffic().Sub(before)
	stat.Endpoints = SubEndpoints(proxy.Endpoints(), beforeEP)
	return stat
}

func selectOps(arg string) ([]string, error) {
	if arg == "all" || arg == "" {
		ids := make([]string, len(opCatalog))
		for i, o := range opCatalog {
			ids[i] = o.ID
		}
		return ids, nil
	}
	var ids []string
	for _, id := range strings.Split(arg, ",") {
		id = strings.TrimSpace(id)
		if _, ok := opTitle(id); !ok || id == opStartup || id == opFirstData {
			return nil, fmt.Errorf("unknown operation %q", id)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func environment(c *common, tools map[string]*Tool, rounds, warmup int, seed uint64) Environment {
	host, _ := os.Hostname()
	kernel, _ := os.ReadFile("/proc/sys/kernel/osrelease")
	e := Environment{
		Date: time.Now(), Host: host, CPU: cpuModel(), Kernel: strings.TrimSpace(string(kernel)), GoArch: runtime.GOARCH,
		Versions: map[string]string{}, Context: c.context, RTTMs: ms(c.rtt), Cols: c.cols, Rows: c.rows,
		Rounds: rounds, Warmup: warmup, Seed: seed, Fixture: map[string]int{},
	}
	for name, t := range tools {
		e.Versions[name] = t.Version
	}
	kubectl := func(args ...string) string {
		out, _ := exec.CommandContext(context.Background(), "kubectl", append([]string{"--kubeconfig", c.kubeconfig, "--context", c.context}, args...)...).Output() // #nosec G204 -- Fixed kubectl reads with the operator's own flags.
		return strings.TrimSpace(string(out))
	}
	var v struct {
		ServerVersion struct {
			GitVersion string `json:"gitVersion"`
		} `json:"serverVersion"`
	}
	_ = json.Unmarshal([]byte(kubectl("version", "-o", "json")), &v)
	e.K8s = v.ServerVersion.GitVersion
	for _, kind := range []string{"pods", "deployments", "namespaces", "services", "configmaps"} {
		if out := kubectl("get", kind, "-A", "--no-headers"); out != "" {
			e.Fixture[kind] = strings.Count(out, "\n") + 1
		}
	}
	return e
}

func cpuModel() string {
	b, _ := os.ReadFile("/proc/cpuinfo")
	for _, line := range strings.Split(string(b), "\n") {
		if name, ok := strings.CutPrefix(line, "model name"); ok {
			return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(name), ":"))
		}
	}
	return runtime.GOARCH
}

func toolVersion(t *Tool) string {
	out, err := exec.CommandContext(context.Background(), t.Bin, "version", "-s").CombinedOutput() // #nosec G204 -- The operator-chosen binary under test.
	if err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			if v, ok := strings.CutPrefix(line, "Version"); ok {
				return strings.TrimSpace(v)
			}
		}
	}
	out, _ = exec.CommandContext(context.Background(), t.Bin, "--version").CombinedOutput() // #nosec G204 -- The operator-chosen binary under test.
	v := strings.TrimSpace(string(out))
	return strings.TrimPrefix(v, t.Name+" version ")
}
