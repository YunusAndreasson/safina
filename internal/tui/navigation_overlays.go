package tui

import (
	tea "charm.land/bubbletea/v2"
	"github.com/yunus/safina/internal/tui/sections"
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
	entries := sections.BuildPaletteIndex(m.tree.Cache())
	entries = append([]sections.PaletteEntry{{Kind: "Incoming", Name: "Incoming proposals", Hint: "Review agent proposals and broker outcomes"}, {Kind: "History", Name: "Logbook", Hint: "What you did and saw, saved across sessions"}}, entries...)
	m.palette.Open(entries)
	m.recordSelection("search")
	m.state = statePalette
	return m, nil
}

func (m *Model) leaveNavigationForDestination() {
	m.state = m.navigationReturnState
	m.returnToMain()
	m.connectedBack = nil
}
