package sections

import (
	"image/color"
	"regexp"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/YunusAndreasson/safina/internal/tui/theme"
)

// colorizeLogLine paints a log line with light syntax highlighting —
// timestamps dim, log levels in their semantic color (ERROR red, WARN
// yellow, INFO blue, DEBUG/TRACE dim), klog preambles dimmed except for
// the level letter, JSON structure dimmed so values pop, stack-trace
// continuation lines dim. Same shape as the YAML colorizer in
// describe.go: opinionated subset of what an on-caller actually scans
// for, not exhaustive tokenization. Filter-match highlighting is
// orthogonal and handled by the caller — we only run when there's no
// active query (see refreshContent).
func colorizeLogLine(line string, t *theme.Theme) string {
	if line == "" {
		return line
	}

	// Stack-trace continuation: tab-indented frames or "    at ..." style.
	// Render the body dim so the eye glides past the frames and lands on
	// the panic/exception message above. Leading whitespace is emitted
	// unstyled because lipgloss normalises tabs inside styled segments
	// (a `\t` would land as four spaces in the output, breaking the raw-
	// text-preserving contract callers rely on).
	if isStackContinuation(line) {
		indent, body := splitLeadingWhitespace(line)
		return indent + styleDim(t).Render(body)
	}

	// klog format wins outright — it has its own preamble shape:
	// I0520 13:45:01.234567       1 file.go:42] Message...
	if m := reLogKlog.FindStringSubmatchIndex(line); m != nil {
		return colorizeKlog(line, m, t)
	}

	var b strings.Builder
	rest := line

	// ISO 8601 timestamp prefix, optionally followed by the kubectl
	// --timestamps "stderr F " / "stdout F " framing. Both get dimmed in
	// one go so the message proper starts at a brighter register.
	if m := reLogTimestampISO.FindString(rest); m != "" {
		b.WriteString(styleDim(t).Render(m))
		rest = rest[len(m):]
		if frame := reLogKubectlFrame.FindString(rest); frame != "" {
			b.WriteString(styleDim(t).Render(frame))
			rest = rest[len(frame):]
		}
	} else if m := reLogTimeOnly.FindString(rest); m != "" {
		b.WriteString(styleDim(t).Render(m))
		rest = rest[len(m):]
	} else if m := reLogBracketTS.FindString(rest); m != "" {
		b.WriteString(styleDim(t).Render(m))
		rest = rest[len(m):]
	}

	// JSON line: structure dim, string values green, keys cyan,
	// numbers pink, true/false/null yellow, level-named values get the
	// level color register. Only triggers when the fully-trimmed
	// remainder starts with `{` and ends with `}` — partial JSON inside
	// a freeform log falls through to body coloring. Leading and
	// trailing whitespace is preserved verbatim so the line round-trips
	// through stripANSI byte-for-byte.
	indent, body := splitLeadingWhitespace(rest)
	bodyTrimRight := strings.TrimRight(body, " \t")
	tail := body[len(bodyTrimRight):]
	if strings.HasPrefix(bodyTrimRight, "{") && strings.HasSuffix(bodyTrimRight, "}") {
		b.WriteString(indent)
		b.WriteString(colorizeJSONLine(bodyTrimRight, t))
		b.WriteString(tail)
		return b.String()
	}

	b.WriteString(colorizeLogBody(rest, t))
	return b.String()
}

// Anchored at line start so we never mistake a timestamp embedded in a
// message body for a preamble. Captures fractional seconds (dot OR
// comma — Python's logging module and Java logback default to a comma
// separator) and optional timezone (Z, ±HH:MM, ±HHMM). Trailing space
// is included so the caller can drop it cleanly without re-trimming.
var reLogTimeOnly = regexp.MustCompile(`^\d{2}:\d{2}:\d{2} `)

var reLogTimestampISO = regexp.MustCompile(
	`^\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(?:[.,]\d+)?(?:Z|[+-]\d{2}:?\d{2})?\s?`,
)

// kubectl --timestamps frames each record with "stderr F " / "stdout F "
// (F = full line, P = partial) right after the ISO timestamp. Treated as
// chrome alongside the timestamp.
var reLogKubectlFrame = regexp.MustCompile(`^(?:stderr|stdout)\s[FP]\s`)

// Bracketed timestamp prefix like "[2026-05-20 13:45:01]" — common in
// apps that don't emit ISO at the line head.
var reLogBracketTS = regexp.MustCompile(
	`^\[\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:?\d{2})?\]\s?`,
)

