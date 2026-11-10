// safina-versus compares safina with k9s from the outside: both run in a
// pseudo-terminal against the same cluster through the same recording proxy,
// receive the same keystrokes, and are timed by when the expected screen
// appears. See docs/development.md, "Comparing with k9s".
//
//	safina-versus run   --context safina-bench --rounds 10
//	safina-versus probe --tool k9s --context safina-bench --keys ':deploy<enter>'
package main

import (
	"cmp"
	"flag"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "run":
		err = runCmd(os.Args[2:])
	case "probe":
		err = probeCmd(os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "safina-versus:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: safina-versus run|probe [flags]  (-h for flags)")
	os.Exit(2)
}

type common struct {
	kubeconfig, context string
	safinaBin, k9sBin   string
	cols, rows          int
	rtt                 time.Duration
	quiet               time.Duration
}

func (c *common) register(fs *flag.FlagSet) {
	home, _ := os.UserHomeDir()
	fs.StringVar(&c.kubeconfig, "kubeconfig", filepath.Join(home, ".kube/config"), "kubeconfig holding the real credentials")
	fs.StringVar(&c.context, "context", "safina-bench", "context to benchmark against")
	fs.StringVar(&c.safinaBin, "safina", "bin/safina", "safina binary")
	fs.StringVar(&c.k9sBin, "k9s", "bench/.build/k9s", "k9s binary (make versus-k9s fetches the pinned release)")
	fs.IntVar(&c.cols, "cols", 200, "terminal columns")
	fs.IntVar(&c.rows, "rows", 50, "terminal rows")
	fs.DurationVar(&c.rtt, "rtt", 0, "added latency per API request, to model a remote cluster")
	fs.DurationVar(&c.quiet, "quiet", 150*time.Millisecond, "no output for this long counts as settled")
}

func (c *common) tools() (map[string]*Tool, error) {
	tools := map[string]*Tool{
		"safina": {Name: "safina", Bin: c.safinaBin, command: safinaCommand, script: safinaScript()},
		"k9s":    {Name: "k9s", Bin: c.k9sBin, command: k9sCommand, script: k9sScript()},
	}
	for _, t := range tools {
		abs, err := filepath.Abs(t.Bin)
		if err != nil {
			return nil, err
		}
		if _, err := os.Stat(abs); err != nil {
			return nil, fmt.Errorf("%s binary: %w", t.Name, err)
		}
		t.Bin = abs
		t.Version = toolVersion(t)
	}
	return tools, nil
}

// probeCmd launches one tool, plays keys and prints the screens. It is how
// scenario predicates are written and checked against the real screens.
//
// Key syntax: plain characters, plus <enter> <esc> <tab> <down> <up>
// <backspace> <ctrl+x>, and <wait> for a one-second pause. A screen is
// printed after launch settles and after every key group separated by
// spaces... keys inside one group are sent together.
func probeCmd(args []string) error {
	fs := flag.NewFlagSet("probe", flag.ExitOnError)
	var c common
	c.register(fs)
	tool := fs.String("tool", "safina", "safina or k9s")
	keys := fs.String("keys", "", "key groups separated by spaces")
	settle := fs.Duration("settle", 3*time.Second, "time to wait after launch before the first dump")
	lines := fs.Int("lines", 0, "print only the first N rows of each screen (0: all)")
	var extraEnv []string
	fs.Func("env", "KEY=VALUE added to the tool's environment (repeatable), e.g. SAFINA_CPUPROFILE=/tmp/cpu.out", func(v string) error {
		if !strings.Contains(v, "=") {
			return fmt.Errorf("want KEY=VALUE, got %q", v)
		}
		extraEnv = append(extraEnv, v)
		return nil
	})
	_ = fs.Parse(args)

	tools, err := c.tools()
	if err != nil {
		return err
	}
	t, ok := tools[*tool]
	if !ok {
		return fmt.Errorf("unknown tool %q", *tool)
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
	env := Env{Kubeconfig: kc, Context: c.context, Home: filepath.Join(work, t.Name)}
	if err = os.MkdirAll(env.Home, 0o700); err != nil {
		return err
	}
	cmd := t.Command(env)
	cmd.Env = append(cmd.Env, extraEnv...)
	s, err := StartSession(t.Name, cmd, c.cols, c.rows, c.quiet)
	if err != nil {
		return err
	}
	s.Marker = env.Home
	defer s.Close()
	time.Sleep(*settle)
	s.Quiet()
	fmt.Printf("── launch ─ %d bytes, %+v\n%s\n", s.OutputBytes(), proxy.Traffic(), head(s.Dump(), *lines))
	for _, group := range strings.Fields(*keys) {
		ks, wait := parseKeys(group)
		if len(ks) > 0 {
			st, err := s.Do(ks, Differs(s.Styled()), 5*time.Second)
			fmt.Printf("── %s ─ latency %v settle %v bytes %d err %v\n", group, st.Latency, st.Settle, st.Bytes, err)
		}
		if wait > 0 {
			time.Sleep(wait)
		}
		s.Quiet()
		fmt.Println(head(s.Dump(), *lines))
	}
	fmt.Printf("── API requests by endpoint ─ %+v\n", proxy.Traffic())
	eps := proxy.Endpoints()
	for _, k := range slices.SortedFunc(maps.Keys(eps), func(a, b string) int { return cmp.Compare(eps[b], eps[a]) }) {
		fmt.Printf("%6d  %s\n", eps[k], k)
	}
	return nil
}

func parseKeys(group string) ([]Key, time.Duration) {
	var keys []Key
	var wait time.Duration
	for len(group) > 0 {
		if group[0] == '<' {
			if end := strings.IndexByte(group, '>'); end > 0 {
				name := group[1:end]
				group = group[end+1:]
				if name == "wait" {
					wait += time.Second
				} else {
					keys = append(keys, Key(name))
				}
				continue
			}
		}
		r := []rune(group)[0]
		keys = append(keys, Key(string(r)))
		group = group[len(string(r)):]
	}
	return keys, wait
}

func head(text string, n int) string {
	if n <= 0 {
		return text
	}
	rows := strings.SplitN(text, "\n", n+1)
	return strings.Join(rows[:min(n, len(rows))], "\n")
}
