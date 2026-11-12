package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/YunusAndreasson/safina/internal/config"
	"github.com/YunusAndreasson/safina/internal/k8s"
	"github.com/YunusAndreasson/safina/internal/tui/sections"
	"github.com/YunusAndreasson/safina/internal/tui/textw"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// The k9s reading keys on a list row open a modal over the list: the list
// stays where it is, and Esc closes the modal with the cursor unchanged.
func TestReadingKeyOnARowOpensTheModalOverTheList(t *testing.T) {
	for _, tc := range []struct {
		verb string
		st   state
	}{
		{"d", stateSheetView},
		{"y", stateDescribeView},
		{"l", stateLogView},
	} {
		m := pageModel(t)
		depth, row := len(m.tree.Path()), m.tree.SelectedRef().Name
		m.handleKey(keyMsg(tc.verb))
		require.Equal(t, tc.st, m.state, tc.verb)
		require.True(t, m.modalOpen(), tc.verb)
		require.Len(t, m.tree.Path(), depth, "%s: the list stays; no page opens", tc.verb)
		require.Contains(t, ansi.Strip(m.renderFooter()), "esc close", tc.verb)
		m.handleKey(keyMsg("esc"))
		require.Equal(t, stateMain, m.state, tc.verb)
		require.False(t, m.modal, tc.verb)
		require.Len(t, m.tree.Path(), depth, tc.verb)
		require.Equal(t, row, m.tree.SelectedRef().Name, tc.verb)
		m.stopLogStream()
	}
}

// m opens the row's metrics in the modal, like the other views.
func TestMetricsKeyOpensTheModal(t *testing.T) {
	m := pageModel(t)
	depth := len(m.tree.Path())
	m.handleKey(keyMsg("m"))
	require.Equal(t, stateMetricsView, m.state)
	require.True(t, m.modalOpen())
	require.Len(t, m.tree.Path(), depth)
	require.Equal(t, "api", m.metrics.Ref.Name)
}

func TestLogbookScopesFoldersToNamespaceInsteadOfInventingResource(t *testing.T) {
	m := shiftFooterModel(t)
	m.tree.SetEnabledResources(config.DefaultEnabledResources())
	m.tree.Push(sections.NodeRef{Kind: k8s.KindNamespaceFolder, Name: "shop", Namespace: "shop"})
	m.tree.SetChildren(m.tree.Path(), &sections.Children{Kind: k8s.KindApp})
	m.tree.SetCursorByRef(k8s.KindWorkloadsFold, "", "Workloads")
	filter := m.selectedHistoryFilter()
	require.Equal(t, "shop", filter.Namespace)
	require.Empty(t, filter.Resource)
	m.tree.Push(m.tree.SelectedRef())
	m.tree.SetChildren(m.tree.Path(), &sections.Children{Kind: k8s.KindApp, Apps: []k8s.AppInfo{{Kind: k8s.AppKindDeployment, Name: "api", Namespace: "shop"}}})
	filter = m.selectedHistoryFilter()
	require.Equal(t, "shop", filter.Namespace)
	require.Equal(t, "Deployment/api", filter.Resource)
}

func TestMetricsDescribeShortcutStillOpensResourceYAML(t *testing.T) {
	m := shiftFooterModel(t)
	m.Update(keyMsg("m"))
	require.False(t, m.metrics.ShowActivity)
	m.Update(keyMsg("y"))
	require.Equal(t, stateDescribeView, m.state)
	require.Nil(t, m.choices)
}

func TestFormAndChoiceShortcutsUseSharedInk(t *testing.T) {
	m := shiftFooterModel(t)
	th := m.ctx.Theme
	m.openNotifications()
	require.Contains(t, m.choicesCard(), strings.TrimSuffix(th.HelpKey.Render("esc"), "\x1b[m"))
	require.Contains(t, m.renderScaleModal(), strings.TrimSuffix(th.Danger.Render("↵"), "\x1b[m"))
	require.Contains(t, m.renderConfirmModal("restart", "api"), strings.TrimSuffix(th.HelpKey.Render("esc"), "\x1b[m"))
	require.Contains(t, ansi.Strip(m.renderHelpModal()), "Navigate & tools")
}

// The modal is a closed box: every row of it is the page's width, and its
// corners sit at the same column top and bottom.
func TestModalBoxIsSquare(t *testing.T) {
	m := pageModel(t)
	m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	m.handleKey(keyMsg("d"))
	rows := strings.Split(ansi.Strip(m.View().Content), "\n")
	top, bottom := -1, -1
	for i, r := range rows {
		if strings.Contains(r, "╭─ Describe") {
			top = i
		}
		if top >= 0 && i > top && strings.Contains(r, "╰─") {
			bottom = i
			break
		}
	}
	require.Positive(t, top)
	require.Greater(t, bottom, top)
	col := func(row string, i int) int { return textw.Width(row[:i]) }
	require.Equal(t, col(rows[top], strings.Index(rows[top], "╭")), col(rows[bottom], strings.Index(rows[bottom], "╰")))
	require.Equal(t, col(rows[top], strings.LastIndex(rows[top], "╮")), col(rows[bottom], strings.LastIndex(rows[bottom], "╯")))
}

// q closes every viewer in the modal, as in less.
func TestQClosesTheModal(t *testing.T) {
	for _, verb := range []string{"d", "y", "l"} {
		m := pageModel(t)
		m.handleKey(keyMsg(verb))
		require.True(t, m.modalOpen(), verb)
		m.handleKey(keyMsg("q"))
		require.False(t, m.modalOpen(), verb)
		require.Equal(t, stateMain, m.state, verb)
		m.stopLogStream()
	}
}

// The modal keeps the row it is about in sight: the highlighted row stays
// on screen, right above the modal's top edge.
func TestModalLeavesItsRowInSight(t *testing.T) {
	m := pageModel(t)
	m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	row := m.tree.SelectedRef().Name
	m.handleKey(keyMsg("d"))
	rows := strings.Split(ansi.Strip(m.View().Content), "\n")
	top := -1
	for i, r := range rows {
		if strings.Contains(r, "╭─ Describe") {
			top = i
			break
		}
	}
	require.Positive(t, top)
	require.Contains(t, rows[top-1], " "+row+" ", "the highlighted row sits just above the modal")
}

// A key that does nothing on the highlighted row says so on the location
// row, and the next key clears it.
func TestInapplicableKeySaysWhy(t *testing.T) {
	m := pageModel(t)
	m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	m.tree.SetChildren(m.tree.Path(), &sections.Children{Kind: k8s.KindConfigMap, ConfigMaps: []k8s.ConfigMapInfo{{Name: "settings", Namespace: "shop"}}})
	m.handleKey(keyMsg("l"))
	require.Equal(t, stateMain, m.state)
	require.Contains(t, ansi.Strip(m.View().Content), "no logs for ConfigMaps")
	m.handleKey(keyMsg("down"))
	require.NotContains(t, ansi.Strip(m.View().Content), "no logs for ConfigMaps")
}
