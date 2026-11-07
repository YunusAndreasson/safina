package tui

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"github.com/yunus/safina/internal/k8s"
	"github.com/yunus/safina/internal/tui/sections"
)

type (
	logTarget struct {
		namespace, pod, container, status string
		previous                          bool
	}
	logTargetsMsg struct {
		request uint64
		targets []logTarget
		err     error
	}
)

func (m *Model) openLogTargets(ref sections.NodeRef) tea.Cmd {
	m.logSource = ref
	m.logPickerRequest++
	request := m.logPickerRequest
	m.openChoices("logs", "Choose log target", "Loading pods and containers… · esc cancel", nil)
	mgr, cluster, ctx := m.ctx.Manager, m.ctx.ActiveContext, m.ctx.Background
	return func() tea.Msg {
		result := logTargetsMsg{request: request}
		loadCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		client, ok := mgr.GetClient(cluster)
		if !ok {
			result.err = fmt.Errorf("context %s unavailable", cluster)
			return result
		}
		pods := []k8s.PodInfo{{Name: ref.Name, Namespace: ref.Namespace}}
		if ref.Kind == k8s.KindApp {
			var err error
			pods, err = client.ListPodsForApp(loadCtx, k8s.AppRef{Kind: ref.SubKind, Name: ref.Name, Namespace: ref.Namespace, UID: ref.UID})
			if err != nil {
				result.err = err
				return result
			}
		}
		for _, pod := range pods {
			containers, err := client.ListContainersForPod(loadCtx, pod.Namespace, pod.Name)
			if err != nil {
				result.err = err
				return result
			}
			for _, c := range containers {
				result.targets = append(result.targets, logTarget{namespace: pod.Namespace, pod: pod.Name, container: c.Name, status: c.State, previous: c.Restarts > 0 || c.LastReason != ""})
			}
		}
		slices.SortStableFunc(result.targets, func(a, b logTarget) int {
			aBad, bBad := !strings.HasPrefix(a.status, "Running"), !strings.HasPrefix(b.status, "Running")
			if aBad != bBad {
				if aBad {
					return -1
				}
				return 1
			}
			return strings.Compare(a.pod+"/"+a.container, b.pod+"/"+b.container)
		})
		return result
	}
}

func (m *Model) setLogTargets(msg logTargetsMsg) tea.Cmd {
	if msg.request != m.logPickerRequest || m.choices == nil || m.choices.kind != "logs" {
		return nil
	}
	if msg.err != nil {
		m.choices = nil
		return m.raiseError("Log targets: " + msg.err.Error())
	}
	items := make([]list.Item, 0, len(msg.targets))
	for _, t := range msg.targets {
		detail := t.namespace + " · " + t.status
		if t.previous {
			detail += " · previous logs available"
		}
		items = append(items, choiceItem{title: t.pod + " / " + t.container, detail: detail, value: t})
	}
	m.openChoices("logs", "Choose log target", "enter current logs · p previous logs · esc cancel", items)
	return nil
}

func (m *Model) startTargetLogs(target logTarget, previous bool) tea.Cmd {
	m.stopLogStream()
	request := m.logRequest
	m.logTarget = target
	m.state = stateLogView
	m.treeFocusInImmersive = false
	m.logs.Reset(target.pod, target.namespace)
	m.logs.SetTarget(target.container, previous)
	m.logs.SetSize(m.ctx.Width, m.ctx.Height)
	ctx, cancel := context.WithCancel(m.ctx.Background)
	m.logCancel = cancel
	mgr, cluster := m.ctx.Manager, m.ctx.ActiveContext
	return func() tea.Msg {
		t := mgr.HistoryTarget(cluster, target.namespace, "Pod", target.pod)
		t.Container = target.container
		op := mgr.History.Begin(t, "logs", map[string]string{"previous": strconv.FormatBool(previous)})
		client, ok := mgr.GetClient(cluster)
		if !ok {
			err := errors.New("context unavailable")
			op.Finish(err, "stream_ended")
			return sections.LogStreamStartedMsg{Request: request, Err: err}
		}
		cs := client.StreamClientset
		if cs == nil {
			cs = client.Clientset
		}
		ch := make(chan k8s.PodLogEvent)
		go func() {
			if mgr.History.EvidenceEnabled() {
				snapshotCtx, cancel := k8s.SnapshotContext(ctx)
				op.Target, _ = client.HistorySnapshot(snapshotCtx, t)
				cancel()
			}
			capture := mgr.History.Capture(op)
			defer capture.Close()
			source := make(chan k8s.PodLogEvent)
			go k8s.StreamPodContainerLogs(ctx, cs, target.namespace, target.pod, target.container, previous, source)
			defer close(ch)
			for event := range source {
				capture.Append(event.Line)
				if event.Done {
					err := event.Err
					if err == nil {
						err = ctx.Err()
					}
					op.Finish(err, "stream_ended")
				}
				select {
				case ch <- event:
				case <-ctx.Done():
				}
			}
		}()
		return sections.LogStreamStartedMsg{Pod: target.pod, Ch: ch, Request: request}
	}
}

// Stop the producer and consume remaining events so channel sends can finish.
func (m *Model) stopLogStream() {
	if m.logCancel != nil {
		m.logCancel()
		m.logCancel = nil
	}
	m.logRequest++
	if ch := m.logCh; ch != nil {
		go func() {
			for range ch {
			}
		}()
	}
	m.logCh = nil
}
