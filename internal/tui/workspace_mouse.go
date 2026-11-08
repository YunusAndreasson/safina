package tui

import (
	tea "charm.land/bubbletea/v2"
	"github.com/yunus/safina/internal/tui/sections"
)

// A pointer click activates a view, rather than typing its keyboard shortcut
// into whichever input previously owned focus. Re-selecting a tab is harmless.
func (m *Model) activateWorkspaceTab(target sections.WorkspaceTab) (tea.Model, tea.Cmd) {
	if m.tree.FilterActive() {
		m.tree.Update(tea.KeyPressMsg{Code: tea.KeyEnter}, m.ctx)
	}
	if m.activeWorkspaceTab() == target {
		m.treeFocusInImmersive = false
		m.tree.Focused = false
		if m.state == stateMain {
			m.peekWorkspace = true
		}
		if m.state == stateChatView {
			m.chat.SetFocus(true)
		}
		return m, nil
	}
	synth, ok := tabActivationKey(target)
	if !ok {
		return m, nil
	}
	m.cleanupImmersiveState()
	m.state = stateMain
	m.peekWorkspace = true
	m.treeFocusInImmersive = false
	m.tree.Focused = false
	if target == sections.WSTabInspector {
		return m, nil
	}
	return m.handleKey(synth)
}
