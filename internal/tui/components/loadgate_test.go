package components

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// fakeClock returns a Clock closure backed by a mutable time pointer so
// tests can advance time deterministically.
func fakeClock(t0 *time.Time) func() time.Time {
	return func() time.Time { return *t0 }
}

func TestLoadGate_FastLoadNeverShowsSpinner(t *testing.T) {
	now := time.Now()
	g := LoadGate{Clock: fakeClock(&now)}
	g.Start()

	now = now.Add(50 * time.Millisecond) // inside ShowAfter
	assert.False(t, g.Visible(), "spinner suppressed during ShowAfter window")

	g.Stop()
	now = now.Add(1 * time.Second)
	assert.False(t, g.Visible(), "spinner never shows for sub-ShowAfter loads")
}

func TestLoadGate_SlowLoadShowsAfterThreshold(t *testing.T) {
	now := time.Now()
	g := LoadGate{Clock: fakeClock(&now)}
	g.Start()

	now = now.Add(199 * time.Millisecond)
	assert.False(t, g.Visible(), "just before ShowAfter: still hidden")

	now = now.Add(2 * time.Millisecond) // crossed 200ms
	assert.True(t, g.Visible(), "after ShowAfter: spinner appears")
}

func TestLoadGate_StoppedSpinnerHoldsForMinVisible(t *testing.T) {
	now := time.Now()
	g := LoadGate{Clock: fakeClock(&now)}
	g.Start()

	now = now.Add(250 * time.Millisecond) // spinner now visible (>200ms)
	assert.True(t, g.Visible())

	g.Stop() // load finishes 50ms after spinner appeared
	now = now.Add(100 * time.Millisecond)
	assert.True(t, g.Visible(), "within MinVisible: spinner held open")

	now = now.Add(250 * time.Millisecond) // total elapsed since show ≈ 350ms > 300ms
	assert.False(t, g.Visible(), "MinVisible elapsed: spinner finally hides")
}

func TestLoadGate_StoppedBeforeShowDoesNotPaint(t *testing.T) {
	now := time.Now()
	g := LoadGate{Clock: fakeClock(&now)}
	g.Start()

	now = now.Add(150 * time.Millisecond)
	g.Stop() // stopped inside ShowAfter — spinner never appeared

	now = now.Add(1 * time.Second)
	assert.False(t, g.Visible(), "stopped before show: no end-flicker either")
}

func TestLoadGate_RestartReanchorsClock(t *testing.T) {
	now := time.Now()
	g := LoadGate{Clock: fakeClock(&now)}
	g.Start()
	now = now.Add(250 * time.Millisecond)
	assert.True(t, g.Visible())

	// User hits refresh mid-load: Start re-anchors so the new load gets
	// its own anti-flicker grace period.
	g.Start()
	assert.False(t, g.Visible(), "re-Start re-suppresses until new ShowAfter elapses")

	now = now.Add(250 * time.Millisecond)
	assert.True(t, g.Visible())
}

func TestLoadGate_ZeroValueUsesDefaults(t *testing.T) {
	var g LoadGate
	assert.Equal(t, DefaultLoadGateShowAfter, g.showAfter())
	assert.Equal(t, DefaultLoadGateMinVisible, g.minVisible())
}

func TestLoadGate_NotStartedNeverVisible(t *testing.T) {
	var g LoadGate
	assert.False(t, g.Visible(), "zero-state gate is dormant")
}

func TestLoadGate_StopWithoutStartNoop(t *testing.T) {
	var g LoadGate
	g.Stop()
	assert.False(t, g.Visible())
}

func TestLoadGate_CustomThresholdsHonored(t *testing.T) {
	now := time.Now()
	g := LoadGate{
		ShowAfter:  50 * time.Millisecond,
		MinVisible: 100 * time.Millisecond,
		Clock:      fakeClock(&now),
	}
	g.Start()
	now = now.Add(40 * time.Millisecond)
	assert.False(t, g.Visible(), "custom 50ms ShowAfter respected (40ms still hidden)")
	now = now.Add(20 * time.Millisecond)
	assert.True(t, g.Visible(), "custom 50ms ShowAfter respected (60ms shown)")
}
