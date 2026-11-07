// Package errlog is safina's always-on error sink.
//
// Two streams land here:
//
//  1. klog output from k8s.io/client-go (request.go:1196 "Unexpected error
//     when reading response body err=context canceled" and friends). klog
//     defaults to stderr, which Bubble Tea's alt-screen cannot mask — the
//     lines paint over the rendered frame. We route klog through slog into
//     this file and neutralize its legacy stderr path, so the TUI stays
//     clean and the noise stays inspectable.
//
//  2. Mirrored debug.Errf events from safina itself, so the human-readable
//     error log captures both client-go noise and our own structured errors
//     in one place a user (or agent) can grep without correlating files.
//
// Always on, regardless of SAFINA_DEBUG: the point is post-mortem evaluation.
// Override the path with SAFINA_ERROR_FILE; default is ~/.safina/errors.log.
//
// Implementation note: in klog v2 neither LogToStderr(false) nor SetOutput
// alone is sufficient — the former still lets per-severity writes reach fd 2,
// and the latter triple-writes Info/Warning/Error copies to whatever you
// hand it. Routing through SetSlogLogger is the only path that captures
// each event exactly once and keeps stderr clean.
package errlog

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"k8s.io/klog/v2"
)

var (
	mu     sync.Mutex
	sink   *os.File
	logger *slog.Logger
	path   string
)

// Init opens the error log and routes klog into it. Safe to call once from
// main; later calls are no-ops. Returns the resolved path, or "" if disabled
// (no home dir, permission error, etc. — degrade silently rather than crash
// before the TUI even starts).
func Init() string {
	mu.Lock()
	defer mu.Unlock()
	if sink != nil {
		return path
	}
	if p := os.Getenv("SAFINA_ERROR_FILE"); p != "" {
		path = p
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		path = filepath.Join(home, ".safina", "errors.log")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		path = ""
		return ""
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) // #nosec G304 -- The log path is chosen by the local operator, not remote cluster input.
	if err != nil {
		path = ""
		return ""
	}
	sink = f
	logger = slog.New(slog.NewTextHandler(f, &slog.HandlerOptions{Level: slog.LevelInfo}))

	// Three-step muzzle. SetSlogLogger swallows klog events into our slog
	// handler (one copy per event). LogToStderr(false) closes the legacy
	// stderr path. SetOutput(io.Discard) catches any residual writes that
	// don't go through the slog path (older code paths in client-go).
	klog.SetSlogLogger(logger)
	klog.LogToStderr(false)
	klog.SetOutput(io.Discard)

	_, _ = fmt.Fprintf(f, "\n==== safina session start %s pid=%d ====\n",
		time.Now().Format(time.RFC3339), os.Getpid())
	return path
}

// Close flushes klog's buffer and releases the file. Safe to call when
// Init was never run or already closed.
func Close() {
	mu.Lock()
	defer mu.Unlock()
	if sink == nil {
		return
	}
	klog.Flush()
	// Restore stderr routing so any post-TUI klog calls (rare, but the
	// process may still be alive briefly) don't disappear into a closed
	// file descriptor.
	klog.LogToStderr(true)
	_ = sink.Close()
	sink = nil
	logger = nil
}

// Writef appends a "source: message" event to the same file klog is using.
// Called by debug.Errf so safina's own error events sit alongside client-go's.
func Writef(source, format string, args ...any) {
	mu.Lock()
	l := logger
	mu.Unlock()
	if l == nil {
		return
	}
	l.Error(source, "msg", fmt.Sprintf(format, args...), "origin", "safina")
}

// Path returns the active error log path, or "" if disabled.
func Path() string {
	mu.Lock()
	defer mu.Unlock()
	return path
}
