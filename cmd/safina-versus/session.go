package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/YunusAndreasson/safina/internal/terminal"
)

// Screen is what a predicate sees: the visible text, and the styled frame for
// changes that are only visible as styling (a moved selection highlight).
type Screen struct {
	term   *terminal.Terminal
	plain  *string
	styled *string
}

func (s *Screen) Plain() string {
	if s.plain == nil {
		text, _ := s.term.Plain()
		s.plain = &text
	}
	return *s.plain
}

func (s *Screen) Styled() string {
	if s.styled == nil {
		frame, _, _ := s.term.Frame()
		s.styled = &frame
	}
	return *s.styled
}

// Predicate reports whether the screen shows the state a step waits for.
type Predicate struct {
	Desc  string
	Match func(*Screen) bool
}

func Contains(text string) Predicate {
	return Predicate{Desc: fmt.Sprintf("screen contains %q", text), Match: func(s *Screen) bool { return strings.Contains(s.Plain(), text) }}
}

func All(ps ...Predicate) Predicate {
	descs := make([]string, len(ps))
	for i, p := range ps {
		descs[i] = p.Desc
	}
	return Predicate{Desc: strings.Join(descs, " and "), Match: func(s *Screen) bool {
		for _, p := range ps {
			if !p.Match(s) {
				return false
			}
		}
		return true
	}}
}

func Not(p Predicate) Predicate {
	return Predicate{Desc: "not " + p.Desc, Match: func(s *Screen) bool { return !p.Match(s) }}
}

// Step is one measured interaction.
type Step struct {
	Latency time.Duration // key write → first output chunk that satisfies the predicate
	Settle  time.Duration // key write → last output before the quiet window
	Bytes   int64         // terminal output written by the tool until settled
	Busy    bool          // still drawing when the settle cap ran out (a spinner)
}

// settleCap bounds the wait for quiet. A tool animating a spinner never goes
// quiet; its step is recorded as busy instead of failing the launch.
const settleCap = 5 * time.Second

type waiter struct {
	match   Predicate
	matched time.Time
	done    chan struct{}
}

// Session drives one tool inside a pseudo-terminal. Output is timestamped
// when it is read, before it is parsed, so emulator cost never inflates a
// measured latency.
type Session struct {
	Name string
	cmd  *exec.Cmd
	pty  *os.File
	term *terminal.Terminal

	mu       sync.Mutex
	wait     *waiter
	lastOut  time.Time
	outBytes int64
	readErr  error
	exited   chan struct{}
	quiet    time.Duration
	// Marker identifies detached processes the tool started (see Owned).
	Marker string
}

func StartSession(name string, cmd *exec.Cmd, cols, rows int, quiet time.Duration) (*Session, error) {
	term, err := terminal.New(cols, rows)
	if err != nil {
		return nil, err
	}
	ptmx, err := startPTY(cmd, cols, rows)
	if err != nil {
		term.Close()
		return nil, err
	}
	s := &Session{Name: name, cmd: cmd, pty: ptmx, term: term, exited: make(chan struct{}), quiet: quiet}
	go s.read()
	go func() { _ = cmd.Wait(); close(s.exited) }()
	return s, nil
}

func (s *Session) Pid() int { return s.cmd.Process.Pid }

func (s *Session) read() {
	buf := make([]byte, 256<<10)
	for {
		n, err := s.pty.Read(buf)
		now := time.Now()
		if n > 0 {
			s.mu.Lock()
			replies, _ := s.term.Feed(buf[:n])
			s.outBytes += int64(n)
			s.lastOut = now
			if w := s.wait; w != nil && w.match.Match(&Screen{term: s.term}) {
				w.matched = now
				s.wait = nil
				close(w.done)
			}
			s.mu.Unlock()
			if len(replies) > 0 {
				_, _ = s.pty.Write(replies)
			}
		}
		if err != nil {
			s.mu.Lock()
			s.readErr = err
			s.mu.Unlock()
			return
		}
	}
}

// Key is one keypress in the notation of tea.KeyPressMsg.String: "j",
// "enter", "esc", "ctrl+c", ":" …
type Key string

func Keys(text string) []Key {
	keys := make([]Key, 0, len(text))
	for _, r := range text {
		keys = append(keys, Key(string(r)))
	}
	return keys
}

var named = map[string]rune{
	"enter": tea.KeyEnter, "esc": tea.KeyEscape, "tab": tea.KeyTab, "backspace": tea.KeyBackspace,
	"space": tea.KeySpace, "up": tea.KeyUp, "down": tea.KeyDown, "left": tea.KeyLeft, "right": tea.KeyRight,
}

func pressMsg(k Key) tea.KeyPressMsg {
	spec := string(k)
	var mod tea.KeyMod
	if rest, ok := strings.CutPrefix(spec, "ctrl+"); ok && len(rest) > 0 {
		mod, spec = tea.ModCtrl, rest
	}
	if code, ok := named[spec]; ok {
		return tea.KeyPressMsg{Code: code, Mod: mod}
	}
	r := []rune(spec)[0]
	msg := tea.KeyPressMsg{Code: r, Mod: mod}
	if mod == 0 {
		msg.Text = spec
	}
	return msg
}

