package tui

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/YunusAndreasson/safina/internal/tui/sections"
)

func TestLogsTabOpensCurrentLogsWithoutPicker(t *testing.T) {
	m := shiftFooterModel(t)
	m.handleKey(keyMsg("m"))
	require.Equal(t, stateMetricsView, m.state)
	cmd := m.showTab(sections.WSTabLogs)
	require.NotNil(t, cmd)
	require.Equal(t, stateLogView, m.state)
	require.Nil(t, m.choices)
	msg := logTargetsMsg{request: m.logPickerRequest, automatic: true, streamRequest: m.logRequest, targets: []logTarget{{pod: "api-1", namespace: "shop", container: "app", previous: true}}}
	cmd = m.setLogTargets(msg)
	require.NotNil(t, cmd)
	require.Nil(t, m.choices)
	require.Equal(t, "api-1", m.logTarget.pod)
	require.True(t, m.logTarget.previous)
	// Target selection remains an explicit action.
	m.handleKey(keyMsg("o"))
	require.NotNil(t, m.choices)
	require.Equal(t, "logs", m.choices.kind)
	m.stopLogStream()
}

func TestAutomaticLogTargetCannotReopenPaneAfterNavigation(t *testing.T) {
	m := shiftFooterModel(t)
	m.handleKey(keyMsg("l"))
	msg := logTargetsMsg{request: m.logPickerRequest, automatic: true, streamRequest: m.logRequest, targets: []logTarget{{pod: "old"}}}
	m.handleKey(keyMsg("esc"))
	require.Nil(t, m.setLogTargets(msg))
	require.NotEqual(t, stateLogView, m.state)
	require.Nil(t, m.choices)
}

func TestAutomaticLogTargetFailureDoesNotOpenPicker(t *testing.T) {
	for _, err := range []error{nil, errors.New("permission denied")} {
		m := shiftFooterModel(t)
		m.openCurrentLogs(m.tree.SelectedRef())
		require.Nil(t, m.setLogTargets(logTargetsMsg{request: m.logPickerRequest, automatic: true, streamRequest: m.logRequest, err: err}))
		require.Equal(t, stateLogView, m.state)
		require.Nil(t, m.choices)
	}
}
