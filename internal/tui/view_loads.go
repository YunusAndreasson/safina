package tui

import tea "charm.land/bubbletea/v2"

type resourceViewResultMsg struct {
	view    state
	request uint64
	cluster string
	result  tea.Msg
}

// Requests can finish out of order even within one cluster. A refresh, reveal
// or different selection supersedes its predecessor without blocking input.
func (m *Model) loadResourceView(view state, cmd tea.Cmd) tea.Cmd {
	if cmd == nil {
		return nil
	}
	if m.viewRequests == nil {
		m.viewRequests = make(map[state]uint64)
	}
	m.viewRequests[view]++
	request, cluster := m.viewRequests[view], m.ctx.ActiveContext
	return func() tea.Msg {
		return resourceViewResultMsg{view: view, request: request, cluster: cluster, result: cmd()}
	}
}
