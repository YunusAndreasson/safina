package sections

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
	"github.com/yunus/safina/internal/tui/keys"
	"github.com/yunus/safina/internal/tui/theme"
	"github.com/yunus/safina/internal/tui/tuictx"
)

func TestShellFrameCursorAndCleanup(t *testing.T) {
	ctx := tuictx.New(theme.Default(), keys.Default(), nil, nil)
	s := NewShell(ctx)
	t.Cleanup(s.Close)
	s.SetSize(100, 30)
	s.Reset("test", "ns", "pod", "")
	s.phase = phaseLive
	s.IngestBytes([]byte("$ "))
	view := ansi.Strip(s.View(ctx, 100, 30))
	rows := strings.Split(view, "\n")
	require.Len(t, rows, 30)
	cursor := s.Cursor(100, 30)
	require.NotNil(t, cursor)
	row := rows[cursor.Y]
	require.Equal(t, "$ ", string([]rune(row)[cursor.X-2:cursor.X]))
	s.IngestBytes([]byte("\x1b[?25l"))
	require.Nil(t, s.Cursor(100, 30))
	old := s.term
	s.Reset("test", "ns", "other", "")
	_, _, err := old.Frame()
	require.Error(t, err)
	s.Close()
	require.Nil(t, s.term)
	require.Nil(t, s.Cursor(100, 30))
}
