// Package claude is safina's bridge to Anthropic's Claude via the user's
// logged-in `claude` CLI subscription. We deliberately avoid the API-key
// route (Go SDKs all require ANTHROPIC_API_KEY) so the chat panel costs the
// user nothing extra beyond their Pro/Max plan.
//
// One Service is shared for the lifetime of a safina session. Each user turn
// spawns a fresh `claude -p` subprocess with --output-format stream-json;
// stdout is decoded line-by-line into typed Events on a channel that the TUI
// consumes via a Bubble Tea command. SessionID is captured from the first
// turn's `system/init` event and threaded back as `--resume` so subsequent
// turns continue the same conversation.
package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
)

// systemPrompt is appended (not replaced) so the user's local CLAUDE.md and
// global skills/hooks still load. Tone is calibrated for a narrow terminal
// panel — terse, no preamble, no "I'd be happy to help".
//
// Critically, we tell the model it's running *inside* safina (not next to
// it). That changes its advice mode: instead of suggesting kubectl
// invocations the user would have to copy out of a TUI scrollback (painful
// and error-prone), it should point at safina's hotkeys, which the user can
// hit immediately on the row they're already looking at.
const systemPrompt = `You are safina's k8s chat — running INSIDE the safina TUI, not next to it.

The user is browsing a live Kubernetes cluster in safina's terminal interface. The [Selection] block prepended to each message tells you what they're looking at — never ask "which pod" or "which cluster", it's already there. The breadcrumb at the top of their screen shows the same path.

Style:
- terse. Narrow terminal panel (often <60 chars wide). No preamble, no closing pleasantries.
- markdown is rendered: use **bold**, bullet lists, and triple-backtick fenced code for YAML/JSON snippets.
- when [Selection] says the resource is healthy, say so in one line — don't manufacture concerns.
- never invent fields, names, or values that aren't in [Selection].

DO NOT suggest kubectl, k9s, or any external command. The user is inside safina; copy-pasting from a TUI scrollback is painful. Instead point at safina's hotkeys, which act on the row they already have selected:

Convention: lowercase keys are read-only / safe; uppercase keys mutate cluster state.

- d        describe (full YAML)
- l        stream logs
- e        events for the selected resource
- s        shell into pod
- v        reveal a Secret's decoded data
- f        port-forward (Pod or Service)
- n        jump to next failing row
- r        refresh now (auto-refresh runs every 5s)
- /        filter the current list
- i / tab  jump to this chat
- S        scale (Deployment / StatefulSet)
- U        undo (Deployment rollback)
- R        restart (rollout restart)
- E        edit YAML in $EDITOR
- C        cordon / uncordon a Node
- D        drain a Node
- K        kill (delete the resource)
- ↵        drill in   ·   esc   drill out / back
- ?        full keymap

When the right next step is one of these, say it inline: "press l" or "hit d for the full manifest", not "run kubectl logs ...".

If the user genuinely needs something safina can't do (edit a manifest, check RBAC across the cluster, look at metrics in another tool), say so in one line — then suggest the safina view that gets them closest.`

// Service is the long-lived chat backend. It is goroutine-safe; concurrent
// Send calls cancel the in-flight subprocess and start a new one (the chat UI
// only ever has one open turn at a time, but the cancel path is what makes
// "user hits send again mid-stream" behave sanely).
type Service struct {
	bin string // path to `claude`; "" means unavailable

	mu        sync.Mutex
	cancel    context.CancelFunc // cancels the in-flight turn, if any
	sessionID string             // set after the first successful turn
}

// NewService probes for the `claude` binary. When absent, the returned
// Service still works — Available() reports false and Send returns an
// instructional error so the UI can render an install card.
func NewService() *Service {
	bin, _ := exec.LookPath("claude")
	return &Service{bin: bin}
}

// Available reports whether the `claude` binary was found on PATH at startup.
// The TUI uses this to swap in an "install / sign in" card when false.
func (s *Service) Available() bool { return s.bin != "" }

// SessionID returns the current conversation id (empty until the first turn
// completes). Persisted across turns so the user's chat history stays
// coherent without us having to feed it back in manually.
func (s *Service) SessionID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessionID
}

// ResetSession drops the conversation id so the next Send starts fresh.
// Wired to the user's Ctrl+N (new session) keybinding.
func (s *Service) ResetSession() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessionID = ""
}

