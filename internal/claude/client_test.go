package claude

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"testing/iotest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// collect drains evs into a slice until close or timeout. Used by tests
// that don't need to interleave assertions with event production.
func collect(t *testing.T, evs <-chan Event, timeout time.Duration) []Event {
	t.Helper()
	out := []Event{}
	deadline := time.After(timeout)
	for {
		select {
		case e, ok := <-evs:
			if !ok {
				return out
			}
			out = append(out, e)
		case <-deadline:
			t.Fatalf("timeout after %s waiting for events; got %d", timeout, len(out))
			return out
		}
	}
}

func runDecode(t *testing.T, ctx context.Context, raw string, resume string) (events []Event, sessionID string, err error) {
	t.Helper()
	out := make(chan Event, 64)
	var (
		wg     sync.WaitGroup
		gotID  string
		gotErr error
	)
	wg.Add(1)
	go func() {
		defer close(out)
		defer wg.Done()
		var ok bool
		gotID, gotErr, ok = decodeStream(ctx, strings.NewReader(raw), out, resume)
		_ = ok
	}()
	for e := range out {
		events = append(events, e)
	}
	wg.Wait()
	return events, gotID, gotErr
}

func TestDecode_InitTokenResult(t *testing.T) {
	raw := strings.Join([]string{
		`{"type":"system","subtype":"init","session_id":"sess-1"}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"hello "}}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"world"}}}`,
		`{"type":"result","subtype":"success","session_id":"sess-1","result":"hello world"}`,
	}, "\n")
	events, sid, err := runDecode(t, context.Background(), raw, "")
	require.NoError(t, err)
	require.Equal(t, "sess-1", sid)
	require.Len(t, events, 3)
	assert.Equal(t, KindInit, events[0].Kind)
	assert.Equal(t, "sess-1", events[0].SessionID)
	assert.Equal(t, KindToken, events[1].Kind)
	assert.Equal(t, "hello ", events[1].Delta)
	assert.Equal(t, "world", events[2].Delta)
	// Concatenating Tokens reconstructs the assistant turn — documented
	// invariant in events.go.
	got := events[1].Delta + events[2].Delta
	assert.Equal(t, "hello world", got)
}

func TestDecode_ErrorResultIsNotAssistantText(t *testing.T) {
	for _, raw := range []string{
		`{"type":"result","is_error":true,"result":"usage limit reached"}`,
		`{"type":"result","subtype":"error_max_turns"}`,
		`{"type":"result","is_error":true}`,
	} {
		events, _, err := runDecode(t, context.Background(), raw, "")
		require.Error(t, err)
		require.Empty(t, events)
	}
}

func TestService_ResetSessionCancelsActiveTurn(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	s := &Service{cancel: cancel, sessionID: "old", turnID: 7}
	s.ResetSession()
	require.ErrorIs(t, ctx.Err(), context.Canceled)
	require.Empty(t, s.SessionID())
	require.Equal(t, uint64(8), s.turnID)
}

func TestServiceReapsProcess(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("process cleanup assertion uses /proc")
	}
	for _, mode := range []string{"cancel", "oversized output"} {
		t.Run(mode, func(t *testing.T) {
			command := "exec sleep 60"
			if mode == "oversized output" {
				command = "exec head -c 5000000 /dev/zero"
			}
			path := filepath.Join(t.TempDir(), "claude")
			script := "#!/bin/sh\nprintf '{\"type\":\"system\",\"subtype\":\"init\",\"session_id\":\"%s\"}\\n' \"$$\"\n" + command + "\n"
			require.NoError(t, os.WriteFile(path, []byte(script), 0o700)) // #nosec G306 -- The subprocess fixture must be executable; only its owner has access.
			s := &Service{bin: path}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			out := s.Send(ctx, "test")
			var init Event
			select {
			case init = <-out:
			case <-ctx.Done():
				t.Fatal("subprocess did not initialize")
			}
			require.Equal(t, KindInit, init.Kind)
			if mode == "cancel" {
				s.Cancel()
			}
			events := collect(t, out, 5*time.Second)
			if mode == "oversized output" {
				require.Len(t, events, 1)
				require.Error(t, events[0].Err)
			}
			_, err := os.Stat(filepath.Join("/proc", init.SessionID))
			require.True(t, os.IsNotExist(err), "child must be reaped before its event channel closes")
		})
	}
}

func TestDecode_NonStreamingResultFallsBackToResultString(t *testing.T) {
	// Some turns (cached / short replies) emit no stream_event deltas. The
	// parser must surface result.result as a Token so the user still sees
	// text — otherwise short answers would render blank.
	raw := `{"type":"result","subtype":"success","session_id":"abc","result":"42"}` + "\n"
	events, sid, err := runDecode(t, context.Background(), raw, "")
	require.NoError(t, err)
	require.Equal(t, "abc", sid)
	require.Len(t, events, 1)
	assert.Equal(t, KindToken, events[0].Kind)
	assert.Equal(t, "42", events[0].Delta)
}

