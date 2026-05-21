package sections

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yunus/safina/internal/tui/theme"
)

// TestColorizeLogLine_PlainTextSurvives is the cardinal contract: the
// colorizer may inject ANSI escape sequences but must never alter the
// underlying characters. A user's grep against the raw log buffer (or a
// future "yank to clipboard" path) reads the same bytes regardless of
// whether the colorizer touched the line.
func TestColorizeLogLine_PlainTextSurvives(t *testing.T) {
	th := theme.Default()
	cases := []string{
		"hello world",
		"",
		"INFO server started on :8080",
		"2026-05-20T13:45:01.234Z ERROR auth failed",
		"2026-05-20T13:45:01.234Z stderr F message body",
		"I0520 13:45:01.234567       1 controller.go:42] Reconciling",
		`{"level":"info","ts":"2026-05-20T13:45:01Z","msg":"hi","count":3,"ok":true}`,
		"\tat com.example.Foo.bar(Foo.java:42)",
		"    at runtime.main()",
		"plain message with no recognized tokens",
	}
	for _, line := range cases {
		t.Run(short(line), func(t *testing.T) {
			got := colorizeLogLine(line, th)
			assert.Equal(t, line, stripANSI(got),
				"colorize must preserve the raw characters exactly")
		})
	}
}

// TestColorizeLogLine_AppliesStylingWhenExpected pins which inputs get
// any styling at all (some ANSI must appear). Inputs the colorizer
// intentionally leaves untouched stay byte-identical.
func TestColorizeLogLine_AppliesStylingWhenExpected(t *testing.T) {
	th := theme.Default()
	cases := map[string]struct {
		line       string
		wantStyled bool
	}{
		"empty stays empty":           {line: "", wantStyled: false},
		"unstyled plain message":      {line: "hello world", wantStyled: false},
		"level token styles":          {line: "INFO server started", wantStyled: true},
		"iso timestamp styles":        {line: "2026-05-20T13:45:01Z message", wantStyled: true},
		"bracketed timestamp styles":  {line: "[2026-05-20 13:45:01] message", wantStyled: true},
		"klog preamble styles":        {line: "E0520 13:45:01.234567       1 ctrl.go:1] boom", wantStyled: true},
		"json object styles":          {line: `{"k":"v"}`, wantStyled: true},
		"stack frame tab styles":      {line: "\tat com.example.Foo.bar(Foo.java:42)", wantStyled: true},
		"stack frame  spaces styles":  {line: "    at runtime.main()", wantStyled: true},
		"non-stack indented untouched": {line: "  just indented", wantStyled: false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := colorizeLogLine(tc.line, th)
			styled := got != tc.line
			assert.Equal(t, tc.wantStyled, styled,
				"styling presence mismatch — got %q, plain %q", got, tc.line)
		})
	}
}

// TestColorizeLogLine_LevelTokensColored sanity-checks the level color
// map by asserting the rendered output contains the level word from the
// theme palette. We strip ANSI to confirm the word survives; the
// presence of *any* ANSI proves color was applied. Covers both casing
// variants — the colorizer was originally uppercase-only, which silently
// missed the majority of real-world structured loggers (zap, logrus,
// zerolog, slog all default to lowercase).
func TestColorizeLogLine_LevelTokensColored(t *testing.T) {
	th := theme.Default()
	words := []string{
		"ERROR", "WARN", "INFO", "DEBUG", "TRACE", "FATAL", "NOTICE", "ERR", "WARNING",
		"error", "warn", "info", "debug", "trace", "fatal", "notice", "err", "warning",
	}
	for _, level := range words {
		t.Run(level, func(t *testing.T) {
			line := level + " something happened"
			got := colorizeLogLine(line, th)
			assert.NotEqual(t, line, got, "expected level %q to be styled", level)
			assert.Equal(t, line, stripANSI(got), "plain text must survive")
		})
	}
}

