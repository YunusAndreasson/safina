package tui

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/yunus/safina/internal/history"
	"github.com/yunus/safina/internal/k8s"
	"github.com/yunus/safina/internal/tui/components"
	"github.com/yunus/safina/internal/tui/sections"
)

type historyLoadedMsg struct {
	request uint64
	result  history.Result
	err     error
}
type historyDetailMsg struct {
	request uint64
	text    string
	record  history.Record
}
type historyExportMsg struct {
	path string
	err  error
}
type historyPrunedMsg struct{}

func historyPruneTick(s *history.Store) tea.Cmd {
	if s == nil {
		return nil
	}
	return tea.Tick(time.Minute, func(time.Time) tea.Msg { _ = s.Prune(); return historyPrunedMsg{} })
}

func (m *Model) selectedHistoryFilter() history.Filter {
	ref := m.tree.SelectedRef()
	kind := ref.Kind
	if ref.SubKind != "" {
		kind = ref.SubKind
	}
	namespace := ref.Namespace
	if namespace == "" && !ref.ClusterScoped && ref.Kind != k8s.KindNode && ref.Kind != k8s.KindNodePool && ref.Kind != k8s.KindPV {
		for _, ancestor := range m.tree.Path() {
			if ancestor.Kind == k8s.KindNamespaceFolder || ancestor.Kind == k8s.KindNamespace {
				namespace = ancestor.Name
			}
		}
	}
	f := history.Filter{Cluster: m.ctx.ActiveContext, Namespace: namespace, Limit: 500}
	if client, ok := m.ctx.Manager.GetClient(m.ctx.ActiveContext); ok && client.RESTConfig != nil {
		f.Cluster = m.ctx.Manager.HistoryTarget(m.ctx.ActiveContext, "", "", "").Cluster
	}
	if ref.Kind != k8s.KindContext && ref.Kind != k8s.KindNamespace && ref.Kind != k8s.KindNamespaceFolder && ref.Kind != k8s.KindContextList && ref.Kind != k8s.KindNodePool && !sections.IsResourceGroup(ref.Kind) && ref.Name != "" {
		f.Resource = kind + "/" + ref.Name
	}
	return f
}

func (m *Model) recordSelection(action string) {
	ref := m.tree.SelectedRef()
	m.ctx.Manager.History.Append(history.Record{Target: m.ctx.Manager.HistoryTarget(m.ctx.ActiveContext, ref.Namespace, ref.Kind, ref.Name), Action: action, Outcome: "requested"})
}

func (m *Model) openHistory() tea.Cmd {
	if m.ctx.Manager.History == nil {
		return m.raiseError("Logbook is unavailable in this session")
	}
	m.historyFilter = m.selectedHistoryFilter()
	return m.loadHistory()
}

func (m *Model) loadHistory() tea.Cmd {
	m.historyRequest++
	request := m.historyRequest
	dir := m.ctx.Manager.History.Directory()
	filter := m.historyFilter
	m.openChoices("history", "Logbook", "Loading saved activity…", nil, components.KeyDesc{Key: "esc", Desc: "close"})
	return func() tea.Msg {
		r, e := history.Query(dir, filter)
		return historyLoadedMsg{request: request, result: r, err: e}
	}
}

func (m *Model) showHistory(msg historyLoadedMsg) tea.Cmd {
	if msg.request != m.historyRequest || m.choices == nil || m.choices.kind != "history" {
		return nil
	}
	if msg.err != nil {
		return m.raiseError("Logbook: " + msg.err.Error())
	}
	items := make([]list.Item, 0, len(msg.result.Records))
	for i := len(msg.result.Records) - 1; i >= 0; i-- {
		r := msg.result.Records[i]
		items = append(items, historyItem(r))
	}
	scope := m.ctx.ActiveContext
	if m.historyFilter.Namespace != "" {
		scope += "/" + m.historyFilter.Namespace
	}
	if m.historyFilter.Resource != "" {
		scope += " · " + m.historyFilter.Resource
	}
	title := "Logbook · " + scope + " · retained"
	if !m.historyFilter.Since.IsZero() {
		title = "Logbook · " + scope + " · since " + m.historyFilter.Since.UTC().Format("Jan 02 15:04Z")
	}
	m.openChoices("history", title, "Recorded Safina activity; external changes are not included.", items, components.KeyDesc{Key: "/", Desc: "filter"}, components.KeyDesc{Key: "↵", Desc: "details"}, components.KeyDesc{Key: "a", Desc: "cluster activity"}, components.KeyDesc{Key: "t", Desc: "time window"}, components.KeyDesc{Key: "e", Desc: "export"}, components.KeyDesc{Key: "c", Desc: "attach interval to chat"}, components.KeyDesc{Key: "esc", Desc: "close"})
	m.choices.model.SetFilteringEnabled(true)
	if msg.result.Omitted > 0 || len(msg.result.Warnings) > 0 {
		m.choices.footer += " · partial results; use CLI time filters"
	}
	return nil
}

