package terminal

import (
	"io"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func testTerminal(t *testing.T, w, h int) *Terminal {
	t.Helper()
	term, err := New(w, h)
	require.NoError(t, err)
	t.Cleanup(term.Close)
	return term
}

func TestFrameAndCursor(t *testing.T) {
	term := testTerminal(t, 10, 3)
	_, err := term.Feed([]byte("\x1b[31m界é\x1b[0m\r\nhello\x1b[6 q"))
	require.NoError(t, err)
	frame, cursor, err := term.Frame()
	require.NoError(t, err)
	require.Contains(t, ansi.Strip(frame), "界é")
	rows := strings.Split(ansi.Strip(frame), "\n")
	require.Len(t, rows, 3)
	for _, row := range rows {
		require.Equal(t, 10, ansi.StringWidth(row))
	}
	require.NotNil(t, cursor)
	require.Equal(t, tea.Position{X: 5, Y: 1}, cursor.Position)
	require.Equal(t, tea.CursorBar, cursor.Shape)
	cursor.X = 99
	_, again, err := term.Frame()
	require.NoError(t, err)
	require.Equal(t, 5, again.X)
	_, err = term.Feed([]byte("\x1b[?25l"))
	require.NoError(t, err)
	_, cursor, err = term.Frame()
	require.NoError(t, err)
	require.Nil(t, cursor)
}

func TestInputProtocols(t *testing.T) {
	for _, tc := range []struct {
		name, mode string
		key        tea.KeyPressMsg
		want       string
	}{
		{"text", "", tea.KeyPressMsg{Code: 'a', Text: "a"}, "a"},
		{"unicode", "", tea.KeyPressMsg{Code: '界', Text: "界"}, "界"},
		{"interrupt", "", tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}, "\x03"},
		{"escape alias", "", tea.KeyPressMsg{Code: '[', Mod: tea.ModCtrl}, "\x1b"},
		{"alt", "", tea.KeyPressMsg{Code: 'x', Mod: tea.ModAlt}, "\x1bx"},
		{"up", "", tea.KeyPressMsg{Code: tea.KeyUp}, "\x1b[A"},
		{"application up", "\x1b[?1h", tea.KeyPressMsg{Code: tea.KeyUp}, "\x1bOA"},
		{"control up", "", tea.KeyPressMsg{Code: tea.KeyUp, Mod: tea.ModCtrl}, "\x1b[1;5A"},
		{"f5", "", tea.KeyPressMsg{Code: tea.KeyF5}, "\x1b[15~"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			term := testTerminal(t, 10, 3)
			_, err := term.Feed([]byte(tc.mode))
			require.NoError(t, err)
			data, err := term.Key(tc.key)
			require.NoError(t, err)
			require.Equal(t, tc.want, string(data))
		})
	}
	term := testTerminal(t, 10, 3)
	data, err := term.Feed([]byte("\x1b[2;3H\x1b[6n\x1b[?2004h"))
	require.NoError(t, err)
	require.Equal(t, "\x1b[2;3R", string(data))
	data, err = term.Paste("hello\nworld")
	require.NoError(t, err)
	require.Equal(t, "\x1b[200~hello\nworld\x1b[201~", string(data))
	data, err = term.Feed(nil)
	require.NoError(t, err)
	require.Empty(t, data)
}

func TestResizeAndClose(t *testing.T) {
	term := testTerminal(t, 10, 3)
	_, err := term.Feed([]byte("abcdefghij"))
	require.NoError(t, err)
	_, err = term.Resize(5, 4)
	require.NoError(t, err)
	frame, _, err := term.Frame()
	require.NoError(t, err)
	require.Contains(t, ansi.Strip(frame), "abcde\nfghij")
	term.Close()
	term.Close()
	_, err = term.Feed(nil)
	require.ErrorIs(t, err, io.ErrClosedPipe)
	_, err = term.Paste("x")
	require.ErrorIs(t, err, io.ErrClosedPipe)
	_, _, err = term.Frame()
	require.ErrorIs(t, err, io.ErrClosedPipe)
}

func TestFrameDoesNotForwardRemoteSideEffects(t *testing.T) {
	term := testTerminal(t, 20, 4)
	_, err := term.Feed([]byte("\x1b]0;remote title\x07\x1b]52;c;c2VjcmV0\x07\x1b[?1049h\x1b[2J\x1b[31mred\x1b[0m"))
	require.NoError(t, err)
	frame, _, err := term.Frame()
	require.NoError(t, err)
	require.Contains(t, ansi.Strip(frame), "red")
	require.Contains(t, frame, "\x1b[")
	for _, sequence := range []string{"remote title", "c2VjcmV0", "\x1b]52", "\x1b[?1049", "\x1b[2J"} {
		require.NotContains(t, frame, sequence)
	}
}

func TestStreamBoundariesAndAlternateScreen(t *testing.T) {
	term := testTerminal(t, 20, 4)
	for _, b := range []byte("界é👩‍💻") {
		_, err := term.Feed([]byte{b})
		require.NoError(t, err)
	}
	frame, _, err := term.Frame()
	require.NoError(t, err)
	require.Contains(t, ansi.Strip(frame), "界é👩‍💻")
	_, err = term.Feed([]byte("\x1b[?1049hother"))
	require.NoError(t, err)
	frame, _, err = term.Frame()
	require.NoError(t, err)
	require.Contains(t, ansi.Strip(frame), "other")
	require.NotContains(t, ansi.Strip(frame), "界")
	_, err = term.Feed([]byte("\x1b[?1049l"))
	require.NoError(t, err)
	frame, _, err = term.Frame()
	require.NoError(t, err)
	require.Contains(t, ansi.Strip(frame), "界é👩‍💻")
}