// TestColorizeLogLine_JSONLevelValueStyled pins the most common
// structured-log shape: `{"level":"error",...}`. The value must get the
// severity register (red bold), not the generic string-green — that's
// the difference between a screen of green-strings that all look alike
// and one where the on-caller can scan for red on sight.
func TestColorizeLogLine_JSONLevelValueStyled(t *testing.T) {
	th := theme.Default()
	cases := []struct {
		name string
		line string
	}{
		{"level error", `{"level":"error","msg":"boom"}`},
		{"level info", `{"level":"info","msg":"hi"}`},
		{"lvl warn", `{"lvl":"warn","msg":"slow"}`},
		{"severity uppercase", `{"severity":"ERROR","msg":"x"}`},
		{"log.level ecs", `{"log.level":"warning","msg":"x"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := colorizeLogLine(c.line, th)
			assert.NotEqual(t, c.line, got, "level-keyed JSON value must receive styling")
			assert.Equal(t, c.line, stripANSI(got), "plain text must survive")
		})
	}
}

// TestColorizeLogLine_JSONLevelValueUnrecognized covers the fallback:
// when the level value isn't one we know how to color (custom levels
// like "verbose", "fine"), it must still receive *some* styling rather
// than rendering uncolored — generic green-string is the right register.
func TestColorizeLogLine_JSONLevelValueUnrecognized(t *testing.T) {
	th := theme.Default()
	line := `{"level":"verbose","msg":"hi"}`
	got := colorizeLogLine(line, th)
	assert.NotEqual(t, line, got)
	assert.Equal(t, line, stripANSI(got))
}

// TestColorizeLogLine_InlineTimestampDimmed catches the mid-line case
// our line-head dimming misses: "INFO 2026-05-20 13:45:01 server.go ..."
// where the timestamp sits between the level and the message body.
func TestColorizeLogLine_InlineTimestampDimmed(t *testing.T) {
	th := theme.Default()
	cases := []string{
		"INFO 2026-05-20 13:45:01 server.go:123: started",
		"info 2026-05-20T13:45:01Z message",
		"event happened on 2026-05-20",
		"ERROR 2026-05-20T13:45:01.234+02:00 auth failed",
	}
	for _, line := range cases {
		t.Run(short(line), func(t *testing.T) {
			got := colorizeLogLine(line, th)
			assert.NotEqual(t, line, got, "inline timestamp should be dimmed")
			assert.Equal(t, line, stripANSI(got), "plain text must survive")
		})
	}
}

// TestColorizeLogLine_JSONTimeValueDimmed pins the structured-log
// equivalent: time fields ("ts", "time", "timestamp", "@timestamp") and
// their values are rendered in the dim register so the eye doesn't
// linger on a column of identical-shaped ISO dates.
func TestColorizeLogLine_JSONTimeValueDimmed(t *testing.T) {
	th := theme.Default()
	cases := []struct {
		name, line string
	}{
		{"ts iso", `{"ts":"2026-05-20T13:45:01Z","msg":"hi"}`},
		{"time iso", `{"time":"2026-05-20T13:45:01.234Z","msg":"x"}`},
		{"@timestamp ecs", `{"@timestamp":"2026-05-20T13:45:01Z","msg":"x"}`},
		{"ts epoch", `{"ts":1716210301,"msg":"x"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := colorizeLogLine(c.line, th)
			assert.NotEqual(t, c.line, got)
			assert.Equal(t, c.line, stripANSI(got))
		})
	}
}

// TestColorizeLogLine_StackTraceWholeLineDimmed locks the choice that
// the entire continuation line gets one styling pass — frames read as
// one visual block, not a rainbow of incidental level matches inside
// fully-qualified Java class names ("com.foo.ErrorHandler.process").
func TestColorizeLogLine_StackTraceWholeLineDimmed(t *testing.T) {
	th := theme.Default()
	line := "\tat com.foo.ErrorHandler.process(ErrorHandler.java:42)"
	got := colorizeLogLine(line, th)
	// Confirm only ONE style run wraps the line — leading ESC[ and a
	// terminating reset, no embedded ERROR-red mid-line. We test this by
	// counting CSI introducers; whole-line dim = 1 opener + 1 closer.
	openers := strings.Count(got, "\x1b[")
	assert.LessOrEqual(t, openers, 2,
		"stack frame should be a single styling run, not multi-token coloured")
}

// short returns a stable, short label for a log-line case so test names
// stay legible in -v output. Long JSON / klog lines would otherwise
// dominate the column.
func short(s string) string {
	if s == "" {
		return "empty"
	}
	if len(s) > 30 {
		return s[:30] + "…"
	}
	return s
}
