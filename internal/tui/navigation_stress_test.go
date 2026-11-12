package tui

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Exercise the real terminal decoder and root Update without rendering between
// keys. Network commands are deliberately held: pending API work must not be
// required for the next navigation key to take effect.
type navigationProtocolHarness struct {
	model       *Model
	keys        int
	cycles      int
	cycleLength int
	t           *testing.T
}

func (h *navigationProtocolHarness) Init() tea.Cmd  { return nil }
func (h *navigationProtocolHarness) View() tea.View { return tea.NewView("") }
func (h *navigationProtocolHarness) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return h, nil
	}
	if k.Code == tea.KeyF12 {
		return h, tea.Quit
	}
	h.model.Update(k)
	h.keys++
	if h.keys%h.cycleLength == 0 {
		assert.Equal(h.t, stateMain, h.model.state)
		assert.False(h.t, h.model.tree.OnResourcePage())
		assert.Len(h.t, h.model.tree.Path(), 2)
		h.cycles++
	}
	return h, nil
}

func TestNavigationTerminalBurst(t *testing.T) {
	for _, tc := range []struct {
		name, prefix, cycle string
		length              int
	}{
		// open, next tab, previous tab, up, down, ← out, open, next tab,
		// ← out to the list from Details.
		{"legacy", "", "\x1b[C\x09\x1b[Z\x1b[A\x1b[B\x1b[D\x1b[C\x09\x1b[D", 9},
		// open, next tab, previous tab, search, close it, up, down, Esc out,
		// open, next tab, ← out to the list from Details.
		{"kitty", "\x1b[?31u", "\x1b[57351u\x1b[9u\x1b[9;2u\x1b[112;5u\x1b[27u\x1b[57352u\x1b[57353u\x1b[27u\x1b[57351u\x1b[9u\x1b[57350u", 11},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &navigationProtocolHarness{model: shiftFooterModel(t), t: t, cycleLength: tc.length}
			input := tc.prefix + strings.Repeat(tc.cycle, 250) + "\x1b[24~"
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			p := tea.NewProgram(h, tea.WithContext(ctx), tea.WithInput(strings.NewReader(input)), tea.WithOutput(io.Discard), tea.WithEnvironment([]string{"TERM=xterm-kitty"}), tea.WithoutRenderer())
			_, err := p.Run()
			require.NoError(t, err)
			require.Equal(t, 250, h.cycles)
			require.Equal(t, 250*tc.length, h.keys)
		})
	}
}

// Legacy Esc is a standalone byte, whereas Esc immediately followed by Tab
// encodes Alt+Tab. Allow the decoder's ambiguity timeout around standalone Esc.
func TestNavigationLegacyEscape(t *testing.T) {
	h := &navigationProtocolHarness{model: shiftFooterModel(t), t: t, cycleLength: 5}
	reader, writer := io.Pipe()
	defer func() { require.NoError(t, reader.Close()) }()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	go func() {
		defer func() { assert.NoError(t, writer.Close()) }()
		for range 10 {
			if _, err := io.WriteString(writer, "\x1b[C\x1b[C"); err != nil {
				return
			}
			time.Sleep(100 * time.Millisecond)
			if _, err := io.WriteString(writer, "\x1b"); err != nil {
				return
			}
			time.Sleep(100 * time.Millisecond)
			if _, err := io.WriteString(writer, "\x09\x09"); err != nil {
				return
			}
		}
		_, _ = io.WriteString(writer, "\x1b[24~")
	}()
	p := tea.NewProgram(h, tea.WithContext(ctx), tea.WithInput(reader), tea.WithOutput(io.Discard), tea.WithEnvironment([]string{"TERM=xterm-256color"}), tea.WithoutRenderer())
	_, err := p.Run()
	require.NoError(t, err)
	require.Equal(t, 10, h.cycles)
	require.Equal(t, 50, h.keys)
}
