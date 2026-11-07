package tui

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/yunus/safina/internal/k8s"
)

const cacheWarmDelay = 200 * time.Millisecond

type cacheWarmTarget struct {
	cluster string
	kind    string
	client  *k8s.Client
}

type cacheWarmMsg struct {
	epoch  uint64
	target cacheWarmTarget
	warm   tea.Cmd
}

func (m *Model) selectedCacheWarmTarget() cacheWarmTarget {
	if m.state != stateMain || !m.focused || !m.tree.Focused || m.ctx.Manager == nil || m.ctx.ActiveContext == "" || m.ctx.Background.Err() != nil {
		return cacheWarmTarget{}
	}
	var kind string
	switch m.tree.SelectedRef().Kind {
	case k8s.KindNetworkFold:
		kind = k8s.KindService
	case k8s.KindConfigFold:
		kind = k8s.KindConfigMap
	case k8s.KindSecretsFold:
		kind = k8s.KindSecret
	case k8s.KindStorageFold:
		kind = k8s.KindPVC
	case k8s.KindIngressFold:
		kind = k8s.KindIngress
	default:
		return cacheWarmTarget{}
	}
	client, ok := m.ctx.Manager.GetClient(m.ctx.ActiveContext)
	if !ok || client.Informers() == nil {
		return cacheWarmTarget{}
	}
	return cacheWarmTarget{m.ctx.ActiveContext, kind, client}
}

// Schedule at most one pending warmup. Repeated selection synchronization for
// the same target does not reset the quiet period or schedule more timers.
func (m *Model) scheduleCacheWarm() tea.Cmd {
	target := m.selectedCacheWarmTarget()
	if target == m.cacheWarmTarget {
		return nil
	}
	if m.cacheWarmCancel != nil {
		m.cacheWarmCancel()
		m.cacheWarmCancel = nil
	}
	m.cacheWarmEpoch++
	m.cacheWarmTarget = target
	if target.client == nil {
		return nil
	}
	ctx, cancel := context.WithCancel(m.ctx.Background)
	m.cacheWarmCancel = cancel
	msg := cacheWarmMsg{epoch: m.cacheWarmEpoch, target: target, warm: func() tea.Msg {
		defer cancel()
		if ctx.Err() == nil {
			target.client.WarmResourceCache(target.kind)
		}
		return nil
	}}
	return func() tea.Msg {
		timer := time.NewTimer(cacheWarmDelay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
			return msg
		}
	}
}

func (m *Model) handleCacheWarm(msg cacheWarmMsg) tea.Cmd {
	if msg.epoch != m.cacheWarmEpoch || msg.target.client == nil {
		return nil
	}
	if msg.target != m.selectedCacheWarmTarget() {
		if m.cacheWarmCancel != nil {
			m.cacheWarmCancel()
			m.cacheWarmCancel = nil
		}
		m.cacheWarmTarget = cacheWarmTarget{}
		return nil
	}
	// Consume once. Keep the cancellation function until the next selection so
	// a queued warm command can still be canceled after this message is handled.
	m.cacheWarmEpoch++
	return msg.warm
}
