package tui

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain runs goroutine-leak detection across the whole internal/tui
// test suite. The TUI spawns goroutines for refresh tickers, log
// streamers, the chat suggestion debouncer, and background fetch
// contexts — a suite-level guard turns the *next* leak into a build
// failure rather than a process-wide drip the operator only notices
// when their laptop fan spins up.
//
// Known long-lived goroutines (klog's flush daemon, anything started
// before our tests run) are baselined via IgnoreCurrent.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m,
		goleak.IgnoreCurrent(),
		goleak.IgnoreTopFunction("k8s.io/klog/v2.(*flushDaemon).run"),
	)
}
