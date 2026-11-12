package tui

import (
	tea "charm.land/bubbletea/v2"
	"github.com/YunusAndreasson/safina/internal/tui/sections"
)

// Navigation overlays borrow input without replacing the active workspace or
// cancelling its running work. Only choosing a destination tears it down.
func (m *Model) baseState() state {
	// All non-overlay states are already their own base.
	//exhaustive:ignore
	switch m.state {
	case statePalette, stateClusters, stateBrowser:
		return m.navigationReturnState
	case stateHelp:
		return m.helpReturnState
	default:
		return m.state
	}
}

func (m *Model) openResourceSearch() (tea.Model, tea.Cmd) {
	m.navigationReturnState = m.state
	m.searchRequest++
	m.searchLoading = false
	// Resource types first, so `:node` lands on Nodes; then the tools; then
	// every resource loaded so far.
	entries := m.tree.TypeEntries()
	entries = append(entries, sections.PaletteEntry{Kind: "Incoming", Name: "Incoming proposals", Hint: "Review agent proposals and broker outcomes"}, sections.PaletteEntry{Kind: "History", Name: "Logbook", Hint: "What you did and saw, saved across sessions"})
	entries = append(entries, sections.BuildPaletteIndex(m.tree.Cache())...)
	m.palette.Open(entries)
	m.recordSelection("search")
	m.state = statePalette
	return m, nil
}

func (m *Model) leaveNavigationForDestination() {
	m.state = m.navigationReturnState
	m.cleanupImmersiveState()
	m.state = stateMain
}