// klog format: <Letter><MMDD> HH:MM:SS.uuuuuu  PID file.go:line] msg
// The letter encodes severity (I/W/E/F); the rest is preamble noise.
var reLogKlog = regexp.MustCompile(
	`^([IWEF])(\d{4}\s\d{2}:\d{2}:\d{2}(?:\.\d+)?\s+\d+\s+\S+:\s*)`,
)

// Word-bounded level token. Case-insensitive (`(?i)`) because most
// structured loggers emit lowercase (`info`, `error`) and the
// uppercase-only earlier version silently did nothing on the majority
// of real-world logs. Matched anywhere in the remainder — apps place
// the token before, after, or bracketed around the message. The list
// deliberately omits common false friends (TRACK, DEBUGGER).
var reLogLevel = regexp.MustCompile(
	`(?i)\b(FATAL|ERROR|ERR|WARN(?:ING)?|INFO|NOTICE|DEBUG|TRACE)\b`,
)

// colorizeKlog handles the klog preamble in a single allocation: paint
// the level letter bold in its semantic color, the rest of the preamble
// dim, then hand the message body to colorizeLogBody so an embedded
// level word ("E0520 ... ERROR: ...") doesn't double-color.
func colorizeKlog(line string, m []int, t *theme.Theme) string {
	dim := styleDim(t)
	letter := line[m[2]:m[3]]
	var lc color.Color
	switch letter {
	case "I":
		lc = t.P.Blue
	case "W":
		lc = t.P.Yellow
	case "E", "F":
		lc = t.P.Red
	default:
		lc = t.P.Dim
	}
	levelStyle := lipgloss.NewStyle().Foreground(lc).Bold(true)
	var b strings.Builder
	b.WriteString(levelStyle.Render(letter))
	b.WriteString(dim.Render(line[m[3]:m[1]]))
	b.WriteString(colorizeLogBody(line[m[1]:], t))
	return b.String()
}

// colorizeLogBody paints inline level tokens and inline timestamps
// (mid-line, not the prefix variant which the caller already handled).
// Both are collected as (start, end, style) regions on the original
// string, sorted by position, then emitted with the unstyled gaps
// between them. Region-based instead of sequential string-replace so
// each rule operates on the original byte positions — otherwise the
// timestamp regex would see ANSI escape codes injected by an earlier
// pass and either miss matches or match across them.
func colorizeLogBody(s string, t *theme.Theme) string {
	if s == "" {
		return s
	}

	var regions []logRegion

	// Inline timestamps — dim them so the on-caller's eye lands on the
	// message body rather than the date/time. The colorizer at line head
	// already dimmed leading timestamps; this catches mid-line ones like
	// "INFO 2026-05-20 13:45:01 server.go:123: ...".
	for _, m := range reLogInlineTime.FindAllStringIndex(s, -1) {
		regions = append(regions, logRegion{start: m[0], end: m[1], style: styleDim(t)})
	}

	// Level token. Skipped if it overlaps an already-collected
	// timestamp region (defensive — regexes target disjoint shapes, but
	// rendering two ANSI runs at the same position garbles the output).
	if m := reLogLevel.FindStringSubmatchIndex(s); m != nil {
		start, end := m[2], m[3]
		if st, ok := levelStyleFor(s[start:end], t); ok && !overlapsRegions(regions, start, end) {
			regions = append(regions, logRegion{start: start, end: end, style: st})
		}
	}

	if len(regions) == 0 {
		return s
	}

	// Sort by start, then emit unstyled gaps + styled regions in order.
	sort.Slice(regions, func(i, j int) bool { return regions[i].start < regions[j].start })

	var b strings.Builder
	pos := 0
	for _, r := range regions {
		if r.start < pos {
			continue
		}
		b.WriteString(s[pos:r.start])
		b.WriteString(r.style.Render(s[r.start:r.end]))
		pos = r.end
	}
	b.WriteString(s[pos:])
	return b.String()
}

// logRegion ties a [start, end) span to the style to render over it.
// Carried as a slice in colorizeLogBody so multiple highlight rules
// (level, timestamp, future ones) can be collected then emitted in
// position order without sequential string-rewrites that would force
// each later rule to scan past ANSI escape sequences injected by
// earlier ones.
type logRegion struct {
	start, end int
	style      lipgloss.Style
}

// overlapsRegions reports whether [start, end) intersects any existing
// region in rs. Used to keep the level-token paint from clobbering an
// already-dimmed timestamp region.
func overlapsRegions(rs []logRegion, start, end int) bool {
	for _, r := range rs {
		if start < r.end && end > r.start {
			return true
		}
	}
	return false
}

