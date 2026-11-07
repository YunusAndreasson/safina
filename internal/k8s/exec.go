package k8s

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
	utilexec "k8s.io/client-go/util/exec"
)

// ShellEvent is the union type emitted on the session's Events channel. Bytes
// carries a stdout/stderr chunk; Done flips when the stream ends; Err carries
// any failure; NoShell is set when the failure looks like "no shell binary in
// the container" (distroless / scratch images), which we use to offer the
// netshoot debug-pod fall-back.
type ShellEvent struct {
	Bytes   []byte
	Err     error
	Done    bool
	NoShell bool
}

// ShellSession is a live remotecommand exec/attach pipe wired up for an
// embedded terminal modal. Caller writes user input to Stdin and reads
// terminal output from Events. Resize accepts new cell dimensions whenever
// the modal panel changes size.
type ShellSession struct {
	Stdin  *io.PipeWriter
	Events <-chan ShellEvent
	Resize chan<- remotecommand.TerminalSize

	cancel context.CancelFunc
}

// Close terminates the session. Safe to call multiple times.
func (s *ShellSession) Close() {
	if s.cancel != nil {
		s.cancel()
	}
}

// ExecPod opens an interactive shell inside container of pod. Pass cmd == nil
// for the default bash-or-sh idiom (k9s convention) — try bash first because
// it has Ctrl+R and tab completion; fall back to sh when bash isn't there.
// container == "" lets kubelet pick the default (first) container.
//
// The dispatcher uses absolute paths (`/bin/bash`, `/bin/sh`) instead of the
// older `command -v bash && exec bash || exec sh` trick: the trick relies on
// PATH containing the directory where bash/sh actually live, which fails on
// distroless / minimal images with non-standard PATHs and surfaces (via
// WebSocket V5's faithful exit-code propagation) as a confusing "exit 127"
// at session end. Absolute-path probes don't depend on PATH.
func ExecPod(
	ctx context.Context,
	cs kubernetes.Interface,
	restCfg *rest.Config,
	namespace, pod, container string,
	cmd []string,
) (*ShellSession, error) {
	if len(cmd) == 0 {
		// Three-stage probe: prefer bash for the rich line editing, fall
		// back to /bin/sh, and finally PATH-based `sh` for containers with
		// non-standard layouts. The final `exec sh` is the safety net — we
		// know it'll succeed because kubelet already resolved `sh` to start
		// this dispatcher in the first place.
		cmd = []string{"sh", "-c", "[ -x /bin/bash ] && exec /bin/bash; [ -x /bin/sh ] && exec /bin/sh; exec sh"}
	}

	req := cs.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(pod).
		Namespace(namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: container,
			Command:   cmd,
			Stdin:     true,
			Stdout:    true,
			// With TTY=true the kubelet multiplexes stderr onto stdout. Setting
			// Stderr alongside TTY is rejected by the API server, so leave it false.
			Stderr: false,
			TTY:    true,
		}, scheme.ParameterCodec)

	executor, err := newStreamExecutor(restCfg, req.URL())
	if err != nil {
		return nil, fmt.Errorf("create exec executor: %w", err)
	}
	return startSession(ctx, executor), nil
}

// startSession kicks off the executor goroutine and returns a wired session
// handle. Shared between ExecPod and AttachDebugContainer.
func startSession(parent context.Context, executor remotecommand.Executor) *ShellSession {
	stdinR, stdinW := io.Pipe()
	events := make(chan ShellEvent, 64)
	resize := make(chan remotecommand.TerminalSize, 4)

	streamCtx, cancel := context.WithCancel(parent)

	go func() {
		defer close(events)
		defer func() { _ = stdinR.Close() }()

		err := executor.StreamWithContext(streamCtx, remotecommand.StreamOptions{
			Stdin:             stdinR,
			Stdout:            chanWriter{ch: events, ctx: streamCtx},
			Stderr:            nil,
			Tty:               true,
			TerminalSizeQueue: &sizeQueue{ch: resize, ctx: streamCtx},
		})

		// WebSocket V5 reliably propagates the in-pod shell's exit code as
		// an ExitError (SPDY usually masked it). A non-zero exit from a
		// successfully-running shell isn't a connection failure — it's just
		// "the user typed `exit` after a failed command." Treat any
		// ExitError as a clean session end so the modal closes cleanly
		// instead of flashing "connection failed · exit 127".
		var exitErr utilexec.ExitError
		if errors.As(err, &exitErr) {
			err = nil
		}

		ev := ShellEvent{Done: true, Err: err}
		if err != nil {
			ev.NoShell = looksLikeNoShell(err.Error())
		}
		// Best-effort: if the consumer has already detached we don't care.
		select {
		case events <- ev:
		case <-streamCtx.Done():
		}
	}()

	return &ShellSession{
		Stdin:  stdinW,
		Events: events,
		Resize: resize,
		cancel: cancel,
	}
}

// looksLikeNoShell pattern-matches the kubelet/runtime error when the exec
// command can't be found. Different runtimes produce slightly different
// strings; we accept the union.
func looksLikeNoShell(msg string) bool {
	m := strings.ToLower(msg)
	return strings.Contains(m, "executable file not found") ||
		strings.Contains(m, "no such file or directory") ||
		strings.Contains(m, "oci runtime exec failed")
}

// chanWriter adapts an events channel to io.Writer for remotecommand stdout.
// We copy each chunk because the executor reuses its read buffer.
type chanWriter struct {
	ch  chan<- ShellEvent
	ctx context.Context //nolint:containedctx // Adapts cancellation to an interface whose methods cannot accept a context.
}

func (w chanWriter) Write(p []byte) (int, error) {
	cp := make([]byte, len(p))
	copy(cp, p)
	select {
	case w.ch <- ShellEvent{Bytes: cp}:
		return len(p), nil
	case <-w.ctx.Done():
		return 0, w.ctx.Err()
	}
}

// sizeQueue feeds remotecommand's TerminalSizeQueue from a resize channel.
// Returning nil signals the goroutine to exit. We watch ctx.Done() too so
// the queue terminates when the session ends — otherwise Next() would stay
// parked on the resize chan forever and leak a goroutine per session.
type sizeQueue struct {
	ch  <-chan remotecommand.TerminalSize
	ctx context.Context //nolint:containedctx // Adapts cancellation to an interface whose methods cannot accept a context.
}

func (q *sizeQueue) Next() *remotecommand.TerminalSize {
	select {
	case sz, ok := <-q.ch:
		if !ok {
			return nil
		}
		return &sz
	case <-q.ctx.Done():
		return nil
	}
}

// randSuffix returns a short hex string for naming ephemeral containers.
func randSuffix(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)[:n]
}
