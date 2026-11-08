package tui

import (
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
	"github.com/yunus/safina/internal/k8s"
	"github.com/yunus/safina/internal/tui/sections"
)

func TestArrowKeysCrossPaneBoundary(t *testing.T) {
	m := shiftFooterModel(t)
	selected := m.tree.SelectedRef()
	m.handleKey(tea.KeyPressMsg{Code: tea.KeyRight})
	require.True(t, m.workspaceOwnsFocus())
	require.Equal(t, sections.WSTabInspector, m.activeWorkspaceTab())
	require.Equal(t, selected, m.tree.SelectedRef())
	m.handleKey(tea.KeyPressMsg{Code: tea.KeyLeft})
	require.False(t, m.workspaceOwnsFocus())
	require.True(t, m.tree.Focused)
	// Enter the pane, then continue through tabs; Left returns one tab at a time.
	m.handleKey(tea.KeyPressMsg{Code: tea.KeyRight})
	m.handleKey(tea.KeyPressMsg{Code: tea.KeyRight})
	require.Equal(t, sections.WSTabMetrics, m.activeWorkspaceTab())
	m.handleKey(tea.KeyPressMsg{Code: tea.KeyLeft})
	require.Equal(t, sections.WSTabInspector, m.activeWorkspaceTab())
	require.True(t, m.workspaceOwnsFocus())
	m.handleKey(tea.KeyPressMsg{Code: tea.KeyLeft})
	require.False(t, m.workspaceOwnsFocus())
}

func TestTreeRightStartsAtInspectorAndPreservesTyping(t *testing.T) {
	m := shiftFooterModel(t)
	m.state = stateLogView
	m.treeFocusInImmersive = true
	m.handleKey(tea.KeyPressMsg{Code: tea.KeyRight})
	require.Equal(t, stateMain, m.state)
	require.True(t, m.peekWorkspace)
	m.handleKey(tea.KeyPressMsg{Code: tea.KeyLeft})
	m.handleKey(keyMsg("/"))
	m.handleKey(tea.KeyPressMsg{Code: tea.KeyRight})
	require.True(t, m.tree.FilterActive())
	require.False(t, m.workspaceOwnsFocus())
}

func TestTreeLeftGoesToParentWithEitherPaneState(t *testing.T) {
	for _, immersive := range []bool{false, true} {
		m := shiftFooterModel(t)
		m.tree.Push(sections.NodeRef{Kind: "NamespaceFolder", Name: "shop", Namespace: "shop"})
		m.tree.Push(sections.NodeRef{Kind: "WorkloadsFold", Name: "Workloads"})
		m.tree.Focused = true
		if immersive {
			m.state = stateLogView
			m.treeFocusInImmersive = true
		}
		before := len(m.tree.Path())
		m.handleKey(tea.KeyPressMsg{Code: tea.KeyLeft})
		require.Len(t, m.tree.Path(), before-1)
		require.Equal(t, "shop", m.tree.Focus().Name)
		require.False(t, m.workspaceOwnsFocus())
		if immersive {
			require.Equal(t, stateLogView, m.state, "tree navigation must not act like closing the log pane")
		}
		for range before {
			m.handleKey(tea.KeyPressMsg{Code: tea.KeyLeft})
		}
		require.Len(t, m.tree.Path(), 1, "Left at the root is harmless")
	}
}

func TestTreeLeftPreservesFilterEditingButPopsAfterCommit(t *testing.T) {
	m := shiftFooterModel(t)
	before := len(m.tree.Path())
	m.handleKey(keyMsg("/"))
	m.handleKey(keyMsg("a"))
	m.handleKey(tea.KeyPressMsg{Code: tea.KeyLeft})
	require.True(t, m.tree.FilterActive())
	require.Len(t, m.tree.Path(), before)
	m.handleKey(keyMsg("enter"))
	require.False(t, m.tree.FilterActive())
	m.handleKey(tea.KeyPressMsg{Code: tea.KeyLeft})
	require.Len(t, m.tree.Path(), before-1)
	require.Empty(t, m.tree.FilterValue())
}

func TestRepeatedWorkspaceEscapeRestoresTreeWithoutRender(t *testing.T) {
	for _, section := range []state{stateMain, stateMetricsView, stateLogView, stateEventsView, stateDescribeView, stateHotView, stateForwardsList, stateChatView} {
		t.Run(fmt.Sprint(section), func(t *testing.T) {
			m := shiftFooterModel(t)
			for range 100 {
				m.handleKey(tea.KeyPressMsg{Code: tea.KeyRight})
				m.state = section
				m.chat.SetFocus(section == stateChatView)
				depth := len(m.tree.Path())
				m.handleKey(keyMsg("esc"))
				require.Equal(t, stateMain, m.state)
				require.False(t, m.workspaceOwnsFocus())
				require.True(t, m.tree.Focused)
				require.Len(t, m.tree.Path(), depth, "closing a pane must not pop the tree")
				m.handleKey(keyMsg("tab"))
				require.True(t, m.workspaceOwnsFocus())
				m.handleKey(keyMsg("tab"))
				require.False(t, m.workspaceOwnsFocus())
			}
		})
	}
}

func TestDrainTabRoutesArrowsToVisibleTree(t *testing.T) {
	m := shiftFooterModel(t)
	m.tree.SetChildren(m.tree.Path(), &sections.Children{Kind: "App", Apps: []k8s.AppInfo{
		{Kind: k8s.AppKindDeployment, Name: "first", Namespace: "shop"},
		{Kind: k8s.AppKindDeployment, Name: "second", Namespace: "shop"},
	}})
	m.state = stateDrainProgress
	m.handleKey(keyMsg("tab"))
	require.False(t, m.workspaceOwnsFocus())
	before := m.tree.SelectedRef()
	m.handleKey(keyMsg("down"))
	require.NotEqual(t, before, m.tree.SelectedRef())
	m.handleKey(keyMsg("up"))
	require.Equal(t, before, m.tree.SelectedRef())
	m.handleKey(keyMsg("tab"))
	require.True(t, m.workspaceOwnsFocus())
	require.Equal(t, stateDrainProgress, m.state)
}

func TestDrainEscapeLeavesOperationRunningAndCompletionPreservesNavigation(t *testing.T) {
	m := shiftFooterModel(t)
	cancelled := false
	m.drain.Reset("node", func() { cancelled = true })
	m.state = stateDrainProgress
	m.handleKey(keyMsg("esc"))
	require.False(t, cancelled)
	require.False(t, m.workspaceOwnsFocus())
	require.Equal(t, stateMain, m.state)
	m.handleKey(keyMsg("i"))
	require.Equal(t, stateChatView, m.state)
	m.Update(sections.DrainProgressMsg{Event: k8s.DrainEvent{Done: true}})
	require.Equal(t, stateChatView, m.state, "background completion must not steal focus")
}

func TestDrainInterruptStillCancelsWithTreeFocus(t *testing.T) {
	m := shiftFooterModel(t)
	cancelled := false
	m.drain.Reset("node", func() { cancelled = true })
	m.state = stateDrainProgress
	m.handleKey(keyMsg("tab"))
	m.handleKey(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	require.True(t, cancelled)
	require.Equal(t, stateDrainProgress, m.state)
}