// historyItem renders one Logbook record as a list row: when, what and how
// it ended in words (capture_ended reads "capture ended"), then where, with
// the parameters and outcome detail an on-caller looks for: the replica
// count asked for, the workload's state before and after a write,
// previous-container logs, "2/2 ready".
func historyItem(r history.Record) choiceItem {
	words := func(s string) string { return strings.ReplaceAll(s, "_", " ") }
	title := fmt.Sprintf("%s · %s · %s", r.Time.UTC().Format("2006-01-02 15:04:05Z"), words(r.Action), words(r.Outcome))
	detail := r.Target.Context + " / " + r.Target.Namespace + " / " + r.Target.Kind + "/" + r.Target.Name
	p := r.Parameters
	switch {
	case r.Outcome == "before" || r.Outcome == "after":
		// A workload snapshot (k8s HistorySnapshot), not a request.
		if p["revision"] != "" {
			detail += " · revision " + p["revision"]
		}
		if p["ready"] != "" && p["replicas"] != "" {
			detail += " · " + p["ready"] + "/" + p["replicas"] + " available"
		}
	case p["replicas"] != "":
		detail += " · to " + p["replicas"]
	}
	if r.Parameters["previous"] == "true" {
		detail += " · previous"
	}
	if r.Detail != "" {
		detail += " · " + r.Detail
	}
	return choiceItem{title: title, detail: detail, value: r}
}

func (m *Model) historyDetail(r history.Record) tea.Cmd {
	m.historyRequest++
	request := m.historyRequest
	dir := m.ctx.Manager.History.Directory()
	return func() tea.Msg {
		b, _ := json.MarshalIndent(r, "", "  ")
		text := string(b)
		if r.Evidence != "" {
			data, err := history.ReadEvidence(dir, r.Evidence)
			if err != nil {
				text += "\nEvidence expired or unavailable"
			} else {
				if len(data) > 16<<10 {
					data = data[:16<<10]
					text += "\nEvidence preview truncated; export for full capture"
				}
				text += "\n" + string(data)
			}
		}
		return historyDetailMsg{request: request, text: text, record: r}
	}
}

func (m *Model) showHistoryDetail(msg historyDetailMsg) {
	if msg.request != m.historyRequest || m.choices == nil {
		return
	}
	m.historySelection = &msg.record
	lines := strings.Split(ansi.Wrap(msg.text, max(20, min(70, m.ctx.Width-16)), ""), "\n")
	items := make([]list.Item, 0, len(lines))
	for _, line := range lines {
		items = append(items, choiceItem{title: line})
	}
	m.openChoices("history_detail", "Logbook entry", "", items, components.KeyDesc{Key: "c", Desc: "attach record/evidence to chat"}, components.KeyDesc{Key: "esc", Desc: "close"})
}

func (m *Model) exportHistory() tea.Cmd {
	dir := m.ctx.Manager.History.Directory()
	filter := m.historyFilter
	// Export lives outside retention-managed pools and is never overwritten.
	path := filepath.Join(filepath.Dir(dir), "history-export-"+history.ID())
	return func() tea.Msg { return historyExportMsg{path, history.Export(dir, path, filter)} }
}

func (m *Model) attachHistoryToChat(text string) tea.Cmd {
	m.chat.AttachHistory("[Attached Safina evidence]\n" + history.BoundedText(text, (16<<10)-64))
	m.state = stateChatView
	m.treeFocusInImmersive = false
	m.tree.Focused = false
	m.chat.SetFocus(true)
	return m.raiseStatus("Logbook attached to chat draft; Enter sends")
}
