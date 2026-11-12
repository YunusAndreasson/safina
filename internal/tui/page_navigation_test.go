package tui

import (
	"math/rand"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"

	"github.com/YunusAndreasson/safina/internal/k8s"
	"github.com/YunusAndreasson/safina/internal/tui/sections"
)

var (
	leftKey  = tea.KeyPressMsg{Code: tea.KeyLeft}
	rightKey = tea.KeyPressMsg{Code: tea.KeyRight}
)

// pageModel is a cluster list with two apps; the first app's page has two
// pods loaded.
func pageModel(t *testing.T) *Model {
	t.Helper()
	m := shiftFooterModel(t)
	m.tree.SetChildren(m.tree.Path(), &sections.Children{Kind: k8s.KindApp, Apps: []k8s.AppInfo{
		{Kind: k8s.AppKindDeployment, Namespace: "shop", Name: "api", UID: "api"},
		{Kind: k8s.AppKindDeployment, Namespace: "shop", Name: "web", UID: "web"},
	}})
	return m
}

func loadPods(m *Model) {
	m.tree.SetChildren(m.tree.Path(), &sections.Children{Kind: k8s.KindPod, Pods: []k8s.PodInfo{
		{Name: "api-1", Namespace: "shop", Status: "Running"},
		{Name: "api-2", Namespace: "shop", Status: "CrashLoopBackOff"},
	}})
}

func TestEnterOpensResourcePageOnItsChildrenAndEscReturnsToTheRow(t *testing.T) {
	for _, open := range []tea.KeyPressMsg{keyMsg("enter"), rightKey} {
		m := pageModel(t)
		m.handleKey(keyMsg("down"))
		depth := len(m.tree.Path())
		m.handleKey(open)
		require.Equal(t, k8s.KindApp, m.tree.Focus().Kind)
		require.Equal(t, "web", m.tree.Focus().Name)
		require.Equal(t, sections.WSTabChildren, m.pageTab)
		require.Equal(t, "demo", m.backLabel())
		for _, back := range []tea.KeyPressMsg{keyMsg("esc"), leftKey} {
			m.handleKey(back)
			require.Len(t, m.tree.Path(), depth)
			require.Equal(t, "web", m.tree.SelectedRef().Name, "back lands on the row that was opened")
			m.handleKey(open)
		}
	}
}

// The arrows mean one thing everywhere: → opens the highlighted row and ←
// goes back, from any tab. Tabs have their own keys.
func TestArrowsOpenAndGoBackAndLeaveTheTabsAlone(t *testing.T) {
	m := pageModel(t)
	depth := len(m.tree.Path())
	m.handleKey(rightKey) // on a list, → opens the row
	loadPods(m)
	require.Len(t, m.tree.Path(), depth+1)
	require.Equal(t, sections.WSTabChildren, m.pageTab)
	m.handleKey(rightKey)
	require.Len(t, m.tree.Path(), depth+2, "→ on a pod opens its containers")
	m.handleKey(leftKey)
	m.handleKey(leftKey)
	require.Len(t, m.tree.Path(), depth, "← goes back a page at a time")
}

// On a page's Pods tab the reading keys act on the highlighted pod, in the
// modal; the page keeps its tab underneath.
func TestVerbOnAPodRowOpensTheModalAndEscReturnsToTheRow(t *testing.T) {
	m := pageModel(t)
	m.handleKey(keyMsg("enter"))
	loadPods(m)
	m.handleKey(keyMsg("down"))
	row := m.tree.SelectedRef().Name
	m.handleKey(keyMsg("l"))
	require.Equal(t, k8s.KindApp, m.tree.Focus().Kind, "the page stays")
	require.Equal(t, row, m.logSource.Name, "the logs are the highlighted pod's")
	require.Equal(t, stateLogView, m.state)
	require.Equal(t, sections.WSTabChildren, m.pageTab)
	m.handleKey(keyMsg("right"))
	require.Equal(t, stateLogView, m.state, "the tab keys do not move the page under the modal")
	m.handleKey(keyMsg("esc"))
	require.Equal(t, stateMain, m.state)
	require.Equal(t, sections.WSTabChildren, m.pageTab)
	require.Equal(t, row, m.tree.SelectedRef().Name)
	m.stopLogStream()
}

func TestToolPageReturnsToTheViewItCovered(t *testing.T) {
	m := pageModel(t)
	m.handleKey(keyMsg("enter"))
	m.showTab(sections.WSTabEvents)
	require.Equal(t, stateEventsView, m.state)
	m.handleKey(keyMsg("i"))
	require.Equal(t, stateChatView, m.state)
	require.Equal(t, "Events", m.backLabel())
	m.handleKey(keyMsg("esc"))
	require.Equal(t, stateEventsView, m.state)
	m.handleKey(keyMsg("h"))
	require.Equal(t, stateHotView, m.state)
	m.handleKey(keyMsg("esc"))
	require.Equal(t, stateEventsView, m.state)
}