// encode renders the keys with the keyboard protocol the program negotiated,
// as the user's terminal would.
func (s *Session) encode(keys []Key) ([]byte, error) {
	var out []byte
	for _, k := range keys {
		b, err := s.term.Key(pressMsg(k))
		if err != nil {
			return nil, err
		}
		out = append(out, b...)
	}
	return out, nil
}

var ErrTimeout = errors.New("timed out")

// Do sends keys and waits until the screen satisfies until. The predicate is
// armed before the keys are written and only evaluated on output that arrives
// afterwards, so a screen that already matched does not count.
func (s *Session) Do(keys []Key, until Predicate, timeout time.Duration) (Step, error) {
	s.mu.Lock()
	payload, err := s.encode(keys)
	if err != nil {
		s.mu.Unlock()
		return Step{}, err
	}
	w := &waiter{match: until, done: make(chan struct{})}
	s.wait = w
	startBytes := s.outBytes
	start := time.Now()
	s.mu.Unlock()

	if len(payload) > 0 {
		if _, err := s.pty.Write(payload); err != nil {
			return Step{}, err
		}
	}
	return s.finish(w, start, startBytes, timeout)
}

// Expect waits for a predicate without sending input. Start is the reference
// time, e.g. process launch.
func (s *Session) Expect(until Predicate, start time.Time, timeout time.Duration) (Step, error) {
	s.mu.Lock()
	if until.Match(&Screen{term: s.term}) {
		matched := s.lastOut
		s.mu.Unlock()
		st := s.settle(start, 0)
		st.Latency = matched.Sub(start)
		return st, nil
	}
	w := &waiter{match: until, done: make(chan struct{})}
	s.wait = w
	s.mu.Unlock()
	return s.finish(w, start, 0, timeout)
}

func (s *Session) finish(w *waiter, start time.Time, startBytes int64, timeout time.Duration) (Step, error) {
	select {
	case <-w.done:
	case <-time.After(timeout):
		s.mu.Lock()
		if s.wait == w {
			s.wait = nil
		}
		s.mu.Unlock()
		return Step{}, fmt.Errorf("%s: waiting for %s: %w", s.Name, w.match.Desc, ErrTimeout)
	case <-s.exited:
		return Step{}, fmt.Errorf("%s: exited while waiting for %s", s.Name, w.match.Desc)
	}
	st := s.settle(start, startBytes)
	st.Latency = w.matched.Sub(start)
	return st, nil
}

// settle waits until the tool has written nothing for the quiet window.
func (s *Session) settle(start time.Time, startBytes int64) Step {
	deadline := time.Now().Add(settleCap)
	for {
		s.mu.Lock()
		last, bytes := s.lastOut, s.outBytes
		s.mu.Unlock()
		idle := time.Since(last)
		switch {
		case idle >= s.quiet:
			return Step{Settle: max(last.Sub(start), 0), Bytes: bytes - startBytes}
		case time.Now().After(deadline):
			return Step{Settle: max(last.Sub(start), 0), Bytes: bytes - startBytes, Busy: true}
		default:
			time.Sleep(s.quiet - idle)
		}
	}
}

// Quiet blocks until no output arrived for the quiet window, or settleCap.
func (s *Session) Quiet() { s.settle(time.Now(), 0) }

// OutputBytes is the running total of terminal output.
func (s *Session) OutputBytes() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.outBytes
}

// Dump returns the current plain screen, for diagnostics.
func (s *Session) Dump() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	text, _ := s.term.Plain()
	return text
}

// Close asks the program to stop, then kills its process group.
func (s *Session) Close() {
	if s.cmd.Process != nil {
		_ = s.cmd.Process.Signal(os.Interrupt)
		select {
		case <-s.exited:
		case <-time.After(2 * time.Second):
			_ = killGroup(s.cmd.Process.Pid)
			<-s.exited
		}
	}
	// Stop what the tool left running, such as safina's broker, so it
	// neither runs into the next launch nor outlives the benchmark.
	for _, pid := range Owned(s.Marker) {
		_ = syscall.Kill(pid, syscall.SIGTERM)
	}
	_ = s.pty.Close()
	s.mu.Lock()
	s.term.Close()
	s.mu.Unlock()
}

// Styled returns the current styled frame, the baseline for Differs.
func (s *Session) Styled() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	frame, _, _ := s.term.Frame()
	return frame
}

// Differs matches the first frame that looks different from prev, including
// styling. It times responses whose only visible effect is a moved highlight.
func Differs(prev string) Predicate {
	return Predicate{Desc: "screen changes", Match: func(s *Screen) bool { return s.Styled() != prev }}
}
