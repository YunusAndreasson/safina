package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/YunusAndreasson/safina/internal/tui/sections"
)

// A pointer click on a tab opens it, rather than typing its keyboard
// shortcut into whichever input previously owned focus. Re-selecting the
// open tab is harmless.
func (m *Model) activateWorkspaceTab(target sections.WorkspaceTab) (tea.Model, tea.Cmd) {
	if m.tree.FilterActive() {
		m.tree.Update(tea.KeyPressMsg{Code: tea.KeyEnter}, m.ctx)
	}
	if m.pageTab == target && !isToolState(m.state) {
		return m, nil
	}
	return m, m.showTab(target)
}
