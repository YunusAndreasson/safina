package tui

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/yunus/safina/internal/k8s"
	"github.com/yunus/safina/internal/tui/sections"
)

type (
	operation struct {
		target         sections.WorkloadTarget
		action, status string
		started        time.Time
		revision       uint64
	}
	operationCheckMsg struct {
		key      string
		revision uint64
	}
	operationProgressMsg struct {
		key      string
		revision uint64
		text     string
		done     bool
		err      error
	}
)

func (m *Model) trackOperation(target sections.WorkloadTarget, action string) tea.Cmd {
	if m.operations == nil {
		m.operations = make(map[string]operation)
	}
	key := target.Context + "/" + target.Namespace + "/" + target.Kind + "/" + target.Name
	revision := m.operations[key].revision + 1
	// A timestamp token prevents a late response from a completed earlier action
	// from applying to a later operation with the same resource key.
	revision += uint64(time.Now().UnixNano())
	m.operations[key] = operation{target: target, action: action, status: "accepted · waiting for controller", started: time.Now(), revision: revision}
	return m.checkOperation(key, revision)
}

func (m *Model) checkOperation(key string, revision uint64) tea.Cmd {
	op, ok := m.operations[key]
	if !ok || op.revision != revision {
		return nil
	}
	mgr := m.ctx.Manager
	ctx := m.ctx.Background
	return func() tea.Msg {
		result := operationProgressMsg{key: key, revision: revision}
		client, ok := mgr.GetClient(op.target.Context)
		if !ok {
			result.err = errors.New("context unavailable")
			return result
		}
		pollCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		result.text, result.done, result.err = k8s.WorkloadProgress(pollCtx, client.Clientset, op.target.Namespace, op.target.Kind, op.target.Name, op.action == "restart")
		return result
	}
}

func (m *Model) updateOperation(msg operationProgressMsg) tea.Cmd {
	op, ok := m.operations[msg.key]
	if !ok || op.revision != msg.revision {
		return nil
	}
	if errors.Is(msg.err, context.Canceled) {
		return tea.Tick(2*time.Second, func(time.Time) tea.Msg { return operationCheckMsg{key: msg.key, revision: msg.revision} })
	}
	if msg.err != nil || msg.done || time.Since(op.started) > 5*time.Minute {
		delete(m.operations, msg.key)
		subject := op.target.Context + " / " + op.target.Namespace + " / " + op.target.Name
		if msg.err != nil {
			return m.raiseReadError(op.action+" monitoring · "+subject+": "+msg.err.Error(), func() tea.Cmd {
				if m.ctx.ActiveContext != op.target.Context {
					return m.raiseError("Switch to " + op.target.Context + " to resume monitoring")
				}
				m.dismissBanner()
				return m.trackOperation(op.target, op.action)
			})
		}
		if !msg.done {
			return m.raiseError(op.action + " still pending · " + subject + " · monitoring stopped after 5m; inspect workload")
		}
		return m.raiseStatus(op.action + " complete · " + subject)
	}
	op.status = msg.text
	m.operations[msg.key] = op
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg { return operationCheckMsg{key: msg.key, revision: msg.revision} })
}

func (m *Model) operationSummary() string {
	keys := make([]string, 0, len(m.operations))
	for key, op := range m.operations {
		if op.target.Context == m.ctx.ActiveContext {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		op := m.operations[key]
		parts = append(parts, op.action+" · "+op.target.Name+" · "+op.status)
	}
	return strings.Join(parts, " | ")
}
func (m *Model) hasFeedback() bool { return m.bannerMsg != "" || m.operationSummary() != "" }