func TestDecode_RetryEvent(t *testing.T) {
	raw := `{"type":"system","subtype":"api_retry","attempt":2,"max_retries":5,"retry_delay_ms":1200,"error":"rate_limit"}` + "\n"
	events, _, err := runDecode(t, context.Background(), raw, "")
	require.NoError(t, err)
	require.Len(t, events, 1)
	got := events[0]
	assert.Equal(t, KindRetry, got.Kind)
	assert.Equal(t, 2, got.Attempt)
	assert.Equal(t, 5, got.MaxRetries)
	assert.Equal(t, 1200, got.DelayMS)
	assert.Equal(t, "rate_limit", got.Reason)
}

func TestDecode_MalformedLinesSkippedNotFatal(t *testing.T) {
	// A garbage line between two valid ones must not abort the turn —
	// claude-code's stream may include diagnostic noise on stderr/stdout.
	raw := strings.Join([]string{
		`{"type":"system","subtype":"init","session_id":"s"}`,
		`{this is not json}`,
		`malformed`,
		`{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"ok"}}}`,
	}, "\n")
	events, sid, err := runDecode(t, context.Background(), raw, "")
	require.NoError(t, err)
	assert.Equal(t, "s", sid)
	require.Len(t, events, 2, "init + token; garbage lines dropped silently")
	assert.Equal(t, KindInit, events[0].Kind)
	assert.Equal(t, KindToken, events[1].Kind)
	assert.Equal(t, "ok", events[1].Delta)
}

func TestDecode_EmptyTextDeltaSuppressed(t *testing.T) {
	// "" deltas would emit zero-content tokens that the UI then renders as
	// no-op flicker. Guard: empty text_delta must not produce an Event.
	raw := strings.Join([]string{
		`{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":""}}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"x"}}}`,
	}, "\n")
	events, _, err := runDecode(t, context.Background(), raw, "")
	require.NoError(t, err)
	require.Len(t, events, 1, "empty text_delta must not emit a Token")
	assert.Equal(t, "x", events[0].Delta)
}

func TestDecode_NonTextDeltaIgnored(t *testing.T) {
	// content_block_delta with a non-text delta (e.g. input_json_delta from
	// tool use) must not be forwarded as a text Token — we'd render JSON
	// fragments in the chat panel.
	raw := strings.Join([]string{
		`{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"input_json_delta","text":"{}"}}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"hi"}}}`,
	}, "\n")
	events, _, err := runDecode(t, context.Background(), raw, "")
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "hi", events[0].Delta)
}

func TestDecode_BlankLinesIgnored(t *testing.T) {
	// Buffered subprocess output may inject "\n\n" between records.
	raw := "\n\n" +
		`{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"a"}}}` +
		"\n\n\n"
	events, _, err := runDecode(t, context.Background(), raw, "")
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "a", events[0].Delta)
}

func TestDecode_ResumeSessionIDPersistsWhenNoInit(t *testing.T) {
	// If the upstream `claude --resume <id>` emits no init (resumed
	// session), the caller's resume id must still flow into the return so
	// the next turn keeps using it. Regression: forgetting `sessionID =
	// resume` produces "" → the next turn starts a fresh conversation.
	raw := `{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"hi"}}}` + "\n"
	events, sid, err := runDecode(t, context.Background(), raw, "resume-me")
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "resume-me", sid, "resume id must be preserved when no result carries a new one")
}

func TestDecode_InitWithoutSessionIDDoesNotEmit(t *testing.T) {
	// init without a session_id is meaningless — we shouldn't emit an
	// Init event with empty id (the TUI uses SessionID != "" to mark the
	// chat as "alive").
	raw := `{"type":"system","subtype":"init"}` + "\n"
	events, _, err := runDecode(t, context.Background(), raw, "")
	require.NoError(t, err)
	assert.Empty(t, events, "init event without session_id must be suppressed")
}

func TestDecode_LongLineRespectsBufferLimit(t *testing.T) {
	// stream-json lines can carry a full assistant message. We bump the
	// scanner buffer past bufio's 64KB default. Use a 1 MiB delta to assert
	// the bump took effect — if we removed it, this would either return
	// scanner.ErrTooLong or truncate.
	big := strings.Repeat("a", 1<<20) // 1 MiB
	raw := `{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"` + big + `"}}}` + "\n"
	events, _, err := runDecode(t, context.Background(), raw, "")
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, big, events[0].Delta)
}

// slowReader returns one line per Read call, then blocks until ctx done.
// Used to exercise the cancellation path inside decodeStream.
type slowReader struct {
	chunks   [][]byte
	cursor   int
	released chan struct{}
}

func (r *slowReader) Read(p []byte) (int, error) {
	if r.cursor < len(r.chunks) {
		n := copy(p, r.chunks[r.cursor])
		r.cursor++
		return n, nil
	}
	<-r.released
	return 0, io.EOF
}

