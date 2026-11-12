package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

// Pasted text belongs to the focused input; it must never become shortcuts.
func (m *Model) handlePaste(msg tea.PasteMsg) tea.Cmd {
	if m.choices != nil {
		return nil
	}
	if m.state == stateMain && m.tree.FilterActive() {
		cmd := m.tree.Update(msg, m.ctx)
		return tea.Batch(cmd, m.syncSelection())
	}
	var cmd tea.Cmd
	// Only input-bearing states accept paste.
	//exhaustive:ignore
	switch m.state {
	case stateChatView:
		cmd, _, _ = m.chat.Update(msg, m.ctx)
	case stateScaleInput:
		m.scaleError = ""
		m.scaleInput, cmd = m.scaleInput.Update(msg)
	case statePortForwardInput:
		m.pfError = ""
		m.pfInput, cmd = m.pfInput.Update(msg)
	case statePalette:
		if !m.palette.ActionsActive() {
			cmd = m.palette.UpdateQuery(tea.PasteMsg{Content: singleLinePaste(msg.Content)})
		}
	case stateBrowser:
		cmd = m.browser.UpdateQuery(tea.PasteMsg{Content: singleLinePaste(msg.Content)})
	case stateClusters:
		cmd = m.clusters.UpdateQuery(tea.PasteMsg{Content: singleLinePaste(msg.Content)})
	case stateLogView:
		cmd, _ = m.logs.Update(msg, m.ctx)
	case stateEventsView:
		cmd, _ = m.events.Update(msg, m.ctx)
	case stateDescribeView:
		cmd, _ = m.describe.Update(msg, m.ctx)
	case stateShellView:
		m.shell.Paste(msg.Content)
	default:
	}
	return cmd
}

func singleLinePaste(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return ' '
		}
		if r < ' ' || r == 127 {
			return -1
		}
		return r
	}, s)
}
