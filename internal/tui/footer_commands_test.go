package tui

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
	"github.com/yunus/safina/internal/config"
	"github.com/yunus/safina/internal/k8s"
	"github.com/yunus/safina/internal/tui/sections"
)

func shiftFooterModel(t *testing.T) *Model {
	t.Helper()
	m := NewModel(&k8s.Manager{}, &config.AppConfig{})
	t.Cleanup(m.ctx.Cancel)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m.ctx.ActiveContext = "demo"
	m.tree.PushContext("demo")
	m.tree.SetChildren(m.tree.Path(), &sections.Children{Kind: k8s.KindApp, Apps: []k8s.AppInfo{{Kind: k8s.AppKindDeployment, Namespace: "shop", Name: "api", UID: "api"}}})
	return &m
}

func enableShiftHints(m *Model) {
	m.Update(tea.KeyboardEnhancementsMsg{Flags: ansi.KittyReportEventTypes | ansi.KittyReportAllKeysAsEscapeCodes | ansi.KittyReportAssociatedKeys})
}

func TestShiftFooterDisclosureAndFallback(t *testing.T) {
	m := shiftFooterModel(t)
	enhancements := m.View().KeyboardEnhancements
	require.True(t, enhancements.ReportEventTypes)
	require.True(t, enhancements.ReportAllKeysAsEscapeCodes)
	require.True(t, enhancements.ReportAssociatedText)
	footer := ansi.Strip(m.renderFooter())
	require.Contains(t, footer, "l logs")
	require.Contains(t, footer, "m metrics")
	require.Contains(t, footer, "? commands")
	require.NotContains(t, footer, "hold")
	require.NotContains(t, footer, "S scale")
	// Partial protocol support must not advertise an unusable gesture.
	m.Update(tea.KeyboardEnhancementsMsg{Flags: ansi.KittyReportEventTypes})
	require.False(t, m.shiftHintsSupported)
	enableShiftHints(m)
	require.Contains(t, ansi.Strip(m.renderFooter()), "hold ⇧ changes")
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyLeftShift, Mod: tea.ModShift})
	require.Nil(t, cmd, "pressing Shift must not issue an action")
	require.Equal(t, stateMain, m.state)
	for _, width := range []int{50, 80, 120, 160} {
		m.ctx.Width = width
		footer = ansi.Strip(m.renderFooter())
		t.Logf("%d columns: %s", width, footer)
		require.Equal(t, 1, lipgloss.Height(footer))
		require.LessOrEqual(t, lipgloss.Width(footer), width)
		require.Contains(t, footer, "CHANGE")
		require.Contains(t, footer, "S scale")
		require.Contains(t, footer, "? commands")
		require.NotContains(t, footer, "l logs")
		if width >= 120 {
			require.Contains(t, footer, "Deployment/api")
		}
	}
	m.Update(tea.KeyReleaseMsg{Code: tea.KeyLeftShift})
	require.False(t, m.shiftHeld)
	require.NotContains(t, ansi.Strip(m.renderFooter()), "CHANGE")
}

func TestShiftFooterModifierReleaseAndBlur(t *testing.T) {
	m := shiftFooterModel(t)
	enableShiftHints(m)
	m.Update(tea.KeyPressMsg{Code: tea.KeyLeftShift, Mod: tea.ModShift})
	m.Update(tea.KeyPressMsg{Code: tea.KeyRightShift, Mod: tea.ModShift})
	m.Update(tea.KeyReleaseMsg{Code: tea.KeyLeftShift, Mod: tea.ModShift})
	require.True(t, m.shiftHeld, "right Shift is still held")
	m.Update(tea.KeyReleaseMsg{Code: tea.KeyRightShift})
	require.False(t, m.shiftHeld)
	m.Update(tea.KeyPressMsg{Code: tea.KeyLeftShift, Mod: tea.ModShift})
	m.Update(tea.BlurMsg{})
	require.False(t, m.shiftHeld)
	m.Update(tea.FocusMsg{})
	require.False(t, m.shiftHeld)
	m.Update(tea.KeyPressMsg{Code: tea.KeyLeftShift, Mod: tea.ModShift})
	m.Update(tea.KeyboardEnhancementsMsg{})
	require.False(t, m.shiftHeld)
	require.NotContains(t, ansi.Strip(m.renderFooter()), "hold")
}

