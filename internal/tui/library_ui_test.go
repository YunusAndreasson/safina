package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
	"github.com/yunus/safina/internal/config"
	"github.com/yunus/safina/internal/k8s"
)

func TestSearchDialogsUseUnicodeCursorEditing(t *testing.T) {
	for _, st := range []state{statePalette, stateBrowser, stateClusters} {
		m := NewModel(&k8s.Manager{}, &config.AppConfig{})
		m.state = st
		query := func() string {
			// Only search dialogs are under test.
			//exhaustive:ignore
			switch st {
			case statePalette:
				return m.palette.Query()
			case stateBrowser:
				return m.browser.Query()
			default:
				return m.clusters.Query()
			}
		}
		m.handlePaste(tea.PasteMsg{Content: "café"})
		m.handleKey(tea.KeyPressMsg{Code: tea.KeyLeft})
		m.handlePaste(tea.PasteMsg{Content: "x"})
		require.Equal(t, "cafxé", query())
		m.handleKey(tea.KeyPressMsg{Code: tea.KeyBackspace})
		require.Equal(t, "café", query())
		m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnd})
		m.handleKey(tea.KeyPressMsg{Code: tea.KeyBackspace})
		require.Equal(t, "caf", query())
		m.handleKey(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
		require.Empty(t, query())
		m.handleKey(tea.KeyPressMsg{Code: '界', Text: "界"})
		require.Equal(t, "界", query())
	}
}

func TestFloatingCardsKeepContextAndCursorGeometry(t *testing.T) {
	for _, size := range [][2]int{{50, 14}, {80, 24}, {120, 40}} {
		m := NewModel(&k8s.Manager{}, &config.AppConfig{})
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		base := "CONTEXT-MARKER\n" + strings.Repeat("underlying resources\n", size[1]-2)
		card := m.ctx.Theme.Modal.Width(28).Render("Dialog\nconfirm action")
		view := m.composeCard(base, card)
		require.Contains(t, ansi.Strip(view), "CONTEXT-MARKER")
		require.Contains(t, ansi.Strip(view), "confirm action")
		require.LessOrEqual(t, lipgloss.Width(view), size[0])
		require.LessOrEqual(t, lipgloss.Height(view), size[1])
		require.Equal(t, (size[0]-lipgloss.Width(card))/2, m.modalCardX)
		require.Equal(t, (size[1]-lipgloss.Height(card))/2, m.modalCardY)
	}
}

func TestFloatingCardRestoresBackgroundAfterNestedStyleReset(t *testing.T) {
	m := NewModel(&k8s.Manager{}, &config.AppConfig{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	th := m.ctx.Theme
	content := th.CyanBold.Render("Title") + " plain text\n" + th.ErrLine.Background(th.P.Red).Render("Error")
	card := th.Modal.Width(40).Render(content)
	frame := m.composeCard("background", card)
	canvas := lipgloss.NewCanvas(80, 24).Compose(lipgloss.NewLayer(frame))
	// A foreground-only child style's trailing reset must not punch a hole
	// in the dialog surface. Explicit error backgrounds still take precedence.
	for _, offset := range []int{3, 8, 15} {
		cell := canvas.CellAt(m.modalCardX+offset, m.modalCardY+2)
		require.NotNil(t, cell)
		require.Equal(t, th.P.BG2, cell.Style.Bg)
	}
	errorCell := canvas.CellAt(m.modalCardX+3, m.modalCardY+3)
	require.Equal(t, th.P.Red, errorCell.Style.Bg)
}

func TestFloatingCardSurfacePreservesWideCharacters(t *testing.T) {
	m := NewModel(&k8s.Manager{}, &config.AppConfig{})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	text := "café 東京 👋 end"
	card := m.ctx.Theme.Modal.Width(40).Render(m.ctx.Theme.CyanBold.Render(text))
	frame := m.composeCard("background", card)
	require.Contains(t, ansi.Strip(frame), text)
	require.LessOrEqual(t, lipgloss.Width(frame), 80)
}
