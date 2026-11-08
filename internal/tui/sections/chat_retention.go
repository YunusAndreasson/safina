package sections

import (
	"slices"
	"strings"
	"unicode/utf8"
)

const (
	maxChatTurns         = 200
	maxChatBytes         = 4 << 20
	maxChatTurnBytes     = 1 << 20
	chatTruncationNotice = "\n\n[Response truncated in Safina scrollback.]"
)

// The CLI owns the resumable conversation. Bound only its terminal scrollback,
// preserving the current exchange and making omitted text explicit.
func (c *Chat) trimScrollback() {
	bytes := 0
	for _, turn := range c.turns {
		bytes += len(turn.text)
	}
	drop := 0
	for len(c.turns)-drop > 2 && (len(c.turns)-drop > maxChatTurns || bytes > maxChatBytes) {
		n := min(2, len(c.turns)-drop-2)
		for _, turn := range c.turns[drop : drop+n] {
			bytes -= len(turn.text)
		}
		drop += n
	}
	if drop > 0 {
		c.omittedTurns += drop
		c.turns = slices.Delete(c.turns, 0, drop)
	}
}

func boundedChatDelta(current, delta string) (string, bool) {
	remaining := maxChatTurnBytes - len(current)
	if len(delta) <= remaining {
		return current + delta, false
	}
	n := max(0, remaining)
	for n > 0 && n < len(delta) && !utf8.RuneStart(delta[n]) {
		n--
	}
	return current + strings.Clone(delta[:n]), true
}
