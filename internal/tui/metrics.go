package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/yunus/safina/internal/history"
	"github.com/yunus/safina/internal/tui/sections"
)

type metricsHistoryMsg struct {
	request, generation uint64
	result              history.Result
	err                 error
}

func (m *Model) syncMetrics(force bool) tea.Cmd {
	changed := m.metrics.Select(m.ctx.ActiveContext, m.tree.SelectedRef(), m.tree.SelectedMetricSeries())
	if changed {
		m.metricsHistoryRequest++
		m.metricsHistoryPending = false
		force = true
	}
	if m.state != stateMetricsView || !m.metrics.ShowActivity || !m.focused || !sections.MetricsSupported(m.metrics.Ref) {
		return nil
	}
	if m.metricsHistoryPending || (!force && time.Since(m.metricsHistoryAt) < 5*time.Second) {
		return nil
	}
	if m.ctx.Manager == nil || m.ctx.Manager.History == nil || !m.ctx.Manager.History.Enabled() {
		m.metrics.HistoryState = "Action history unavailable"
		return nil
	}
	filter := m.selectedHistoryFilter()
	filter.Since, filter.Until = m.metrics.Range(time.Now())
	filter.Limit = 500
	filter.UID = m.metrics.Ref.UID
	filter.Actions = sections.MetricActions
	if filter.UID == "" {
		m.metrics.HistoryState = "Action identity unavailable"
		return nil
	}
	m.metricsHistoryRequest++
	request, generation := m.metricsHistoryRequest, m.metrics.Generation
	m.metricsHistoryPending = true
	m.metricsHistoryAt = time.Now()
	m.metrics.HistoryState = "Loading related Logbook changes…"
	dir := m.ctx.Manager.History.Directory()
	return func() tea.Msg {
		result, err := history.Query(dir, filter)
		return metricsHistoryMsg{request: request, generation: generation, result: result, err: err}
	}
}
