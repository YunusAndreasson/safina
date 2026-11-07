package sections

import (
	"strings"
	"testing"

	"charm.land/bubbles/v2/viewport"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestChatUserMessageWrapsWithoutLosingText(t *testing.T) {
	c := Chat{vp: viewport.New(viewport.WithWidth(30))}
	message := "Reply with exactly QA_OK. Do not run tools or change the cluster."
	body := c.renderTurnBody(chatTurn{role: "user", text: message})
	lines := strings.Split(body, "\n")
	require.Greater(t, len(lines), 1)
	for i, line := range lines {
		require.LessOrEqual(t, ansi.StringWidth(line), 30)
		lines[i] = strings.TrimSpace(line)
	}
	require.Equal(t, message, strings.Join(lines, " "))
}
