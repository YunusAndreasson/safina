package sections

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/YunusAndreasson/safina/internal/claude"
	"github.com/stretchr/testify/require"
)

func TestChatScrollbackBoundsLongConversationsAndKeepsCurrentExchange(t *testing.T) {
	c := Chat{}
	for range maxChatTurns * 2 {
		c.turns = append(c.turns, chatTurn{role: "user", text: "question"}, chatTurn{role: "assistant", text: "answer"})
		c.trimScrollback()
	}
	require.LessOrEqual(t, len(c.turns), maxChatTurns)
	require.Positive(t, c.omittedTurns)
	c.turns = append(c.turns, chatTurn{role: "user", text: "current question"}, chatTurn{role: "assistant", text: "current answer", pending: true})
	large := strings.Repeat("x", maxChatTurnBytes)
	for n := range len(c.turns) - 2 {
		c.turns[n].text = large
	}
	c.trimScrollback()
	bytes := 0
	for _, turn := range c.turns {
		bytes += len(turn.text)
	}
	require.LessOrEqual(t, bytes, maxChatBytes)
	require.Equal(t, "current question", c.turns[len(c.turns)-2].text)
	require.True(t, c.turns[len(c.turns)-1].pending)
	c.ResetSession()
	require.Nil(t, c.turns)
	require.Zero(t, c.omittedTurns)
}

func TestChatOversizedResponseIsBoundedAndTruncationIsVisibleWhenCopied(t *testing.T) {
	text, truncated := boundedChatDelta("", strings.Repeat("界", maxChatTurnBytes))
	require.True(t, truncated)
	require.LessOrEqual(t, len(text), maxChatTurnBytes)
	require.True(t, utf8.ValidString(text))
	c := Chat{streaming: true, turns: []chatTurn{{role: "assistant", text: text, truncated: true}}}
	c.IngestToken("more text")
	require.Equal(t, text, c.turns[0].text)
	require.Contains(t, c.YankPayload(), chatTruncationNotice)
}

func TestChatNonDisplayEventBurstStillDeliversNextToken(t *testing.T) {
	ch := make(chan claude.Event, 30001)
	for range 30000 {
		ch <- claude.Event{Kind: claude.KindInit}
	}
	ch <- claude.Event{Kind: claude.KindToken, Delta: "answer"}
	close(ch)
	result, ok := NextChatEvent(ch)().(ChatTokenMsg)
	require.True(t, ok)
	require.Equal(t, "answer", result.Delta)
}