func TestLeftPreservesFilterEditingButGoesBackAfterCommit(t *testing.T) {
	m := pageModel(t)
	before := len(m.tree.Path())
	m.handleKey(keyMsg("/"))
	m.handleKey(keyMsg("a"))
	m.handleKey(leftKey)
	require.True(t, m.tree.FilterActive())
	require.Len(t, m.tree.Path(), before)
	m.handleKey(keyMsg("enter"))
	require.False(t, m.tree.FilterActive())
	m.handleKey(leftKey)
	require.Len(t, m.tree.Path(), before-1)
	require.Empty(t, m.tree.FilterValue())
	for range before {
		m.handleKey(leftKey)
	}
	require.Len(t, m.tree.Path(), 1, "back at the Harbour is harmless")
	require.Empty(t, m.backLabel())
}

// Enter on a thing that holds nothing (a ConfigMap) describes it in the
// modal: there is no list to open.
func TestEnterOnALeafDescribesItInTheModal(t *testing.T) {
	m := pageModel(t)
	m.tree.SetChildren(m.tree.Path(), &sections.Children{Kind: k8s.KindConfigMap, ConfigMaps: []k8s.ConfigMapInfo{{Name: "settings", Namespace: "shop"}}})
	depth := len(m.tree.Path())
	m.handleKey(keyMsg("enter"))
	require.Len(t, m.tree.Path(), depth)
	require.Equal(t, stateSheetView, m.state)
	require.Equal(t, "settings", m.sheet.Ref().Name)
}

func TestDrainEscapeLeavesOperationRunningAndCompletionPreservesNavigation(t *testing.T) {
	m := pageModel(t)
	cancelled := false
	m.drain.Reset("node", func() { cancelled = true })
	m.state = stateDrainProgress
	m.handleKey(keyMsg("esc"))
	require.False(t, cancelled)
	require.Equal(t, stateMain, m.state)
	m.handleKey(keyMsg("i"))
	require.Equal(t, stateChatView, m.state)
	m.Update(sections.DrainProgressMsg{Event: k8s.DrainEvent{Done: true}})
	require.Equal(t, stateChatView, m.state, "background completion must not steal focus")
}

func TestDrainInterruptCancels(t *testing.T) {
	m := pageModel(t)
	cancelled := false
	m.drain.Reset("node", func() { cancelled = true })
	m.state = stateDrainProgress
	m.handleKey(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	require.True(t, cancelled)
	require.Equal(t, stateDrainProgress, m.state)
}

// TestBackLabelAlwaysNamesWhereEscLands walks the pages at random and, before
// every Esc, checks the footer's promise against where Esc actually goes.
func TestBackLabelAlwaysNamesWhereEscLands(t *testing.T) {
	rng := rand.New(rand.NewSource(7)) //nolint:gosec // Reproducible test input.
	keys := []tea.KeyPressMsg{
		keyMsg("enter"), rightKey, keyMsg("tab"),
		{Code: tea.KeyTab, Mod: tea.ModShift},
		keyMsg("down"), keyMsg("up"), keyMsg("1"), keyMsg("2"), keyMsg("3"), keyMsg("l"), keyMsg("y"), keyMsg("d"), keyMsg("h"),
	}
	for run := range 40 {
		m := pageModel(t)
		for step := range 60 {
			if m.tree.Focus().Kind == k8s.KindApp && m.tree.Children() == nil {
				loadPods(m)
			}
			if rng.Intn(3) > 0 {
				m.handleKey(keys[rng.Intn(len(keys))])
				continue
			}
			if m.modalOpen() {
				// The modal's Esc closes it and leaves the page as it was.
				depth := len(m.tree.Path())
				m.handleKey(keyMsg("esc"))
				require.False(t, m.modalOpen(), "run %d step %d", run, step)
				require.Len(t, m.tree.Path(), depth, "run %d step %d", run, step)
				continue
			}
			label := m.backLabel()
			beforeState := m.state
			m.handleKey(keyMsg("esc"))
			var landed string
			switch {
			case isToolState(beforeState):
				if m.tree.OnResourcePage() && m.pageTab.IsReading() {
					landed = m.pageTab.Title(m.tree.Focus().Kind)
				} else {
					landed = pageName(m.tree.Focus())
				}
			default:
				landed = pageName(m.tree.Focus())
			}
			if label == "" {
				require.Len(t, m.tree.Path(), 1, "run %d step %d: no label only at the Harbour", run, step)
				continue
			}
			require.Equal(t, label, landed, "run %d step %d: footer promised %q", run, step, label)
		}
		m.stopLogStream()
	}
}