// reLogInlineTime catches a date-and-optional-time anywhere in the
// line. Anchored on word boundaries so we don't half-match a
// version-string like "v2026-05-20". Covers:
//   - "2026-05-20T13:45:01Z" / "2026-05-20T13:45:01.234+02:00" (ISO)
//   - "2026-05-20 13:45:01" / "...01.234" (space-separated)
//   - "2026-05-20" (date only)
//
// Time-of-day alone (`13:45:01`) is intentionally excluded — without a
// date anchor it overlaps too easily with durations, version numbers,
// and IPv6 fragments.
var reLogInlineTime = regexp.MustCompile(
	`\b\d{4}-\d{2}-\d{2}(?:[T ]\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:?\d{2})?)?\b`,
)

// styleLogLevel maps the recognized level word to a bold colored
// rendering. Thin wrapper over levelStyleFor for the call sites that
// just want the rendered string (klog preamble letter, JSON level
// value).
func styleLogLevel(word string, t *theme.Theme) string {
	if st, ok := levelStyleFor(word, t); ok {
		return st.Render(word)
	}
	return word
}

// levelStyleFor returns the style for a recognized severity word and
// true; or a zero Style and false. Split out from styleLogLevel so the
// region-collecting code in colorizeLogBody can capture the style
// without rendering yet (rendering must happen in sorted order, not at
// detection time).
func levelStyleFor(word string, t *theme.Theme) (lipgloss.Style, bool) {
	switch strings.ToUpper(word) {
	case "ERROR", "ERR", "FATAL":
		return lipgloss.NewStyle().Foreground(t.P.Red).Bold(true), true
	case "WARN", "WARNING":
		return lipgloss.NewStyle().Foreground(t.P.Yellow).Bold(true), true
	case "INFO", "NOTICE":
		return lipgloss.NewStyle().Foreground(t.P.Blue).Bold(true), true
	case "DEBUG", "TRACE":
		return lipgloss.NewStyle().Foreground(t.P.Dim), true
	}
	return lipgloss.Style{}, false
}

// colorizeJSONLine is a single-pass tokenizer with just enough state to
// distinguish keys (string before `:`) from values (string after `:`).
// Structure ({}, [], :, ,) is rendered dim so the data jumps out; keys
// land in cyan to match the YAML colorizer's register; string values
// green, numbers pink, true/false/null bold yellow.
//
// Doesn't validate JSON — malformed input produces a best-effort
// coloring rather than an error (logs in the wild are messy; bail-and-
// dump would lose the case where coloring would still help).
func colorizeJSONLine(s string, t *theme.Theme) string {
	dim := styleDim(t)
	cyan := lipgloss.NewStyle().Foreground(t.P.Cyan)
	green := lipgloss.NewStyle().Foreground(t.P.Green)
	pink := lipgloss.NewStyle().Foreground(t.P.Pink)
	yellow := lipgloss.NewStyle().Foreground(t.P.Yellow).Bold(true)

	var b strings.Builder
	// expectKey tracks whether the next string token is a key (after `{`
	// or after a `,` inside an object). Approximate — we don't track a
	// real bracket stack — but it's right for the by-far-most-common
	// case (flat objects from zap/logrus/zerolog) and the cost of a miss
	// is just that one value gets cyan instead of green.
	expectKey := true
	// levelValueNext is set when we just emitted a key like "level" /
	// "lvl" / "severity" / "log.level" — the following string value
	// gets the severity color register (bold red/yellow/blue) instead
	// of the generic string green, so on-callers can scan a screen of
	// `{"level":"error",...}` lines as fast as they would `ERROR ...`
	// freeform lines.
	levelValueNext := false
	// timeValueNext is the same shape but for fields that name a
	// timestamp ("ts" / "time" / "timestamp" / "@timestamp"). The value
	// gets dimmed so the eye doesn't stop on a date string that's the
	// same across every line — same instinct as the line-prefix
	// timestamp dimming at the top of colorizeLogLine.
	timeValueNext := false
	i := 0
	for i < len(s) {
		c := s[i]
		switch c {
		case '{', '}', '[', ']':
			b.WriteString(dim.Render(string(c)))
			switch c {
			case '{':
				expectKey = true
			case '[':
				expectKey = false
			}
			i++
		case ',':
			b.WriteString(dim.Render(","))
			expectKey = true
			i++
		case ':':
			b.WriteString(dim.Render(":"))
			expectKey = false
			i++
		case '"':
			j := i + 1
			for j < len(s) {
				if s[j] == '\\' && j+1 < len(s) {
					j += 2
					continue
				}
				if s[j] == '"' {
					j++
					break
				}
				j++
			}
			tok := s[i:j]
			switch {
			case expectKey:
				b.WriteString(cyan.Render(tok))
				// Inner content between the quotes — strip them to
				// match against known level / time key names. Lowercase
				// once and check both registries.
				if j-i >= 2 {
					inner := strings.ToLower(s[i+1 : j-1])
					if isLevelKey(inner) {
						levelValueNext = true
					} else if isTimeKey(inner) {
						timeValueNext = true
					}
				}
			case timeValueNext:
				// String value of a time-named key — dim the whole
				// quoted token (quotes included) so it reads as a
				// single recessive unit, matching the line-head
				// timestamp dim.
				b.WriteString(dim.Render(tok))
				timeValueNext = false
			case levelValueNext:
				// String value of a level-named key. Color the inner
				// word with the severity register if it's a recognized
				// level; otherwise fall back to the generic green so
				// unfamiliar custom levels still read as JSON strings
				// rather than uncolored gaps in the line.
				if j-i >= 2 {
					inner := s[i+1 : j-1]
					if isLevelWord(inner) {
						b.WriteString(dim.Render(`"`))
						b.WriteString(styleLogLevel(inner, t))
						b.WriteString(dim.Render(`"`))
					} else {
						b.WriteString(green.Render(tok))
					}
				} else {
					b.WriteString(green.Render(tok))
				}
				levelValueNext = false
			default:
				b.WriteString(green.Render(tok))
			}
			i = j
		case ' ', '\t':
			b.WriteByte(c)
			i++
		default:
			j := i
			for j < len(s) && !isJSONDelim(s[j]) {
				j++
			}
			if j == i {
				b.WriteByte(c)
				i++
				continue
			}
			tok := s[i:j]
			switch {
			case timeValueNext:
				// Epoch timestamp (e.g. `"ts":1716210301`) — dim it
				// like the string-shaped variant above so all time
				// fields share one register regardless of encoding.
				b.WriteString(dim.Render(tok))
				timeValueNext = false
			case tok == "true" || tok == "false" || tok == "null":
				b.WriteString(yellow.Render(tok))
			default:
				b.WriteString(pink.Render(tok))
			}
			i = j
		}
	}
	return b.String()
}

