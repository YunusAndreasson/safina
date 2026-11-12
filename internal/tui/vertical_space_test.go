package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/YunusAndreasson/safina/internal/tui/components"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// logPage is the api app's page on its Logs tab with more lines than any
// screen holds, at w × h.
func logPage(t *testing.T, w, h int) *Model {
	t.Helper()
	m := pageModel(t)
	m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	m.handleKey(keyMsg("enter"))
	loadPods(m)
	m.handleKey(keyMsg("l"))
	t.Cleanup(m.stopLogStream)
	var b strings.Builder
	for i := range 200 {
		fmt.Fprintf(&b, "line-%03d\n", i)
	}
	m.logs.Append(strings.TrimSuffix(b.String(), "\n"))
	return m
}

func visibleLogLines(m *Model) int {
	return strings.Count(ansi.Strip(m.View().Content), "line-")
}

// headerRows is what the full header adds above the location row on a
// screen big enough to carry it.
func headerRows(m *Model) int {
	if m.fullHeader() {
		return components.HeaderRows
	}
	return 0
}

// The screen is used to its last row, and the full header comes with room
// for it: it gives its rows back on a small screen.
func TestPageFillsTheScreenAndTheHeaderNeedsRoom(t *testing.T) {
	for _, size := range [][2]int{{160, 37}, {100, 30}, {80, 24}, {50, 14}} {
		m := logPage(t, size[0], size[1])
		rows := strings.Split(ansi.Strip(m.View().Content), "\n")
		require.Len(t, rows, size[1])
		top := headerRows(m)
		require.Equal(t, size[0] >= components.MinHeaderWidth && size[1] >= components.MinHeaderHeight, top > 0, "full header at %v", size)
		require.Positive(t, visibleLogLines(m), "the logs modal shows lines at %v", size)
	}
}

func TestFullHeaderNamesTheClusterAndListsThePageKeys(t *testing.T) {
	m := logPage(t, 160, 37)
	head := strings.Join(strings.Split(ansi.Strip(m.View().Content), "\n")[:components.HeaderRows], "\n")
	require.Contains(t, head, "context  demo")
	require.NotContains(t, head, "←/→", "tabs have their own keys")
	require.Contains(t, head, "space pause")
	require.Contains(t, head, "s a f i n a")
}

func TestTopbarSaysWhatThePageIsAndHowItIsDoing(t *testing.T) {
	m := logPage(t, 160, 37)
	top := strings.Split(ansi.Strip(m.View().Content), "\n")[headerRows(m)]
	require.Contains(t, top, "api")
	require.Contains(t, top, "Deployment")
}

func TestFeedbackDoesNotMoveThePage(t *testing.T) {
	m := logPage(t, 120, 30)
	before := visibleLogLines(m)
	m.bannerMsg, m.bannerErr = "restarted api", false
	view := strings.Split(ansi.Strip(m.View().Content), "\n")
	top := headerRows(m)
	require.Contains(t, view[top], "✓ restarted api", "confirmations show on the location row")
	require.Equal(t, before, visibleLogLines(m))

	m.bannerMsg, m.bannerErr = "connection refused", true
	view = strings.Split(ansi.Strip(m.View().Content), "\n")
	require.Contains(t, view[top+1], "connection refused", "an error earns its own row")
	require.Equal(t, before-1, visibleLogLines(m), "and only one")
}

func TestLogFilterRowAppearsOnlyWhileInUse(t *testing.T) {
	m := logPage(t, 120, 30)
	before := visibleLogLines(m)
	m.handleKey(keyMsg("/"))
	require.Equal(t, before-1, visibleLogLines(m), "an open filter takes one row")
	m.handleKey(keyMsg("esc"))
	require.Equal(t, before, visibleLogLines(m), "a closed, empty filter takes none")
}

// Under the full header the page is framed: the location titles it from
// the line above, the footer is the bottom edge, and Esc is named once.
func TestFullHeaderFramesThePage(t *testing.T) {
	m := logPage(t, 160, 37)
	rows := strings.Split(ansi.Strip(m.View().Content), "\n")
	require.True(t, strings.HasPrefix(rows[components.HeaderRows], strings.Repeat(" ", components.HeaderMargin)+"demo"), rows[components.HeaderRows])
	top, bottom := rows[components.HeaderRows+1], rows[len(rows)-1]
	require.True(t, strings.HasPrefix(top, "╭─") && strings.HasSuffix(top, "─╮"), top)
	require.True(t, strings.HasPrefix(bottom, "╰─ ") && strings.HasSuffix(bottom, "─╯"), bottom)
	require.Equal(t, 1, strings.Count(bottom, "esc"), bottom)
	require.Contains(t, bottom, "? help")
	for _, r := range rows[components.HeaderRows+2 : len(rows)-1] {
		require.True(t, strings.HasPrefix(r, "│") && strings.HasSuffix(r, "│"), r)
	}
}

// In a viewer ctrl+b and ctrl+f page up and down, as in less and k9s; they
// do not open resource types or the forwards list there.
func TestViewerPagesWithCtrlBAndCtrlF(t *testing.T) {
	m := logPage(t, 120, 30)
	bottom := m.View().Content
	m.handleKey(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	require.Equal(t, stateLogView, m.state)
	up := m.View().Content
	require.NotEqual(t, bottom, up, "ctrl+b pages up")
	m.handleKey(tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl})
	require.Equal(t, stateLogView, m.state, "ctrl+f pages, it does not open the forwards list")
	require.NotEqual(t, up, m.View().Content, "ctrl+f pages down")
}
