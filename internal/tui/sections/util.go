package sections

import tea "charm.land/bubbletea/v2"

// emit wraps a value as a tea.Cmd that yields it as a tea.Msg.
func emit(msg tea.Msg) tea.Cmd { return func() tea.Msg { return msg } }

func clamp(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
