package tui

import (
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/types"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/YunusAndreasson/safina/internal/config"
	"github.com/YunusAndreasson/safina/internal/history"
	"github.com/YunusAndreasson/safina/internal/k8s"
	"github.com/YunusAndreasson/safina/internal/metrics"
	"github.com/YunusAndreasson/safina/internal/tui/sections"
	"github.com/stretchr/testify/require"
)

func TestMetricsWorkspaceNavigation(t *testing.T) {
	m := NewModel(&k8s.Manager{}, &config.AppConfig{})
	t.Cleanup(m.ctx.Cancel)
	m.ctx.ActiveContext = "demo"
	m.tree.PushContext("demo")
	m.tree.SetChildren(m.tree.Path(), &sections.Children{Kind: k8s.KindApp, Apps: []k8s.AppInfo{{Kind: k8s.AppKindDeployment, Name: "api", Namespace: "shop", UID: "original", Metrics: metrics.Reading{State: metrics.Available, SourceAt: time.Now(), CPU: 10}}}})
	m.Update(keyMsg("m"))
	require.Equal(t, stateMetricsView, m.state)
	require.True(t, m.modalOpen(), "metrics open in the modal over the list")
	require.NotNil(t, m.metrics.Series)
	require.Equal(t, types.UID("original"), m.metrics.Series.Key.UID)
	for _, size := range [][2]int{{80, 24}, {120, 40}, {160, 50}} {
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		v := m.View()
		require.LessOrEqual(t, lipgloss.Width(v.Content), size[0])
		require.LessOrEqual(t, lipgloss.Height(v.Content), size[1])
	}
	m.Update(keyMsg("a"))
	m.metrics.Actions = []sections.MetricAction{{Record: history.Record{ID: "action", Action: "restart", Outcome: "accepted"}}}
	_, detail := m.Update(keyMsg("enter"))
	require.NotNil(t, detail)
	require.NotNil(t, m.choices)
	m.Update(detail())
	require.Equal(t, "history_detail", m.choices.kind)
	m.Update(keyMsg("esc"))
	require.Nil(t, m.choices)
	require.Equal(t, stateMetricsView, m.state)
	m.Update(keyMsg("]"))
	require.Equal(t, 2, m.metrics.Preset)
	require.False(t, m.tree.OnResourcePage(), "m opens the chart over the list, not a page")
	m.Update(keyMsg("esc"))
	require.Equal(t, stateMain, m.state)
	require.False(t, m.tree.OnResourcePage(), "Esc returns to the list the chart was opened from")
	m.Update(keyMsg("/"))
	m.Update(keyMsg("m"))
	require.Equal(t, stateMain, m.state, "typing a filter must not open charts")
}

func TestMetricsIgnoresObsoleteHistory(t *testing.T) {
	m := NewModel(&k8s.Manager{}, &config.AppConfig{})
	t.Cleanup(m.ctx.Cancel)
	m.state = stateMetricsView
	m.metricsHistoryRequest = 2
	m.metrics.Generation = 3
	m.metrics.Ref = sections.NodeRef{Kind: k8s.KindPod, UID: "new"}
	result := history.Result{Records: []history.Record{{Target: history.Target{UID: "new"}, Action: "restart", Outcome: "accepted", ID: "old-response"}}}
	m.Update(metricsHistoryMsg{request: 1, generation: 3, result: result})
	require.Empty(t, m.metrics.Actions)
	m.Update(metricsHistoryMsg{request: 2, generation: 2, result: result})
	require.Empty(t, m.metrics.Actions)
	m.Update(metricsHistoryMsg{request: 2, generation: 3, result: result})
	require.Len(t, m.metrics.Actions, 1)
}