func TestShiftFooterDoesNotChangeTypingOrConfirmations(t *testing.T) {
	m := shiftFooterModel(t)
	enableShiftHints(m)
	m.Update(keyMsg("/"))
	m.Update(tea.KeyPressMsg{Code: tea.KeyLeftShift, Mod: tea.ModShift})
	require.False(t, m.shiftHeld)
	m.Update(tea.KeyPressMsg{Code: 's', ShiftedCode: 'S', Mod: tea.ModShift, Text: "S"})
	require.Equal(t, "S", m.tree.FilterValue())
	require.Equal(t, stateMain, m.state)
	require.NotContains(t, ansi.Strip(m.renderFooter()), "CHANGE")

	m = shiftFooterModel(t)
	enableShiftHints(m)
	// Uppercase shortcuts still open a dialog without first revealing hints.
	m.Update(tea.KeyPressMsg{Code: 's', ShiftedCode: 'S', Mod: tea.ModShift, Text: "S"})
	require.Equal(t, stateScaleInput, m.state)
	require.False(t, m.shiftHeld)
	m.Update(tea.KeyPressMsg{Code: tea.KeyLeftShift, Mod: tea.ModShift})
	require.False(t, m.shiftHeld)
	require.Equal(t, stateScaleInput, m.state)

	m = shiftFooterModel(t)
	enableShiftHints(m)
	m.state = stateChatView
	m.chat.SetFocus(true)
	m.Update(tea.KeyPressMsg{Code: tea.KeyLeftShift, Mod: tea.ModShift})
	m.Update(tea.KeyPressMsg{Code: 's', ShiftedCode: 'S', Mod: tea.ModShift, Text: "S"})
	require.False(t, m.shiftHeld)
	require.True(t, m.chat.HasInput())
	require.Equal(t, stateChatView, m.state)
	m.state = stateShellView
	enhancements := m.View().KeyboardEnhancements
	require.False(t, enhancements.ReportAllKeysAsEscapeCodes, "embedded shells keep their existing input protocol")
}

func TestShiftFooterUsesContextAndHelpGroups(t *testing.T) {
	m := shiftFooterModel(t)
	enableShiftHints(m)
	m.tree.SetChildren(m.tree.Path(), &sections.Children{Kind: k8s.KindNode, Nodes: []k8s.NodeInfo{{Name: "worker", Schedulable: false}}})
	m.Update(tea.KeyPressMsg{Code: tea.KeyLeftShift, Mod: tea.ModShift})
	footer := ansi.Strip(m.renderFooter())
	require.Contains(t, footer, "C uncordon")
	require.Contains(t, footer, "D drain")
	require.NotContains(t, footer, "S scale")
	m.Update(keyMsg("?"))
	require.Equal(t, stateHelp, m.state)
	require.False(t, m.shiftHeld)
	groups := m.ctx.Keys.Groups()
	titles := make([]string, 0, len(groups))
	for _, g := range groups {
		titles = append(titles, g.Title)
	}
	require.Contains(t, titles, "Inspect & connect")
	require.Contains(t, titles, "Change")
}

// Drive actual terminal bytes through Bubble Tea's decoder, rather than only
// manufacturing KeyPressMsg values. Associated text must preserve typing and
// uppercase bindings when all-key reporting is enabled.
type footerProtocolHarness struct {
	model                                *Model
	revealed, restored, typed, confirmed bool
}

func (h *footerProtocolHarness) Init() tea.Cmd  { return nil }
func (h *footerProtocolHarness) View() tea.View { return tea.NewView("") }
func (h *footerProtocolHarness) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if size, ok := msg.(tea.WindowSizeMsg); ok && size.Width == 0 {
		return h, nil
	} // headless output has no terminal dimensions
	if k, ok := msg.(tea.KeyPressMsg); ok && k.Code == tea.KeyF12 {
		return h, tea.Quit
	}
	h.model.Update(msg) // discard commands: this test never connects to a cluster
	if k, ok := msg.(tea.KeyPressMsg); ok {
		switch k.Code {
		case tea.KeyLeftShift:
			h.revealed = h.revealed || strings.Contains(ansi.Strip(h.model.renderFooter()), "CHANGE")
		case 's':
			h.typed = h.typed || h.model.tree.FilterValue() == "S"
			h.confirmed = h.confirmed || h.model.state == stateScaleInput
		}
	}
	if k, ok := msg.(tea.KeyReleaseMsg); ok && k.Code == tea.KeyLeftShift {
		h.restored = !h.model.shiftHeld
	}
	return h, nil
}

func TestShiftFooterTerminalProtocol(t *testing.T) {
	m := shiftFooterModel(t)
	h := &footerProtocolHarness{model: m}
	input := "\x1b[?31u" + // negotiated enhancements
		"\x1b[57441;2u\x1b[57441;1:3u" + // Shift press/release
		"\x1b[47;1;47u\x1b[115:83;2;83u" + // / then S in the filter
		"\x1b[27u" + // cancel the filter and restore the selected row
		"\x1b[115:83;2;83u" + // S opens scale dialog
		"\x1b[24~" // harness exit (no application shutdown/config writes)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	p := tea.NewProgram(h, tea.WithContext(ctx), tea.WithInput(strings.NewReader(input)), tea.WithOutput(io.Discard), tea.WithEnvironment([]string{"TERM=xterm-kitty"}), tea.WithoutRenderer())
	_, err := p.Run()
	require.NoError(t, err)
	require.True(t, h.revealed, "raw Shift press reveals changes")
	require.True(t, h.restored, "raw Shift release clears hints")
	require.True(t, h.typed, "associated uppercase text reaches the filter")
	require.True(t, h.confirmed, "uppercase shortcut still opens confirmation")
}

