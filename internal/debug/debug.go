// Package debug provides an opt-in structured log sink for safina.
//
// Activation: set SAFINA_DEBUG=1 (or any non-empty value). When unset, every
// helper here is a no-op and no file is opened.
//
// Output: JSON lines to ~/.safina/debug.log (override with SAFINA_DEBUG_FILE).
// Stdout/stderr are off-limits — Bubble Tea owns the terminal and any stray
// write would corrupt the rendered frame. We only log to a file.
//
// The intent is twofold:
//  1. give a human something to `tail -f` while driving the TUI;
//  2. give an agent (Claude) a machine-readable trail to correlate with
//     screen captures or test failures.
package debug

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/yunus/safina/internal/errlog"
)

var (
	mu      sync.Mutex
	logger  *slog.Logger
	sink    io.Closer
	enabled bool
)

// Init opens the debug log if SAFINA_DEBUG is set. Safe to call exactly once
// from main; subsequent calls are no-ops. Returns the resolved log path (or
// "" when disabled) so the caller can surface it for discoverability.
func Init() string {
	mu.Lock()
	defer mu.Unlock()
	if logger != nil {
		return ""
	}
	if os.Getenv("SAFINA_DEBUG") == "" {
		logger = slog.New(slog.DiscardHandler)
		return ""
	}
	path := os.Getenv("SAFINA_DEBUG_FILE")
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			// No home → fall back to stderr-equivalent silent disable rather
			// than corrupting the TUI by writing to a real fd.
			logger = slog.New(slog.DiscardHandler)
			return ""
		}
		path = filepath.Join(home, ".safina", "debug.log")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		logger = slog.New(slog.DiscardHandler)
		return ""
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		logger = slog.New(slog.DiscardHandler)
		return ""
	}
	sink = f
	logger = slog.New(slog.NewJSONHandler(f, &slog.HandlerOptions{Level: slog.LevelDebug}))
	enabled = true
	logger.Info("debug.start", "pid", os.Getpid(), "path", path)
	return path
}

// Close flushes and releases the log file. Safe to call when disabled.
func Close() {
	mu.Lock()
	defer mu.Unlock()
	if sink != nil {
		_ = sink.Close()
		sink = nil
	}
	logger = nil
	enabled = false
}

// Enabled reports whether the sink is live. Useful to skip expensive payload
// construction when nobody is listening.
func Enabled() bool {
	mu.Lock()
	defer mu.Unlock()
	return enabled
}

func log() *slog.Logger {
	mu.Lock()
	l := logger
	mu.Unlock()
	if l == nil {
		// Init was never called; behave as a no-op rather than crash.
		return slog.New(slog.DiscardHandler)
	}
	return l
}

// Log emits an event. Pairs are slog-style key/value attributes.
func Log(event string, kv ...any) { log().Info(event, kv...) }

// Errf emits an event at error level. Same key/value calling convention.
// Errors are also mirrored to the always-on errlog so they're grep-able
// alongside klog noise from client-go, even when SAFINA_DEBUG is unset.
func Errf(event string, kv ...any) {
	log().Error(event, kv...)
	errlog.Writef(event, "%s", flattenKV(kv))
}

// flattenKV renders slog-style key/value pairs as "k=v k=v" for the
// single-line errlog format. Odd trailing keys are emitted as bare "k".
func flattenKV(kv []any) string {
	if len(kv) == 0 {
		return ""
	}
	var b strings.Builder
	for i := 0; i < len(kv); i += 2 {
		if i > 0 {
			b.WriteByte(' ')
		}
		fmt.Fprintf(&b, "%v", kv[i])
		if i+1 < len(kv) {
			fmt.Fprintf(&b, "=%v", kv[i+1])
		}
	}
	return b.String()
}

// Track times an operation and emits {event, dur_ms, err?}. Use as:
//
//	defer debug.Track("k8s.list_pods", "namespace", ns)()
//
// The returned closure must be called exactly once. err is captured by
// reference via the optional *error pointer in kv (key "err"); pass it as
// the LAST pair if you want the duration log to include the failure.
func Track(event string, kv ...any) func() {
	if !Enabled() {
		return func() {}
	}
	start := time.Now()
	return func() {
		all := append([]any{"dur_ms", time.Since(start).Milliseconds()}, kv...)
		log().Info(event, all...)
	}
}
