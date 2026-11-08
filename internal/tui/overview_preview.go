package tui

import (
	"slices"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/YunusAndreasson/safina/internal/k8s"
	"github.com/YunusAndreasson/safina/internal/tui/sections"
)

type treeLoadResultMsg struct {
	epoch  uint64
	result tea.Msg
}

type overviewPreviewMsg struct {
	epoch    uint64
	path     []sections.NodeRef
	snapshot *overviewSnapshot
}

func (m *Model) loadOverviewPreview(epoch uint64, path []sections.NodeRef) tea.Cmd {
	key, store := m.overviewKey(), m.overviewStore
	if key == "" || store == nil {
		return nil
	}
	return func() tea.Msg {
		return overviewPreviewMsg{epoch: epoch, path: path, snapshot: store.load(key, time.Now())}
	}
}

func (m *Model) applyOverviewPreview(msg overviewPreviewMsg) tea.Cmd {
	if msg.snapshot == nil || msg.epoch != m.treeLoadEpoch || !m.overviewPending || !slices.Equal(msg.path, m.tree.Path()) || m.tree.LoadErr != nil {
		return nil
	}
	m.tree.ShowOverviewPreview(msg.snapshot.children())
	m.overviewPreview = true
	m.lastRefresh = msg.snapshot.At
	m.tree.FinishLoad()
	m.tree.StartRefresh()
	return m.syncSelection()
}

func (m *Model) saveOverview(ch *sections.Children, at time.Time) tea.Cmd {
	if m.tree.Focus().Kind != k8s.KindContext || ch == nil || ch.Kind != k8s.KindNamespaceFolder {
		return nil
	}
	key, store := m.overviewKey(), m.overviewStore
	if key == "" || store == nil {
		return nil
	}
	snapshot := snapshotOverview(key, at, ch)
	return func() tea.Msg { store.save(snapshot); return nil }
}
