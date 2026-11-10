package tui

import (
	"testing"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"

	"github.com/YunusAndreasson/safina/internal/config"
	"github.com/YunusAndreasson/safina/internal/k8s"
)

// An idle screen must not redraw for a spinner nobody sees: every tick
// renders a full frame. The tick ends when the last frame drew no spinner,
// comes back with the next message, and keeps going while one is drawn.
func TestSpinnerTicksOnlyWhileShown(t *testing.T) {
	m := NewModel(&k8s.Manager{}, &config.AppConfig{})
	t.Cleanup(m.ctx.Cancel)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 36})

	m.View() // nothing loading: no spinner on screen
	require.False(t, m.spinnerShown)
	_, cmd := m.Update(m.ctx.Spinner.Tick())
	require.Nil(t, cmd, "an unseen spinner must stop ticking")
	require.True(t, m.spinnerStopped)

	m.Update(clockMsg{})
	require.True(t, m.spinnerStopped, "the clock never starts a load, so it does not restart the spinner")

	_, cmd = m.Update(keyMsg("down"))
	require.NotNil(t, cmd)
	require.False(t, m.spinnerStopped)
	require.True(t, hasSpinnerTick(cmd), "the next message restarts the tick")

	m.ctx.SpinnerView() // a section drew the spinner in this frame
	m.spinnerShown = m.ctx.TakeSpinnerDrawn()
	_, cmd = m.Update(m.ctx.Spinner.Tick())
	require.NotNil(t, cmd, "a visible spinner keeps animating")
	require.False(t, m.spinnerStopped)
	require.False(t, m.ctx.TakeSpinnerDrawn(), "the draw record resets every frame")
}

// hasSpinnerTick runs cmd (unwrapping batches) and reports whether it
// produced a spinner tick.
func hasSpinnerTick(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	switch msg := cmd().(type) {
	case spinner.TickMsg:
		return true
	case tea.BatchMsg:
		for _, c := range msg {
			if hasSpinnerTick(c) {
				return true
			}
		}
	}
	return false
}