func TestDecode_ContextCancellationStopsEmission(t *testing.T) {
	// If the consumer drops mid-stream (user pressed esc), ctx is cancelled.
	// decodeStream must return ok=false from the next emit attempt and stop
	// touching `out`.
	ctx, cancel := context.WithCancel(context.Background())
	out := make(chan Event) // unbuffered — emit blocks until we read
	released := make(chan struct{})
	r := &slowReader{
		chunks: [][]byte{
			[]byte(`{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"a"}}}` + "\n"),
		},
		released: released,
	}

	type res struct {
		sid string
		err error
		ok  bool
	}
	done := make(chan res, 1)
	go func() {
		sid, err, ok := decodeStream(ctx, r, out, "resume-id")
		done <- res{sid, err, ok}
	}()

	// Read one event so decodeStream proceeds, then cancel before next emit.
	ev := <-out
	assert.Equal(t, "a", ev.Delta)
	cancel()
	// Allow the reader to wake (it would block on released otherwise).
	close(released)

	select {
	case r := <-done:
		// ok must be true here because the channel was already drained; cancel
		// signaled mid-iteration but no further emits happened. The important
		// invariant is decodeStream RETURNS rather than blocking forever.
		_ = r
	case <-time.After(2 * time.Second):
		t.Fatal("decodeStream did not return after context cancel")
	}
}

// ── Service-level tests ──────────────────────────────────────────────

func TestService_NotAvailable_EmitsInstallError(t *testing.T) {
	// When the claude binary isn't on PATH, Send must still return a closed
	// channel that surfaces one Done event with an instructional error —
	// the TUI uses this to render an "install / sign in" card. Critical:
	// the channel MUST be closed, or the chat UI hangs spinning forever.
	s := &Service{bin: ""}
	assert.False(t, s.Available())

	out := s.Send(context.Background(), "hi")
	events := collect(t, out, 2*time.Second)
	require.Len(t, events, 1)
	assert.Equal(t, KindDone, events[0].Kind)
	require.Error(t, events[0].Err)
	assert.Contains(t, events[0].Err.Error(), "claude CLI not found",
		"error must guide the user to install claude")
}

func TestService_ResetSessionDropsResumeID(t *testing.T) {
	s := &Service{bin: ""}
	// Simulate a successful turn that set the session id.
	s.mu.Lock()
	s.sessionID = "abc-123"
	s.mu.Unlock()

	assert.Equal(t, "abc-123", s.SessionID())
	s.ResetSession()
	assert.Empty(t, s.SessionID(),
		"ResetSession must clear so the next turn doesn't resume the old conversation")
}

func TestService_Cancel_IsIdempotentWhenIdle(t *testing.T) {
	// User pressing Ctrl+C while no turn is running must be a no-op rather
	// than panic. The chat panel wires Cancel() to a key handler that fires
	// regardless of whether anything is in flight.
	s := &Service{bin: ""}
	require.NotPanics(t, func() {
		s.Cancel()
		s.Cancel()
	})
}

func TestDecode_ResultCarriesUpdatedSessionID(t *testing.T) {
	// When --resume is used but the result event reports a different
	// session_id (server-side resume rejection / fork), we must persist
	// the NEW id so subsequent turns continue the actually-active session.
	raw := strings.Join([]string{
		`{"type":"system","subtype":"init","session_id":"orig"}`,
		`{"type":"result","subtype":"success","session_id":"forked","result":"x"}`,
	}, "\n")
	_, sid, err := runDecode(t, context.Background(), raw, "")
	require.NoError(t, err)
	assert.Equal(t, "forked", sid,
		"a result session_id must overwrite the init one when they differ")
}

func TestDecode_NonEOFScannerErrorReturned(t *testing.T) {
	// io.EOF is normal — decodeStream returns nil. Any other error must
	// surface so the caller can wrap it as "claude read: …" for the user.
	r := iotest.ErrReader(errors.New("disk on fire"))
	out := make(chan Event, 4)
	_, scanErr, _ := decodeStream(context.Background(), r, out, "")
	require.Error(t, scanErr)
	assert.Contains(t, scanErr.Error(), "disk on fire")
}

func TestStderrTailBoundsMemoryAndPreservesLastBytes(t *testing.T) {
	for _, chunks := range [][]string{
		{strings.Repeat("x", maxStderrBytes*2), "failure"},
		{strings.Repeat("x", maxStderrBytes-3), "failure"},
		{"first", strings.Repeat("z", maxStderrBytes*2)},
		{"short", " message"},
	} {
		var buf stderrTail
		for _, chunk := range chunks {
			n, err := buf.Write([]byte(chunk))
			require.NoError(t, err)
			assert.Equal(t, len(chunk), n)
			assert.LessOrEqual(t, len(buf.data), maxStderrBytes)
		}
		want := strings.Join(chunks, "")
		if len(want) > maxStderrBytes {
			want = want[len(want)-maxStderrBytes:]
		}
		assert.Equal(t, want, buf.String())
	}
}