// Send launches a new turn. It returns a receive-only channel that emits one
// or more Events, ending in exactly one KindDone (with Err set on failure).
// The channel is closed after Done.
//
// Cancellation: if a previous Send is still running, it is cancelled before
// the new one starts. The parent ctx is also honored — when ctx is done, the
// subprocess is killed.
func (s *Service) Send(ctx context.Context, prompt string) <-chan Event {
	out := make(chan Event, 32)

	if s.bin == "" {
		go func() {
			out <- Event{Kind: KindDone, Err: errors.New(
				"claude CLI not found on PATH — install with `npm i -g @anthropic-ai/claude-code` and run `claude` to sign in",
			)}
			close(out)
		}()
		return out
	}

	s.mu.Lock()
	if s.cancel != nil {
		s.cancel() // pre-empt any in-flight turn
	}
	turnCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	resume := s.sessionID
	s.mu.Unlock()

	go s.run(turnCtx, prompt, resume, out)
	return out
}

// run is the goroutine body of one turn. It owns the subprocess lifecycle —
// stdout decoding, stderr capture for error reporting, and the final Done
// event regardless of outcome.
func (s *Service) run(ctx context.Context, prompt, resume string, out chan<- Event) {
	defer close(out)

	args := []string{
		"-p", prompt,
		"--output-format", "stream-json",
		"--verbose",
		"--include-partial-messages",
		"--append-system-prompt", systemPrompt,
	}
	if resume != "" {
		args = append(args, "--resume", resume)
	}

	cmd := exec.CommandContext(ctx, s.bin, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		out <- Event{Kind: KindDone, Err: fmt.Errorf("claude stdout: %w", err)}
		return
	}
	// Capture stderr in memory so we can surface it on a non-zero exit.
	// `claude -p` prints auth errors and similar there.
	var stderrBuf strings.Builder
	cmd.Stderr = &stderrBuf

	if err := cmd.Start(); err != nil {
		out <- Event{Kind: KindDone, Err: fmt.Errorf("claude start: %w", err)}
		return
	}

	sessionID := resume
	scanner := bufio.NewScanner(stdout)
	// stream-json lines can carry a full assistant message — bump the line cap
	// well past bufio's 64KB default so we don't truncate long replies.
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)

	// emit sends an event but bails on ctx cancellation so we never block
	// forever on a full out channel after the consumer has dropped (e.g.,
	// the user pressed esc and walked away mid-stream). Returns false if
	// the caller should stop producing.
	emit := func(ev Event) bool {
		select {
		case out <- ev:
			return true
		case <-ctx.Done():
			return false
		}
	}

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var raw rawEvent
		if err := json.Unmarshal(line, &raw); err != nil {
			continue // skip malformed lines rather than abort the turn
		}
		switch raw.Type {
		case "system":
			switch raw.Subtype {
			case "init":
				if raw.SessionID != "" {
					sessionID = raw.SessionID
					if !emit(Event{Kind: KindInit, SessionID: raw.SessionID}) {
						return
					}
				}
			case "api_retry":
				if !emit(Event{
					Kind:       KindRetry,
					Attempt:    raw.Attempt,
					MaxRetries: raw.MaxRetries,
					DelayMS:    raw.RetryDelayMS,
					Reason:     raw.Error,
				}) {
					return
				}
			}
		case "stream_event":
			if raw.Event != nil && raw.Event.Delta != nil &&
				raw.Event.Delta.Type == "text_delta" && raw.Event.Delta.Text != "" {
				if !emit(Event{Kind: KindToken, Delta: raw.Event.Delta.Text}) {
					return
				}
			}
		case "result":
			if raw.SessionID != "" {
				sessionID = raw.SessionID
			}
			// Some turns produce no stream_event deltas (cached/short replies):
			// fall back to the result.result string so the user still sees text.
			if raw.Result != "" {
				if !emit(Event{Kind: KindToken, Delta: raw.Result}) {
					return
				}
			}
		}
	}
	scanErr := scanner.Err()
	if scanErr != nil && !errors.Is(scanErr, io.EOF) {
		emit(Event{Kind: KindDone, Err: fmt.Errorf("claude read: %w", scanErr)})
		return
	}

	waitErr := cmd.Wait()
	// Persist the session id even on error — the next turn can still resume.
	if sessionID != "" {
		s.mu.Lock()
		s.sessionID = sessionID
		s.mu.Unlock()
	}

	if waitErr != nil && ctx.Err() == nil {
		// Real failure (not our cancel). Surface stderr if present — it usually
		// holds the actionable detail (auth, rate-limit, etc.).
		msg := strings.TrimSpace(stderrBuf.String())
		if msg == "" {
			msg = waitErr.Error()
		}
		emit(Event{Kind: KindDone, SessionID: sessionID, Err: errors.New(msg)})
		return
	}
	emit(Event{Kind: KindDone, SessionID: sessionID})
}

// Cancel pre-empts any in-flight turn. Safe to call when nothing is running.
func (s *Service) Cancel() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
}