// isLevelKey lists the field names structured loggers conventionally
// use for severity. Includes the dotted variant ("log.level") emitted
// by Elasticsearch Common Schema. Case-folded by the caller.
func isLevelKey(k string) bool {
	switch k {
	case "level", "lvl", "severity", "log.level", "loglevel":
		return true
	}
	return false
}

// isTimeKey lists the field names structured loggers conventionally use
// for the per-record timestamp. Covers zap (`ts`), logrus (`time`),
// zerolog (`time`), slog (`time`), python's standard `asctime`,
// Elasticsearch Common Schema (`@timestamp`), and the all-caps Datadog
// shape (`@timestamp` lowercased here since the caller case-folds).
// Case-folded by the caller.
func isTimeKey(k string) bool {
	switch k {
	case "ts", "time", "timestamp", "@timestamp", "asctime", "datetime", "eventtime":
		return true
	}
	return false
}

// isLevelWord reports whether word is one of the severity tokens
// styleLogLevel knows how to paint. Used by the JSON branch to avoid
// running unrecognized values through styleLogLevel (which would leave
// them uncolored — worse than the generic string-green).
func isLevelWord(word string) bool {
	switch strings.ToUpper(word) {
	case "ERROR", "ERR", "FATAL",
		"WARN", "WARNING",
		"INFO", "NOTICE",
		"DEBUG", "TRACE":
		return true
	}
	return false
}

func isJSONDelim(c byte) bool {
	return c == ',' || c == '}' || c == ']' || c == ' ' || c == '\t' || c == ':'
}

// isStackContinuation recognises the tab-or-indent prefixed frames that
// follow a panic / exception line. Go and tabbed-Java use a literal tab;
// Python / spaced-Java use 4+ spaces with "at " or "File ". Whole-line
// dimming lets the eye skip frames and land on the message itself.
func isStackContinuation(line string) bool {
	if line == "" {
		return false
	}
	if line[0] == '\t' {
		return true
	}
	if strings.HasPrefix(line, "    at ") || strings.HasPrefix(line, "  File \"") {
		return true
	}
	return false
}

// styleDim is a small allocation-free helper — every colorize branch
// reaches for the dim style, and inlining the constructor at each site
// would inflate the file. Identical to lipgloss.NewStyle().Foreground(...).
func styleDim(t *theme.Theme) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(t.P.Dim)
}

// splitLeadingWhitespace separates a run of leading spaces/tabs from the
// rest of a line. Used so that styled segments don't include tabs
// (lipgloss expands a tab inside a styled run to spaces during render
// and that would corrupt the plain-text contract for raw-line callers).
func splitLeadingWhitespace(s string) (indent, body string) {
	i := 0
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	return s[:i], s[i:]
}
