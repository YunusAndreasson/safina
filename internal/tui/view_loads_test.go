package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/YunusAndreasson/safina/internal/k8s"
	"github.com/YunusAndreasson/safina/internal/tui/sections"
	"github.com/stretchr/testify/require"
)

func TestLateResourceViewRepliesCannotReplaceNewerSelectionOrRefresh(t *testing.T) {
	for _, view := range []state{stateDescribeView, stateEventsView} {
		m := shiftFooterModel(t)
		m.state = view
		makeReply := func(label string) tea.Cmd {
			return func() tea.Msg {
				if view == stateDescribeView {
					return sections.DescribeLoadedMsg{YAML: "source: " + label}
				}
				return sections.EventsLoadedMsg{Events: []k8s.EventInfo{{Reason: "Started", Message: label}}}
			}
		}
		m.describe.Reset("Deployment", "new", "shop")
		m.events.Reset("Deployment", "new", "shop")
		old := m.loadResourceView(view, makeReply("old selection"))()
		newest := m.loadResourceView(view, makeReply("new selection"))()
		m.Update(newest)
		m.Update(old)
		if view == stateDescribeView {
			require.Contains(t, m.describe.YankPayload(), "new selection")
			require.NotContains(t, m.describe.YankPayload(), "old selection")
		} else {
			require.Contains(t, m.events.YankPayload(), "new selection")
			require.NotContains(t, m.events.YankPayload(), "old selection")
		}
		pending := m.loadResourceView(view, makeReply("during search"))()
		m.handleKey(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
		m.Update(pending)
		require.Equal(t, statePalette, m.state)
		m.handleKey(keyMsg("esc"))
		require.Equal(t, view, m.state)
		if view == stateDescribeView {
			require.Contains(t, m.describe.YankPayload(), "during search")
		} else {
			require.Contains(t, m.events.YankPayload(), "during search")
		}
	}
}

func TestRefreshOnlySupersedesResourceLoadOutsideFilter(t *testing.T) {
	for _, view := range []state{stateDescribeView, stateEventsView} {
		m := shiftFooterModel(t)
		m.state = view
		m.describe.Reset("Deployment", "api", "shop")
		m.events.Reset("Deployment", "api", "shop")
		m.handleKey(keyMsg("r"))
		require.Equal(t, uint64(1), m.viewRequests[view])
		m.handleKey(keyMsg("/"))
		m.handleKey(keyMsg("r"))
		require.Equal(t, uint64(1), m.viewRequests[view], "typing r is not another load")
	}
}

func TestTabAndShiftTabCycleThePageAndComeBackAround(t *testing.T) {
	m := pageModel(t)
	m.handleKey(keyMsg("enter"))
	loadPods(m)
	tabs := m.pageTabs()
	for range 20 {
		for range tabs {
			m.Update(keyMsg("tab"))
		}
		require.Equal(t, sections.WSTabChildren, m.pageTab, "a full cycle returns to the first tab")
		m.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
		m.Update(keyMsg("tab"))
		require.Equal(t, sections.WSTabChildren, m.pageTab)
	}
	m.stopLogStream()
}
