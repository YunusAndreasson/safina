package claude

// Event is the typed envelope our parser emits onto the consumer channel.
// One subprocess invocation produces a stream: an Init at the start, zero or
// more Tokens (and optionally Retries), then one terminal Done event.
//
// We deliberately decode only the fields safina cares about — the upstream
// stream-json schema is wider and evolves between Claude Code versions, so
// pinning every field would be brittle.
type Event struct {
	Kind EventKind

	// Init / Done — the (possibly resumed) conversation id. The first Init
	// of a session populates this; Done re-confirms it so callers can persist.
	SessionID string

	// Token — incremental assistant text. Concatenate Tokens to reconstruct
	// the assistant turn.
	Delta string

	// Retry — surfaced from `system/api_retry` events.
	Attempt    int
	MaxRetries int
	DelayMS    int
	Reason     string

	// Done — non-nil when the subprocess failed to produce a clean result.
	Err error
}

type EventKind int

const (
	KindInit EventKind = iota
	KindToken
	KindRetry
	KindDone
)

// rawEvent is a permissive decoder for one stream-json line. Anything we don't
// care about gets dropped. Pointer fields let us tell "absent" from "zero".
type rawEvent struct {
	Type      string `json:"type"`
	Subtype   string `json:"subtype,omitempty"`
	SessionID string `json:"session_id,omitempty"`

	// Stream events: {"type":"stream_event","event":{"type":"content_block_delta",
	//                  "delta":{"type":"text_delta","text":"..."}}}
	Event *struct {
		Type  string `json:"type"`
		Delta *struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"delta,omitempty"`
	} `json:"event,omitempty"`

	// Result events: {"type":"result","subtype":"success|error_*",
	//                 "is_error":bool,"result":"<final assistant text>"}
	IsError bool   `json:"is_error,omitempty"`
	Result  string `json:"result,omitempty"`

	// Retry events: {"type":"system","subtype":"api_retry","attempt":1,
	//                "max_retries":5,"retry_delay_ms":1200,"error":"rate_limit"}
	Attempt      int    `json:"attempt,omitempty"`
	MaxRetries   int    `json:"max_retries,omitempty"`
	RetryDelayMS int    `json:"retry_delay_ms,omitempty"`
	Error        string `json:"error,omitempty"`
}
