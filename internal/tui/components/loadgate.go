package components

import "time"

// LoadGate suppresses spinner flicker for fast operations. Two thresholds:
//
//   - ShowAfter: how long a load must run before the spinner appears at all.
//     Loads that complete inside this window never paint a spinner; the user
//     just sees the result land. Default 200ms — below Nielsen's ~250ms
//     "user feels system is reacting" threshold, above the ~100ms perceptual
//     "instant" floor.
//   - MinVisible: once the spinner has appeared, how long it stays visible
//     even if the load finishes quickly afterwards. Without this, slow-enough
//     loads cause a spinner-flash on shutdown that's worse than the original
//     startup flicker. Default 300ms.
//
// Zero value is valid; defaults kick in. Per-section overrides via the
// public fields if the call has different latency characteristics.
//
// Usage:
//
//	type Hot struct {
//	    gate components.LoadGate
//	    ...
//	}
//	func (s *Hot) Reset()              { s.gate.Start() }
//	func (s *Hot) SetItems(...)        { s.gate.Stop() }
//	func (s *Hot) View()               { if s.gate.Visible() { spinner } }
//
// No tea.Cmd plumbing required — the bubbles spinner ticks unconditionally
// while the program runs, which drives view re-renders frequently enough
// for Visible() to flip on its own at the gate's transition moments.
type LoadGate struct {
	ShowAfter  time.Duration
	MinVisible time.Duration
	// Clock is the time source. Nil means time.Now; tests inject a fake.
	Clock func() time.Time

	loadStartedAt time.Time
	stoppedAt     time.Time
	loading       bool
}

const (
	DefaultLoadGateShowAfter  = 200 * time.Millisecond
	DefaultLoadGateMinVisible = 300 * time.Millisecond
)

func (g *LoadGate) now() time.Time {
	if g.Clock != nil {
		return g.Clock()
	}
	return time.Now()
}

func (g *LoadGate) showAfter() time.Duration {
	if g.ShowAfter > 0 {
		return g.ShowAfter
	}
	return DefaultLoadGateShowAfter
}

func (g *LoadGate) minVisible() time.Duration {
	if g.MinVisible > 0 {
		return g.MinVisible
	}
	return DefaultLoadGateMinVisible
}

// Start records load-begin. Idempotent restart — calling Start while already
// loading re-anchors the clock to "now," which is the intended behaviour for
// user-triggered refresh during an in-flight load.
func (g *LoadGate) Start() {
	g.loadStartedAt = g.now()
	g.stoppedAt = time.Time{}
	g.loading = true
}

// Stop records load-end. Safe to call without a prior Start (no-op).
func (g *LoadGate) Stop() {
	g.stoppedAt = g.now()
	g.loading = false
}

// IsLoading reports whether a load is currently in flight (Start without a
// matching Stop). Distinct from Visible — a fast load is loading but not
// visible. Exposed for tests that want to check the loading lifecycle
// without coupling to the spinner-display thresholds.
func (g *LoadGate) IsLoading() bool { return g.loading }

// Visible reports whether the spinner should currently render. Pure: never
// mutates gate state. Behaviour:
//
//   - loading, ShowAfter not yet elapsed         → false (suppress flicker)
//   - loading, ShowAfter elapsed                 → true
//   - stopped before spinner would have appeared → false (never showed)
//   - stopped after spinner appeared, within MinVisible → true (hold open)
//   - stopped, MinVisible elapsed                → false (done)
func (g *LoadGate) Visible() bool {
	if g.loadStartedAt.IsZero() {
		return false
	}
	now := g.now()
	showAt := g.loadStartedAt.Add(g.showAfter())
	if g.loading {
		return !now.Before(showAt)
	}
	if g.stoppedAt.Before(showAt) {
		return false
	}
	return now.Sub(showAt) < g.minVisible()
}