func TestShiftFooterPreTransitionModifierFlags(t *testing.T) {
	for _, shift := range []rune{tea.KeyLeftShift, tea.KeyRightShift} {
		m := shiftFooterModel(t)
		enableShiftHints(m)
		// Some terminals report the modifier mask from before the key event:
		// no Shift bit on press, and Shift still set on release.
		m.Update(tea.KeyPressMsg{Code: shift})
		require.Contains(t, ansi.Strip(m.renderFooter()), "CHANGE", "a Shift press must reveal changes even without its modifier bit")
		m.Update(tea.KeyReleaseMsg{Code: shift, Mod: tea.ModShift})
		require.NotContains(t, ansi.Strip(m.renderFooter()), "CHANGE", "a Shift release must restore inspection even with a stale modifier bit")
	}
}

func TestShiftFooterOverlappingKeysAndLateReleases(t *testing.T) {
	for _, preTransition := range []bool{false, true} {
		for _, releaseFirst := range []rune{tea.KeyLeftShift, tea.KeyRightShift} {
			m := shiftFooterModel(t)
			enableShiftHints(m)
			pressMod, lastReleaseMod := tea.ModShift, tea.KeyMod(0)
			if preTransition {
				pressMod, lastReleaseMod = 0, tea.ModShift
			}
			last := tea.KeyRightShift
			if releaseFirst == last {
				last = tea.KeyLeftShift
			}
			sequence := []struct {
				msg  tea.Msg
				held bool
			}{
				{tea.KeyPressMsg{Code: tea.KeyLeftShift, Mod: pressMod}, true},
				{tea.KeyPressMsg{Code: tea.KeyLeftShift, Mod: pressMod, IsRepeat: true}, true},
				{tea.KeyPressMsg{Code: tea.KeyLeftCtrl}, true},
				{tea.KeyReleaseMsg{Code: tea.KeyLeftCtrl}, true},
				{tea.KeyPressMsg{Code: tea.KeyRightShift, Mod: tea.ModShift}, true},
				{tea.KeyReleaseMsg{Code: releaseFirst, Mod: tea.ModShift}, true},
				{tea.KeyReleaseMsg{Code: last, Mod: lastReleaseMod}, false},
				{tea.KeyReleaseMsg{Code: 's', Mod: tea.ModShift}, false},
			}
			for i, step := range sequence {
				_, cmd := m.Update(step.msg)
				require.Nil(t, cmd)
				require.Equal(t, stateMain, m.state)
				require.Equal(t, step.held, strings.Contains(ansi.Strip(m.renderFooter()), "CHANGE"), "pre-transition=%v first=%v step=%d", preTransition, releaseFirst, i)
			}
			m.Update(tea.KeyPressMsg{Code: tea.KeyLeftShift})
			m.Update(tea.BlurMsg{})
			m.Update(tea.FocusMsg{})
			m.Update(tea.KeyReleaseMsg{Code: tea.KeyLeftShift, Mod: tea.ModShift})
			require.False(t, m.shiftHeld, "a release received after focus returns must not reveal changes")
		}
	}
}

// Replay post-transition and pre-transition masks through the real decoder.
// The foot sequence was captured from foot 1.28 with Num Lock enabled.
func TestShiftFooterRawTerminalVariants(t *testing.T) {
	for _, tc := range []struct{ name, press, release string }{
		{"post-transition", "\x1b[57441;2:1u", "\x1b[57441;1:3u"},
		{"pre-transition", "\x1b[57441;1:1u", "\x1b[57441;2:3u"},
		{"foot with num lock", "\x1b[57441;130:1u", "\x1b[57441;129:3u"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := shiftFooterModel(t)
			h := &footerProtocolHarness{model: m}
			input := "\x1b[?31u" + tc.press + tc.release + "\x1b[24~"
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			p := tea.NewProgram(h, tea.WithContext(ctx), tea.WithInput(strings.NewReader(input)), tea.WithOutput(io.Discard), tea.WithEnvironment([]string{"TERM=xterm-kitty"}), tea.WithoutRenderer())
			_, err := p.Run()
			require.NoError(t, err)
			require.True(t, h.revealed)
			require.True(t, h.restored)
			require.False(t, m.shiftHeld)
		})
	}
}
