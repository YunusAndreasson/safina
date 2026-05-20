package sections

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain runs goroutine-leak detection across internal/tui/sections.
// Sections own per-section background contexts (log streamers, event
// fetchers, describe loaders); this guard ensures Cancel/Cleanup wires
// stay correct as new sections are added.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m,
		goleak.IgnoreCurrent(),
		goleak.IgnoreTopFunction("k8s.io/klog/v2.(*flushDaemon).run"),
	)
}
