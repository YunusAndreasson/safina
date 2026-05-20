package k8s

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain runs goroutine-leak detection across the whole internal/k8s
// test suite. safina spawns goroutines aggressively on the hot path —
// shared informers, log streamers, port-forwards, exec sessions — so a
// suite-level guard catches the *next* leak automatically instead of
// requiring another bespoke logs_leak_test.go.
//
// Known long-lived goroutines (klog's flush daemon, the cgo timer thread
// libraries register at init) are baselined via IgnoreCurrent before the
// tests run; subsequent goroutines that don't exit by suite end are real
// findings.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m,
		goleak.IgnoreCurrent(),
		// klog flushes log buffers on a background timer; it starts via
		// the first klog.V call and never stops within process lifetime.
		goleak.IgnoreTopFunction("k8s.io/klog/v2.(*flushDaemon).run"),
	)
}
