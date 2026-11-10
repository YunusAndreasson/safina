package main

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/YunusAndreasson/safina/internal/k8s/bench"
)

// The test binary doubles as a fake TUI: in raw mode it answers every "j"
// with a new row after FAKE_TUI_DELAY, and prints unrelated noise on a timer
// when FAKE_TUI_NOISE is set, the way k9s refreshes its table.
func TestMain(m *testing.M) {
	if os.Getenv("FAKE_TUI") == "1" {
		fakeTUI()
		return
	}
	os.Exit(m.Run())
}

func fakeTUI() {
	delay, _ := time.ParseDuration(os.Getenv("FAKE_TUI_DELAY"))
	t, err := unix.IoctlGetTermios(0, unix.TCGETS)
	if err == nil {
		t.Lflag &^= unix.ICANON | unix.ECHO | unix.ISIG
		t.Iflag &^= unix.ICRNL
		t.Cc[unix.VMIN], t.Cc[unix.VTIME] = 1, 0
		_ = unix.IoctlSetTermios(0, unix.TCSETS, t)
	}
	if noise, _ := time.ParseDuration(os.Getenv("FAKE_TUI_NOISE")); noise > 0 {
		go func() {
			for i := 0; ; i++ {
				time.Sleep(noise)
				fmt.Printf("\x1b[1;30Hclock %d", i)
			}
		}()
	}
	fmt.Print("\x1b[2J\x1b[Hready")
	buf := make([]byte, 64)
	row := 0
	for {
		n, err := os.Stdin.Read(buf)
		if err != nil {
			return
		}
		for _, b := range buf[:n] {
			switch b {
			case 'j':
				row++
				time.Sleep(delay)
				fmt.Printf("\x1b[%d;1Hrow %d", row+1, row)
			case 'q':
				return
			}
		}
	}
}

func startFake(t *testing.T, delay, noise time.Duration) *Session {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), executable, "-test.run=^$") // #nosec G204 -- The current test executable, re-run as the fake program.
	cmd.Env = append(os.Environ(), "FAKE_TUI=1", "FAKE_TUI_DELAY="+delay.String(), "FAKE_TUI_NOISE="+noise.String())
	s, err := StartSession("fake", cmd, 80, 24, 50*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	if _, err := s.Expect(Contains("ready"), time.Now(), 5*time.Second); err != nil {
		t.Fatal(err)
	}
	return s
}

func sampleFake(t *testing.T, s *Session, n int) []float64 {
	t.Helper()
	var out []float64
	for i := 1; i <= n; i++ {
		st, err := s.Do([]Key{"j"}, Contains("row "+strconv.Itoa(i)), 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, ms(st.Latency))
	}
	return out
}

// The driver must report the program's own response time, not its own
// parsing or scheduling: a 30ms response measures as 30ms within 2ms.
func TestLatencyMatchesProgramDelay(t *testing.T) {
	const delay = 30 * time.Millisecond
	got := sampleFake(t, startFake(t, delay, 0), 20)
	median := bench.Percentile(got, 50)
	if median < ms(delay) || median > ms(delay)+2 {
		t.Fatalf("median latency %.2fms for a %v program, samples %v", median, delay, got)
	}
}

// A predicate armed before the key never matches on a screen that was
// already showing the text, and timeouts name what was awaited.
func TestDoWaitsForNewOutput(t *testing.T) {
	s := startFake(t, 0, 0)
	if _, err := s.Do(nil, Contains("ready"), 200*time.Millisecond); err == nil {
		t.Fatal("matched a screen that did not change")
	}
	st, err := s.Do([]Key{"j"}, Contains("row 1"), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if st.Settle < st.Latency || st.Bytes == 0 {
		t.Fatalf("settle %v before latency %v, or no bytes (%d)", st.Settle, st.Latency, st.Bytes)
	}
}

// Differs ignores nothing: periodic noise can land in a sample, but the
// median stays at the real response.
func TestDiffersUnderPeriodicNoise(t *testing.T) {
	s := startFake(t, 15*time.Millisecond, 500*time.Millisecond)
	got := make([]float64, 0, 20)
	for range 20 {
		st, err := s.Do([]Key{"j"}, Differs(s.Styled()), 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, ms(st.Latency))
	}
	if m := bench.Percentile(got, 50); m < 14 || m > 18 {
		t.Fatalf("median %.2fms for a 15ms program with noise, samples %v", m, got)
	}
}

// A/A: the same program measured twice gives no verdict, and a 2× slower one
// is told apart. This is the noise floor the report's verdicts rely on.
func TestVerdictsOnKnownPrograms(t *testing.T) {
	a := sampleFake(t, startFake(t, 20*time.Millisecond, 0), 30)
	b := sampleFake(t, startFake(t, 20*time.Millisecond, 0), 30)
	slow := sampleFake(t, startFake(t, 40*time.Millisecond, 0), 30)
	if v := verdict(a, b); v != "no difference" {
		t.Errorf("same program: %s (a %v, b %v)", v, a, b)
	}
	if v := verdict(a, slow); v != "safina faster" {
		t.Errorf("20ms vs 40ms: %s", v)
	}
	if v := verdict(slow, a); v != "k9s faster" {
		t.Errorf("40ms vs 20ms: %s", v)
	}
}

func TestParseKeys(t *testing.T) {
	keys, wait := parseKeys(":deploy<enter><wait>")
	if len(keys) != 8 || keys[7] != "enter" || wait != time.Second {
		t.Fatalf("keys %v wait %v", keys, wait)
	}
}

func TestIsCollection(t *testing.T) {
	for path, want := range map[string]bool{
		"/api/v1/pods": true, "/api/v1/namespaces/a/pods": true, "/api/v1/namespaces/a/pods/p": false,
		"/apis/apps/v1/deployments": true, "/apis/apps/v1/namespaces/a/deployments/d": false,
		"/api/v1/namespaces": true, "/api/v1/namespaces/a": false, "/version": false, "/apis": false,
	} {
		if got := isCollection(path); got != want {
			t.Errorf("%s: %v, want %v", path, got, want)
		}
	}
}

func verdict(a, b []float64) string {
	return verdictWithin(bench.MedianRatioCI(a, b, 4000, 0.95, 1), bench.Percentile(a, 50), bench.Percentile(b, 50), minLatency)
}
