package tui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"

	"github.com/yunus/safina/internal/config"
	"github.com/yunus/safina/internal/debug"
	"github.com/yunus/safina/internal/history"
	"github.com/yunus/safina/internal/k8s"
	"github.com/yunus/safina/internal/tui/sections"
)

// isCtxDone reports whether err is the kind of cancellation we expect during
// normal flow — switching contexts or rapid drill+pop races against in-flight
// loaders. We suppress those cleanly so they don't surface as an error banner.
func isCtxDone(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// Register the stream in the update loop before scheduling any reads. Send
// launches its subprocess asynchronously, so this does not block the UI.
type historyChatReadyMsg struct {
	request uint64
	prompt  string
}

func (m *Model) startChatStream(prompt string) tea.Cmd {
	m.historyChatRequest++
	m.chatStreamCh = nil // Reject late events while the new history query is pending.
	if m.ctx.Manager.History != nil && !strings.Contains(prompt, "[Attached Safina evidence]") {
		request := m.historyChatRequest
		dir := m.ctx.Manager.History.Directory()
		f := m.selectedHistoryFilter()
		f.Since = time.Now().Add(-24 * time.Hour)
		f.Limit = 50
		return func() tea.Msg {
			if result, err := history.Query(dir, f); err == nil && len(result.Records) > 0 {
				prompt += "\n\n" + history.Summary(result, 16<<10)
			}
			return historyChatReadyMsg{request, prompt}
		}
	}
	m.chatStreamCh = m.claudeSvc.Send(m.ctx.Background, prompt)
	return sections.NextChatEvent(m.chatStreamCh)
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case treeLoadResultMsg:
		if msg.epoch != m.treeLoadEpoch {
			return m, nil
		}
		return m.Update(msg.result)
	case overviewPreviewMsg:
		return m, m.applyOverviewPreview(msg)
	case cacheWarmMsg:
		return m, m.handleCacheWarm(msg)
	case incomingTickMsg:
		var detail tea.Cmd
		if m.state == stateIncoming && m.incoming.mode == "outcome" && !m.incoming.busy {
			detail = m.incomingCommand("details")
		}
		return m, tea.Batch(m.loadIncoming(), detail, incomingTick())
	case incomingInboxMsg:
		if msg.request != m.incoming.inboxRequest {
			return m, nil
		}
		m.incoming.inboxBusy = false
		if msg.err == nil {
			rows := m.incomingRows()
			var selected incomingRow
			if m.incoming.cursor < len(rows) {
				selected = rows[m.incoming.cursor]
			}
			m.incoming.inbox = msg.inbox
			m.incoming.inboxErr = ""
			rows = m.incomingRows()
			m.incoming.cursor = min(m.incoming.cursor, max(0, len(rows)-1))
			for i, row := range rows {
				if row.investigation.ID == selected.investigation.ID && row.proposal.ID == selected.proposal.ID && row.proposal.Revision == selected.proposal.Revision {
					m.incoming.cursor = i
					break
				}
			}
		} else {
			m.incoming.inboxErr = msg.err.Error()
		}
		return m, nil
	case incomingResultMsg:
		return m, m.incomingResult(msg)
	case historyChatReadyMsg:
		if msg.request != m.historyChatRequest || !m.chat.Streaming() {
			return m, nil
		}
		m.chatStreamCh = m.claudeSvc.Send(m.ctx.Background, msg.prompt)
		return m, sections.NextChatEvent(m.chatStreamCh)
	case historyLoadedMsg:
		return m, m.showHistory(msg)
	case historyDetailMsg:
		m.showHistoryDetail(msg)
		return m, nil
	case historyExportMsg:
		if msg.err != nil {
			return m, m.raiseError("Logbook export: " + msg.err.Error())
		}
		return m, m.raiseStatus("Logbook exported: " + msg.path)
	case historyPrunedMsg:
		return m, historyPruneTick(m.ctx.Manager.History)

	case operationCheckMsg:
		return m, m.checkOperation(msg.key, msg.revision)
	case operationProgressMsg:
		return m, m.updateOperation(msg)
	case logTargetsMsg:
		return m, m.setLogTargets(msg)
	case sections.NamespaceSearchMsg:
		if msg.Request != m.searchRequest || msg.Context != m.ctx.ActiveContext || m.state != statePalette {
			return m, nil
		}
		m.searchLoading = false
		m.palette.SetEntries(msg.Entries)
		scope := "Namespace " + msg.Namespace + " · core kinds"
		if msg.Err != nil {
			scope += " · partial"
		}
		m.palette.SetScope(scope)
		if msg.Err != nil {
			return m, m.raiseError("Namespace search incomplete: " + msg.Err.Error())
		}
		return m, nil
	case tea.WindowSizeMsg:
		m.ctx.Width = msg.Width
		m.ctx.Height = msg.Height
		m.logs.SetSize(msg.Width, msg.Height)
		m.events.SetSize(msg.Width, msg.Height)
		m.describe.SetSize(msg.Width, msg.Height)
		m.shell.SetSize(msg.Width, msg.Height)
		// Chat sizes off the right-panel split computed in view.go; we let
		// view.go keep ownership of the split math and only forward the
		// outer terminal dims here so a resize mid-stream re-flows tokens.
		m.syncChatContext()
		return m, nil

	case tea.KeyPressMsg:
		return m.handleKey(msg)

	case clockMsg:
		// Skip the wall-clock advance while blurred — the topbar reads the
		// staleness "↻ Ns" off m.now, and we want that pinned to the last
		// focused moment so the operator returning from another window sees
		// at-a-glance "your data is N seconds old" instead of a clock that
		// kept ticking against frozen data. The next FocusMsg rearms it.
		if !m.focused {
			return m, nil
		}
		m.now = time.Time(msg)
		return m, m.clockTick()

	case sections.TreeFocusChangedMsg:
		// Navigation commands may arrive after another key has moved the tree.
		// Never activate a cluster for a path that is no longer on screen.
		if len(msg.Path) == 0 || !slices.Equal(msg.Path, m.tree.Path()) {
			return m, nil
		}
		// The tree pushed or popped a level. If we landed on a context (the
		// user picked / switched a cluster, or auto-drilled on startup), wire
		// it up: set ActiveContext, drop stale caches, re-stamp the window
		// title, and persist. The load fires from the cache-miss check below.
		focus := msg.Path[len(msg.Path)-1]
		m.overviewPreview = false
		m.overviewPending = false
		cluster := m.ctx.ActiveContext
		if focus.Kind == k8s.KindContext {
			cluster = focus.Name
		}
		m.ctx.Manager.History.Append(history.Record{Target: m.ctx.Manager.HistoryTarget(cluster, focus.Namespace, focus.Kind, focus.Name), Action: "browse", Outcome: "selected"})
		debug.Log("tree.focus",
			"kind", focus.Kind,
			"name", focus.Name,
			"namespace", focus.Namespace,
			"depth", len(msg.Path))
		if focus.Kind == k8s.KindContext {
			// Lazy informers: entering (or re-entering) a cluster starts its
			// watch-backed cache and stops every other context's — watch
			// streams only ever target the cluster on screen. Idempotent and
			// non-blocking, so calling it on every context focus is free;
			// the first paint renders from direct reads while the cache warms.
			m.ctx.Manager.ActivateContext(focus.Name)
		}
		if focus.Kind == k8s.KindContext && m.ctx.ActiveContext != focus.Name {
			m.ctx.ActiveContext = focus.Name
			m.ctx.Manager.History.Append(history.Record{Target: m.ctx.Manager.HistoryTarget(focus.Name, "", "", ""), Action: "context_switch", Outcome: "selected"})
			if !m.bannerErr {
				m.dismissBanner()
			}
			m.ctx.ResetBackground()
			m.tree.ResetForContextSwitch()
			m.inspector.Clear()
			m.activity.Clear()          // events from the prior cluster are stale on cluster switch
			m.connected.Clear()         // cross-cluster connection refs aren't valid
			m.lastRefresh = time.Time{} // reset staleness clock for the new cluster
			// Blank the cluster-health pill on cluster switch so the topbar
			// doesn't render cluster A's stats over cluster B while the
			// first fetch is in flight.
			m.clusterHealth = k8s.ClusterHealth{}
			m.clusterHealthAt = time.Time{}
			m.clusterHealthCtx = ""
			// Discovery is cluster-scoped — drop the cache so the new
			// cluster's GVR set replaces the previous one on first open
			// of the Resource Browser.
			m.discoveryByContext = make(map[string]bool)
			m.tree.SetDiscoveredKinds(nil)
			m.saveConfig()
		}
		if focus.Kind == k8s.KindContextList {
			// Popped back to the picker — parked there, no cluster deserves
			// open watch streams, so every context's informers shut down.
			m.ctx.Manager.DeactivateAll()
			// There's no "data" to be stale, so hide the refresh indicator
			// until the user picks again.
			m.lastRefresh = time.Time{}
			// Right-stack tiles target a resource; at the picker there is
			// no selection so all three go quiet rather than render ghosts.
			m.activity.Clear()
			m.inspector.Clear()
			m.connected.Clear()
			// Same logic as a context switch: there's no cluster, so the
			// pill should disappear instead of showing the previous one.
			m.clusterHealth = k8s.ClusterHealth{}
			m.clusterHealthAt = time.Time{}
			m.clusterHealthCtx = ""
		}
		// Restamp the window title from the current path so terminal tabs
		// stay accurate as the operator drills in or pops back out.
		m.title = windowTitleFor(m.tree.BreadcrumbCrumbs())
		actCmd := m.syncSelection()
		// Kick off discovery on first entry to a cluster so the next
		// fold render uses the real Kind names from the API server
		// (instead of falling back to title-cased plurals).
		var discoCmd tea.Cmd
		if focus.Kind == k8s.KindContext && m.ctx.ActiveContext != "" &&
			!m.discoveryByContext[m.ctx.ActiveContext] {
			m.discoveryByContext[m.ctx.ActiveContext] = true
			discoCmd = sections.LoadDiscovery(m.ctx.Background, m.ctx.Manager, m.ctx.ActiveContext)
		}
		if m.tree.Children() == nil {
			m.tree.Loading = true
			m.tree.StartLoad()
			return m, tea.Batch(m.loadTreeFocus(), actCmd, discoCmd)
		}
		if focus.Kind == k8s.KindContext {
			m.overviewPreview = true
			m.tree.StartRefresh()
			return m, tea.Batch(m.loadTreeFocus(), actCmd, discoCmd, m.finishPaletteNavigation())
		}
		return m, tea.Batch(actCmd, discoCmd, m.finishPaletteNavigation())

	case sections.TreeChildrenLoadedMsg:
		if !slices.Equal(msg.Path, m.tree.Path()) {
			return m, nil
		}
		m.overviewPending = false
		m.tree.Loading = false
		m.tree.FinishRefresh()
		m.tree.FinishLoad()
		focus := msg.Path[len(msg.Path)-1]
		if msg.Err != nil {
			if isCtxDone(msg.Err) {
				debug.Log("tree.load_canceled", "kind", focus.Kind, "name", focus.Name)
				return m, nil
			}
			debug.Errf("tree.load_failed", "kind", focus.Kind, "name", focus.Name, "err", msg.Err.Error())
			// Record on the tree so the body can show a retry hint inline
			// (P4.5: distinguish "load failed" from "API returned empty"),
			// AND raise the global banner so an error during a background
			// auto-refresh — when stale rows are still on screen — isn't
			// invisible.
			m.tree.LoadErr = msg.Err
			path := append([]sections.NodeRef(nil), msg.Path...)
			cluster := m.ctx.ActiveContext
			return m, m.raiseReadError(msg.Err.Error(), func() tea.Cmd {
				if m.ctx.ActiveContext != cluster || !slices.Equal(m.tree.Path(), path) {
					return m.raiseError("Return to the original resource view to retry this read")
				}
				m.dismissBanner()
				return m.loadTreeFocus()
			})
		}
		m.tree.SetChildren(msg.Path, msg.Children)
		m.overviewPreview = false
		m.lastRefresh = time.Now()
		persistOverview := m.saveOverview(msg.Children, m.lastRefresh)
		if m.startupNamespace != "" && focus.Kind == k8s.KindContext &&
			m.tree.Focus() == focus && msg.Children != nil {
			ns := m.startupNamespace
			m.startupNamespace = ""
			found := false
			for _, item := range msg.Children.UserNS {
				found = found || item.Name == ns
			}
			for _, name := range msg.Children.SystemNS {
				found = found || name == ns
			}
			if !found {
				return m, m.raiseError(fmt.Sprintf("--namespace=%q not found in context %q", ns, focus.Name))
			}
			return m, tea.Batch(persistOverview, m.tree.Push(sections.NodeRef{
				Kind: k8s.KindNamespaceFolder, Name: ns, Namespace: ns, Display: ns,
			}))
		}
		actCmd := m.syncSelection()
		debug.Log("tree.loaded",
			"kind", focus.Kind,
			"name", focus.Name,
			"children", childCount(msg.Children))
		return m, tea.Batch(actCmd, persistOverview, m.finishPaletteNavigation())

	case sections.ActivityLoadedMsg:
		// Right-stack Activity tile got its events back. Apply drops the
		// result silently when the selection has changed since the fetch
		// was issued — no need for the model to gate again here.
		m.activity.Apply(msg)
		return m, nil

	case sections.InspectorDetailMsg:
		// Inspector detail fetch landed. Apply caches under the
		// (kind, ns, name) key and re-renders if the result is still
		// for the current selection. Stale results don't disturb
		// the live render, but stay cached so a future cursor visit
		// is instant.
		m.inspector.Apply(msg, &m.tree)
		return m, nil

	case sections.ConnectedMsg:
		// Connected-tile fetch landed. Same shape as InspectorDetailMsg:
		// stale results are kept in the cache for future visits but don't
		// disturb the live render.
		if msg.Err != nil && isCtxDone(msg.Err) {
			return m, nil
		}
		m.connected.Apply(msg)
		return m, nil

	case sections.PodSelectedMsg:
		m.logSource = sections.NodeRef{Kind: k8s.KindPod, Name: msg.Name, Namespace: msg.Namespace}
		if msg.Container != "" {
			return m, m.startTargetLogs(logTarget{pod: msg.Name, namespace: msg.Namespace, container: msg.Container}, false)
		}
		return m, m.openLogTargets(m.logSource)

	case sections.ActionDoneMsg:
		if msg.Err != nil {
			if isCtxDone(msg.Err) {
				debug.Log("action.canceled", "action", msg.Action, "subject", msg.Subject)
				return m, nil
			}
			debug.Errf("action.failed", "action", msg.Action, "subject", msg.Subject, "err", msg.Err.Error())
			return m, m.raiseError(fmt.Sprintf("%s: %v", msg.Action, msg.Err))
		}
		debug.Log("action.ok", "action", msg.Action, "subject", msg.Subject)
		// Surface a transient confirmation so the user has visible proof the
		// action took, without waiting up to 5s for the next refresh tick to
		// reflect the new state. Doherty Threshold: a sub-second cue.
		var statusCmd tea.Cmd
		if msg.Target != nil {
			statusCmd = tea.Batch(m.raiseStatus(msg.Action+" accepted · "+msg.Subject), m.trackRecordedOperation(*msg.Target, msg.Action, msg.Operation, msg.Generation))
		} else {
			statusCmd = m.raiseStatus(actionSuccessMsg(msg.Action, msg.Subject))
		}
		// If the user has popped back to the picker while the action was in
		// flight, there's no current view to refresh — issuing a load would
		// just hit the no-op KindContextList path and wipe the seeded list.
		if m.tree.Focus().Kind == k8s.KindContextList {
			return m, statusCmd
		}
		// Refresh in the background: invalidate cache for the current focus +
		// parent and reload, but don't toggle Loading=true — that would flash
		// a spinner over data we already trust. SetChildren swaps in place
		// when the new payload arrives.
		m.tree.InvalidateFocus()
		// Kick a cluster-health refetch immediately so the topbar pill
		// reflects the change (deleted pod, scaled deployment, drained
		// node) without waiting up to 15s for the next throttle tick.
		// Without this the pill stays stale just long enough for the
		// operator to wonder whether the mutation actually took.
		var healthCmd tea.Cmd
		if m.ctx.ActiveContext != "" {
			m.clusterHealthAt = time.Time{}
			healthCmd = m.loadClusterHealth()
		}
		return m, tea.Batch(statusCmd, m.loadTreeFocus(), healthCmd)

	case sections.PortForwardStartedMsg:
		if msg.Err != nil {
			debug.Errf("portforward.failed", "err", msg.Err.Error())
			return m, m.raiseError("forward: " + msg.Err.Error())
		}
		pf := msg.Forward
		// Compose the success banner. When we substituted a free port for
		// the user's requested one, call it out — silent substitution is
		// the kind of surprise that wastes ten minutes of debugging later.
		base := fmt.Sprintf("forwarded · %s/%s %d→%d", strings.ToLower(pf.TargetKind), pf.TargetName, pf.LocalPort, pf.RemotePort)
		if msg.Requested > 0 && msg.Requested != pf.LocalPort {
			base += fmt.Sprintf(" (requested :%d was in use)", msg.Requested)
		}
		// If the user has the list modal open (rare race), refresh it so the
		// new row shows up immediately.
		if m.state == stateForwardsList {
			m.forwards.Refresh(m.ctx.Manager.Forwards.List())
		}
		return m, m.raiseStatus(base)

	case sections.PortForwardStoppedMsg:
		if msg.Err != nil {
			return m, m.raiseError("stop forward: " + msg.Err.Error())
		}
		// Stay in the list modal — refresh so the dropped row disappears
		// and the cursor relocates onto whatever's left (or empty state).
		if m.state == stateForwardsList {
			m.forwards.Refresh(m.ctx.Manager.Forwards.List())
		}
		return m, m.raiseStatus("forward stopped")

	case sections.PodsOnNodeMsg:
		if msg.Err != nil {
			// Surface but don't bail — the user can still confirm with an
			// unknown count if they really mean to drain.
			m.drainConfirmPods = 0
			return m, m.raiseError("count pods: " + msg.Err.Error())
		}
		m.drainConfirmPods = msg.Count
		return m, nil

	case sections.DrainStartedMsg:
		// Swap in the real cancel func now that DrainNode has captured the
		// context, and start reading events. The progress modal re-renders
		// on every event we receive.
		m.drain.Reset(msg.Node, msg.Cancel)
		m.drainCh = msg.Channel
		return m, sections.NextDrainEvent(msg.Channel)

	case sections.DrainProgressMsg:
		done := m.drain.Apply(msg.Event)
		if done {
			// Drain finished — banner success/error, return to tree, and
			// refresh the focus so the node row reflects its new state.
			m.state = stateMain
			if msg.Event.Err != nil {
				return m, m.raiseError("drain: " + msg.Event.Err.Error())
			}
			m.tree.InvalidateFocus()
			return m, tea.Batch(m.raiseStatus("drained · "+m.nodeTarget.Name), m.loadTreeFocus())
		}
		// Still going — request the next event. The DrainStartedMsg case
		// kicked off the first read; we keep the chain going from here.
		// Channel is stored in the goroutine's closure; we don't keep a
		// reference on the model because the only consumer is this very
		// loop, and DrainStartedMsg fed it forward.
		// To support that, we need to keep the channel — store it on the model.
		return m, sections.NextDrainEvent(m.drainCh)

	case sections.LogStreamStartedMsg:
		if msg.Request != 0 && (msg.Request != m.logRequest || m.state != stateLogView) {
			if msg.Ch != nil {
				go func() {
					for range msg.Ch {
					}
				}()
			}
			return m, nil
		}
		if msg.Err != nil {
			m.logs.EndStream(msg.Err)
			return m, m.raiseError("logs: " + msg.Err.Error())
		}
		m.logCh = msg.Ch
		return m, m.nextLogLine()

	case sections.ShellSessionStartedMsg:
		// Abort race: the user can press esc during phaseConnect (which
		// returns leave=true and resets the modal) before the dial goroutine
		// has emitted this message. If we're no longer in the shell view,
		// the session is orphaned — close it so we don't leak the stream.
		if m.state != stateShellView {
			msg.Session.Close()
			msg.Operation.Record("disconnected", "view closed before connection arrived", nil)
			return m, nil
		}
		m.shellJournal = msg.Operation
		m.shellSession = msg.Session
		m.shell.Attach(msg.Session, msg.IsDebug)
		return m, m.nextShellEvent()

	case sections.ShellOutputMsg:
		m.shell.IngestBytes(msg.Bytes)
		return m, m.nextShellEvent()

	case sections.ShellExitMsg:
		if len(msg.Bytes) > 0 {
			m.shell.IngestBytes(msg.Bytes)
		}
		if m.shellSession != nil {
			m.shellJournal.Record("disconnected", "", nil)
			m.shellJournal = nil
			m.shellSession.Close()
			m.shellSession = nil
		}
		m.shell.HandleExit(msg.NoShell, msg.Err)
		if !msg.NoShell && msg.Err == nil {
			m.ctx.ResetBackground()
			m.state = stateMain
		}
		return m, nil

	case sections.EventsLoadedMsg:
		if msg.Err != nil && isCtxDone(msg.Err) {
			return m, nil
		}
		m.events.SetItems(msg.Events, msg.Err)
		return m, nil

	case sections.DescribeLoadedMsg:
		if msg.Err != nil && isCtxDone(msg.Err) {
			return m, nil
		}
		m.describe.SetYAML(msg.YAML, msg.Err)
		return m, nil

	case sections.EditYAMLLoadedMsg:
		if msg.Err != nil {
			if isCtxDone(msg.Err) {
				return m, nil
			}
			return m, m.raiseError(fmt.Sprintf("edit: %v", msg.Err))
		}
		return m, m.startEditor(msg)

	case sections.EditorExitedMsg:
		return m, m.handleEditorExit(msg.Err)

	case logEventMsg:
		if msg.channel != m.logCh {
			return m, nil
		}
		return m.Update(msg.event)

	case k8s.PodLogEvent:
		if msg.Done {
			m.logs.EndStream(msg.Err)
			if msg.Err != nil && !isCtxDone(msg.Err) {
				return m, m.raiseError(fmt.Sprintf("log: %v", msg.Err))
			}
			return m, nil
		}
		if msg.Err != nil {
			m.logs.EndStream(msg.Err)
			if isCtxDone(msg.Err) {
				return m, nil
			}
			return m, m.raiseError(fmt.Sprintf("log: %v", msg.Err))
		}
		if msg.Line != "" {
			m.logs.Append(msg.Line)
		}
		return m, m.nextLogLine()

	case refreshMsg:
		var cmd tea.Cmd
		// Auto-refresh is meaningful only inside a cluster: at the context
		// list level the data is the kubeconfig, which doesn't change at
		// runtime. Skipping the load there also avoids overwriting the
		// pre-seeded context list with an empty payload.
		//
		// We deliberately do NOT set Loading=true or InvalidateFocus here —
		// the auto-tick should refresh data silently in the background. The
		// existing children stay rendered until the new payload arrives via
		// SetChildren, which avoids a 5s loading-flicker on every tick.
		// Cache also stays self-consistent because SetChildren writes both
		// the live children pointer and the cache slot.
		//
		// We do flip Refreshing=true: that drives a small spinner in the
		// panel title (without disturbing the body), giving the operator a
		// quiet "data is updating" cue. Cleared on TreeChildrenLoadedMsg.
		var actCmd tea.Cmd
		var healthCmd tea.Cmd
		if m.focused && m.state == stateMain && m.ctx.ActiveContext != "" &&
			m.tree.Focus().Kind != k8s.KindContextList {
			m.tree.StartRefresh()
			cmd = m.loadTreeFocus()
			debug.Log("refresh.tick", "context", m.ctx.ActiveContext, "focus", m.tree.Focus().Kind)
			// Piggyback the activity-tile re-fetch on the same tick when
			// its own throttle has elapsed. Events change less often than
			// the tree's row data, so the tile's NeedsRefresh check (15s)
			// stops us hammering the API every 5s.
			if m.activity.NeedsRefresh(m.now) {
				actCmd = m.activity.Refresh(m.ctx.Background, m.ctx.Manager, m.ctx.ActiveContext)
			}
			// Cluster-health pill follows the same piggyback pattern: list
			// the whole cluster on its own throttle so the topbar reflects
			// the cluster's state even when the user is parked on a
			// namespace view.
			if m.clusterHealthCtx != m.ctx.ActiveContext ||
				m.clusterHealthAt.IsZero() ||
				m.now.Sub(m.clusterHealthAt) >= clusterHealthRefreshInterval {
				healthCmd = m.loadClusterHealth()
			}
		}
		return m, tea.Batch(m.autoRefreshTick(), cmd, actCmd, healthCmd)

	case clusterHealthMsg:
		// Drop snapshots that came back after a context switch; they refer
		// to a cluster the user is no longer looking at.
		if msg.Context != m.ctx.ActiveContext {
			return m, nil
		}
		if msg.Err != nil {
			// Preserve the previous snapshot — a single API hiccup shouldn't
			// blank the pill. The refresh tick will retry on the next cycle.
			debug.Log("cluster_health.error", "context", msg.Context, "err", msg.Err.Error())
			return m, nil
		}
		m.clusterHealth = msg.Health
		m.clusterHealthAt = m.now
		m.clusterHealthCtx = msg.Context
		return m, nil

	case sections.HotLoadedMsg:
		m.hot.SetItems(msg.Pods, msg.Err)
		return m, nil

	case sections.DiscoveryLoadedMsg:
		if msg.Err != nil && !isCtxDone(msg.Err) {
			// Discovery only feeds the Resource Browser corpus + the
			// custom-fold label resolution. A failure here doesn't break
			// the tree; banner it once so the operator knows the modal
			// will be smaller than expected and move on.
			debug.Errf("k8s.discovery_failed", "err", msg.Err.Error())
			return m, m.raiseError("discovery: " + msg.Err.Error())
		}
		m.tree.SetDiscoveredKinds(msg.Kinds)
		if m.state == stateBrowser {
			// The browser opened before discovery landed; reopen with the
			// fuller corpus now so the user sees everything.
			m.browser.Open(m.tree.DiscoveredKinds(), m.tree.EnabledResources())
		}
		return m, nil

	case errorMsg:
		return m, m.raiseError(msg.err.Error())

	case clearBannerMsg:
		// Only clear if no newer banner has been raised in the meantime.
		// raiseBanner bumps bannerEpoch on every call so an older timer
		// firing after a fresh banner sees a stale epoch and no-ops.
		if msg.epoch == m.bannerEpoch && !m.bannerErr {
			m.bannerMsg = ""
			m.bannerErr = false
		}
		return m, nil

	case sections.SubmitMsg:
		// The chat handed us a draft + crumbs; we own building the
		// [Selection] block from the live tree (the chat doesn't have
		// the tree reference). Wrap it and kick off the subprocess.
		snap := sections.BuildContextSnapshot(&m.tree, m.ctx.ActiveContext)
		full := msg.Prompt
		if snap.Block != "" {
			full = snap.Block + "\n\n" + msg.Prompt
		}
		return m, m.startChatStream(full)

	case sections.ChatTokenMsg:
		if msg.Ch != m.chatStreamCh || !m.chat.Streaming() {
			return m, nil
		}
		m.chat.IngestToken(msg.Delta)
		if m.chatStreamCh != nil {
			return m, sections.NextChatEvent(m.chatStreamCh)
		}
		return m, nil

	case sections.ChatRetryMsg:
		if msg.Ch != m.chatStreamCh || !m.chat.Streaming() {
			return m, nil
		}
		m.chat.IngestRetry(msg.Attempt, msg.Of, msg.DelayMS, msg.Reason)
		if m.chatStreamCh != nil {
			return m, sections.NextChatEvent(m.chatStreamCh)
		}
		return m, nil

	case sections.ChatTurnDoneMsg:
		if msg.Ch != m.chatStreamCh || !m.chat.Streaming() {
			return m, nil
		}
		m.chat.FinishTurn(msg.Err)
		m.chatStreamCh = nil
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.ctx.Spinner, cmd = m.ctx.Spinner.Update(msg)
		return m, cmd

	case tea.PasteMsg:
		if m.state == stateIncoming {
			if m.incoming.mode == "edit" && !m.incoming.busy {
				if len(m.incoming.edit)+len(msg.Content) > 1024 {
					m.incoming.err = "Action parameter is too long"
				} else {
					m.incoming.edit += msg.Content
				}
			}
			return m, nil
		}
		return m, m.handlePaste(msg)

	case tea.MouseClickMsg:
		if m.state == stateIncoming {
			return m, nil
		}
		if m.choices != nil {
			return m, nil
		}
		// Left-click on the workspace tab strip switches to that tab. The
		// stripe lives at (m.tabStripOriginX, m.tabStripOriginY); hit-test
		// translates the click column back into a WorkspaceTab. We
		// synthesize the matching keypress and re-dispatch so the load
		// orchestration (Reset / SetSize / Stream) stays in one place
		// instead of being duplicated for the mouse path.
		if msg.Button == tea.MouseLeft && msg.Y == m.tabStripOriginY {
			localX := msg.X - m.tabStripOriginX
			if localX >= 0 {
				for _, hit := range m.tabHitboxes {
					if localX >= hit.X0 && localX < hit.X1 {
						if synth, ok := tabActivationKey(hit.Tab); ok {
							return m.handleKey(synth)
						}
						return m, nil
					}
				}
			}
		}
		return m, nil

	case tea.MouseWheelMsg:
		if m.state == stateIncoming {
			var cmd tea.Cmd
			m.incoming.viewport, cmd = m.incoming.viewport.Update(msg)
			return m, cmd
		}
		if m.choices != nil {
			var cmd tea.Cmd
			m.choices.model, cmd = m.choices.model.Update(msg)
			return m, cmd
		}
		// Mouse wheel is the natural escape-hatch for "I'm reading and want
		// to scroll without finding the right keybinding." The scrollable
		// workspace tabs (logs / events / yaml / chat) each wrap a bubbles
		// viewport that handles MouseWheelMsg internally — we just forward.
		// Tree pane doesn't use a viewport, so wheel there is a no-op; that
		// matches the v1 behaviour and avoids a confusing partial scroll.
		// This feature applies only to the states listed; all others use the fallback.
		//exhaustive:ignore
		switch m.state {
		case stateLogView:
			cmd, _ := m.logs.Update(msg, m.ctx)
			return m, cmd
		case stateEventsView:
			cmd, _ := m.events.Update(msg, m.ctx)
			return m, cmd
		case stateDescribeView:
			cmd, _ := m.describe.Update(msg, m.ctx)
			return m, cmd
		case stateChatView:
			cmd, _, _ := m.chat.Update(msg, m.ctx)
			return m, cmd
		default:
			return m, nil
		}

	case tea.FocusMsg:
		// The terminal regained focus — resume the work the blur handler
		// suppressed. We rearm both ticks immediately so the operator sees
		// fresh data within a beat of switching back instead of waiting for
		// the next 5s tick to land.
		m.focused = true
		return m, tea.Batch(m.autoRefreshTick(), m.clockTick())

	case tea.BlurMsg:
		// The operator switched to another tmux pane / window. We keep the
		// tick scheduling alive (so the loop survives) but the refreshMsg /
		// clockMsg handlers short-circuit while !m.focused — no API calls,
		// no wall-clock churn. The next FocusMsg snaps everything back.
		m.focused = false
		return m, nil

	case tea.ColorProfileMsg:
		// v2's renderer auto-downsamples truecolor hex values to whatever
		// the terminal supports — we don't need to act on this beyond
		// logging it. Useful for debug: a degraded palette on an ANSI16
		// terminal will be obvious in the journal.
		debug.Log("terminal.color_profile", "profile", msg.String())
		return m, nil

	case tea.BackgroundColorMsg:
		// Latch on the first response; some terminals re-emit on theme
		// changes but a banner each time would be obnoxious. TokyoNight
		// is a dark palette — on a light terminal it reads as a muddy
		// gray wash, so we surface that once so the operator isn't left
		// debugging the render.
		if !m.lightBgWarned && !msg.IsDark() {
			m.lightBgWarned = true
			return m, m.raiseStatus("light terminal detected — safina's palette is dark-optimised")
		}
		return m, nil
	}

	return m, nil
}

// handleKey is the top-level key dispatcher. Modal states (logs, events,
// scale, confirm, help) consume keys before the main view sees them.
// sectionHasFilterFocus returns true when the active workspace section
// has its filter input focused (any printable key the operator types is
// going into a search query, not a global hotkey). Used to gate global
// shortcuts like the `1` tab-switch.
func (m *Model) sectionHasFilterFocus() bool {
	if m.treeFocusInImmersive {
		return false
	}
	// This feature applies only to the states listed; all others use the fallback.
	//exhaustive:ignore
	switch m.state {
	case stateLogView:
		return m.logs.FilterActive()
	case stateEventsView:
		return m.events.FilterActive()
	case stateDescribeView:
		return m.describe.FilterActive()
	case stateChatView:
		// Chat is always "filter active" — every key is a textarea char.
		return true
	default:
		return false
	}
}

// isImmersiveSwitchKey reports whether the keystroke is one of the global
// action verbs the tree pane responds to. When the operator is inside an
// immersive workspace tab (logs/events/yaml/etc.) we want these to bounce
// through returnToMain → handleTreeAction instead of being swallowed by
// the section, so jumping logs→events doesn't require an Esc step. The
// list mirrors the entries in handleTreeAction and the "global modal" keys
// (Hot, Forwards, ClusterSwitch, Refresh, Chat, Configure, Help, palette)
// that also do nothing inside a section's Update right now.
func (m *Model) isImmersiveSwitchKey(msg tea.KeyPressMsg) bool {
	k := m.ctx.Keys
	// Section-local verbs take precedence over resource shortcuts. In
	// particular K in the forwards list must never target the selected pod.
	if m.state == stateForwardsList && key.Matches(msg, k.Delete) ||
		m.state == stateLogView && msg.String() == "f" ||
		(m.state == stateEventsView || m.state == stateDescribeView || m.state == stateHotView) && key.Matches(msg, k.Refresh) {
		return false
	}
	switch {
	case key.Matches(msg, k.Logs),
		key.Matches(msg, k.Events),
		key.Matches(msg, k.Describe),
		key.Matches(msg, k.Shell),
		key.Matches(msg, k.Delete),
		key.Matches(msg, k.Scale),
		key.Matches(msg, k.Rollback),
		key.Matches(msg, k.Restart),
		key.Matches(msg, k.Edit),
		key.Matches(msg, k.Reveal),
		key.Matches(msg, k.PortForward),
		key.Matches(msg, k.Cordon),
		key.Matches(msg, k.Drain),
		key.Matches(msg, k.Hot),
		key.Matches(msg, k.Forwards),
		key.Matches(msg, k.ClusterSwitch),
		key.Matches(msg, k.Refresh),
		key.Matches(msg, k.Ask),
		key.Matches(msg, k.Configure),
		key.Matches(msg, k.Help):
		return true
	}
	if msg.String() == ":" {
		return true // palette
	}
	return false
}

// tabActivationKey maps a workspace tab to the KeyPressMsg the operator
// would type to activate it. Mouse clicks on the tab strip re-dispatch
// through handleKey so the load orchestration (Reset / SetSize / Stream)
// lives in exactly one place. Returns ok=false for tabs that don't have
// a discoverable activation hotkey (Drain — it appears only while the op
// is already running, so there's nothing to "activate"; Forwards — Ctrl+F
// is a chord we synthesize with ModCtrl).
func tabActivationKey(tab sections.WorkspaceTab) (tea.KeyPressMsg, bool) {
	switch tab {
	case sections.WSTabInspector:
		return tea.KeyPressMsg{Text: "1", Code: '1'}, true
	case sections.WSTabLogs:
		return tea.KeyPressMsg{Text: "l", Code: 'l'}, true
	case sections.WSTabEvents:
		return tea.KeyPressMsg{Text: "e", Code: 'e'}, true
	case sections.WSTabYaml:
		// The Yaml tab is the Describe state — `d` is the hotkey for both.
		return tea.KeyPressMsg{Text: "d", Code: 'd'}, true
	case sections.WSTabChat:
		return tea.KeyPressMsg{Text: "i", Code: 'i'}, true
	case sections.WSTabHot:
		return tea.KeyPressMsg{Text: "h", Code: 'h'}, true
	case sections.WSTabForwards:
		// Ctrl+F: Text is empty for non-printable chords; the matcher reads
		// Code + Mod.
		return tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl}, true
	case sections.WSTabDrain:
		// No discoverable activation hotkey.
	}
	return tea.KeyPressMsg{}, false
}

// cycleWorkspaceTab moves to the next or previous tab in the workspace
// strip and routes activation through handleKey so the load orchestration
// (Reset / SetSize / Stream) stays in one place. Cycles across the
// availability set computed for the current selection — so the strip the
// operator sees on screen is what they cycle through.
//
// Stops at the edges instead of wrapping: pressing ← on Inspector (the
// leftmost tab) is a no-op rather than silently jumping to the far end.
// Wraparound is unpredictable when the rightmost tab is Chat — the chat
// textarea consumes ←/→ for cursor movement, so a wraparound would land
// in a state where the operator can't easily cycle back out.
//
// When the next tab is Inspector we call returnToMain instead of
// synthesizing a keypress: Inspector is the "no immersive state" view, so
// it's activated by clearing state rather than pressing a verb. Peek mode
// is preserved across the cycle so the workspace keeps focus until the
// operator presses Tab again or hits Esc.
func (m *Model) cycleWorkspaceTab(forward bool) (tea.Model, tea.Cmd) {
	tabs := sections.AvailableTabs(m.workspaceTabAvailability())
	if len(tabs) < 2 {
		return m, nil
	}
	active := m.activeWorkspaceTab()
	idx := -1
	for i, t := range tabs {
		if t == active {
			idx = i
			break
		}
	}
	if idx < 0 {
		idx = 0
	}
	next := idx - 1
	if forward {
		next = idx + 1
	}
	if next < 0 || next >= len(tabs) {
		// At the edge — no-op rather than wrap. Keeps the cycle
		// directionally predictable for the operator.
		return m, nil
	}
	target := tabs[next]
	if target == active {
		return m, nil
	}
	// Skip stateMain between immersive tabs: peek stays true so
	// workspaceOwnsFocus() never flips false during the transition,
	// preventing the tree from briefly rendering as focused.
	m.cleanupImmersiveState()
	m.peekWorkspace = true
	m.tree.Focused = false
	m.treeFocusInImmersive = false
	m.state = stateMain

	if target == sections.WSTabInspector {
		return m, nil
	}
	synth, ok := tabActivationKey(target)
	if !ok {
		return m, nil
	}
	return m.handleKey(synth)
}

// isImmersiveState reports whether the model is in one of the states
// whose per-state Update normally consumes keystrokes (logs, events,
// describe, chat, hot, forwards, drain). Used to gate the "tree owns
// keys after Tab" path in handleKey — when this is true and
// m.tree.Focused was set by the operator tabbing away from the
// section, key dispatch bypasses the section and falls through to
// the stateMain keymap.
func (m *Model) isImmersiveState() bool {
	// This feature applies only to the states listed; all others use the fallback.
	//exhaustive:ignore
	switch m.state {
	case stateLogView, stateEventsView, stateDescribeView,
		stateChatView, stateHotView, stateForwardsList, stateDrainProgress:
		return true
	default:
		return false
	}
}

// returnToMain closes whatever immersive workspace tab is active and
// returns focus to the tree. Same cleanup the individual section-Esc
// handlers do, lifted here so the global `1` tab-switch can call it
// without duplicating per-state teardown.
func (m *Model) returnToMain() {
	m.cleanupImmersiveState()
	m.state = stateMain
	m.treeFocusInImmersive = false
	// tree.Focused is left to renderMainSplit's authoritative compute
	// from workspaceOwnsFocus — setting it true here flashed the
	// focused gradient on the tree when returnToMain was used as a
	// teardown step before re-dispatching into another immersive
	// state (action-key branch's stateMain bounce).
}

// cleanupImmersiveState runs per-state teardown without touching state /
// focus. Separated from returnToMain so tab-cycle transitions can clean
// up the prior tab without an intermediate stateMain frame.
func (m *Model) cleanupImmersiveState() {
	// This feature applies only to the states listed; all others use the fallback.
	//exhaustive:ignore
	switch m.state {
	case stateLogView:
		m.stopLogStream()
	case stateChatView:
		m.chat.SetFocus(false)
	default:
		// remaining states hold no immersive resources to tear down
	}
}

func (m *Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	// Ctrl+C is the terminal-wide exit, including overlays and focused
	// filters. Shells and drains own it as interrupt/cancel instead.
	if msg.String() == "ctrl+c" && m.state != stateShellView && m.state != stateDrainProgress {
		m.shutdown()
		m.ctx.Cancel()
		return m, tea.Quit
	}
	if m.state == stateIncoming {
		return m.handleIncoming(msg)
	}
	if m.choices != nil {
		return m.handleChoices(msg)
	}
	if m.state != stateShellView {
		if msg.String() == "ctrl+n" {
			m.openNotifications()
			return m, nil
		}
		if msg.String() == "ctrl+d" && m.bannerMsg != "" {
			m.dismissBanner()
			return m, nil
		}
	}
	if m.state == stateLogView && !m.treeFocusInImmersive && !m.logs.FilterActive() {
		if msg.String() == "c" {
			return m, m.openLogTargets(m.logSource)
		}
		if msg.String() == "l" && m.logTarget.pod != "" {
			return m, m.startTargetLogs(m.logTarget, false)
		}
		if msg.String() == "p" && m.logTarget.previous {
			return m, m.startTargetLogs(m.logTarget, true)
		}
	}
	// A new user interaction cancels any action waiting for a palette load.
	m.paletteTarget = nil
	m.paletteAction = ""
	// Help is an overlay: preserve the current view and its running work.
	if key.Matches(msg, m.ctx.Keys.Help) && (m.state == stateMain || m.isImmersiveState()) &&
		!m.tree.FilterActive() && !m.sectionHasFilterFocus() {
		m.helpReturnState = m.state
		m.helpViewport.GotoTop()
		m.state = stateHelp
		return m, nil
	}
	// Yank (`y`) is intercepted before the per-state dispatch in every state
	// where it makes sense — the section exposes its payload via YankPayload
	// and a FilterActive guard so the user can still type a literal `y` into
	// the filter prompt. Shell forwards `y` to the pod, so it's excluded.
	if cmd, yanked := m.tryYank(msg); yanked {
		return m, cmd
	}

	if msg.String() == "z" && m.canExpandWorkspace() && !m.sectionHasFilterFocus() && !m.tree.FilterActive() {
		m.workspaceExpanded = !m.workspaceExpanded
		m.treeFocusInImmersive = false
		return m, nil
	}
	// Tab toggles pane focus. In stateMain it's peekWorkspace; in
	// immersive states it's treeFocusInImmersive (keys go to tree while
	// the section keeps rendering).
	if msg.String() == "tab" && (m.state == stateMain || m.isImmersiveState()) && (m.state == stateChatView || !m.sectionHasFilterFocus()) && !m.tree.FilterActive() {
		if m.isImmersiveState() {
			m.workspaceExpanded = false
			m.treeFocusInImmersive = !m.treeFocusInImmersive
			m.tree.Focused = m.treeFocusInImmersive
			if m.state == stateChatView {
				m.chat.SetFocus(!m.treeFocusInImmersive)
			}
		} else {
			m.peekWorkspace = !m.peekWorkspace
			m.tree.Focused = !m.peekWorkspace
		}
		return m, nil
	}

	// Esc / q in tab-to-tree closes the immersive surface instead of
	// popping the tree (Esc) or quitting the app (q). A sticky tree
	// filter still consumes Esc first.
	if m.treeFocusInImmersive && m.isImmersiveState() {
		if key.Matches(msg, m.ctx.Keys.Back) && !m.tree.FilterActive() && m.tree.FilterValue() == "" {
			m.returnToMain()
			return m, nil
		}
		if msg.String() == "q" && !m.tree.FilterActive() {
			m.returnToMain()
			return m, nil
		}
	}

	// Workspace tab quick-switch: when the workspace owns focus (any
	// immersive tab is active, or the operator pressed Tab to peek),
	// `1` jumps to Inspector and ←/→ cycle through the visible tabs.
	// Action keys (l/e/d/s/K/S/U/R/E/v/f/C/D) are hoisted out of the
	// section's Update so the operator can jump immersive→immersive
	// without the Esc bounce — they target the same tree selection that
	// was live when we entered the section. The filter-focus guard keeps
	// these letters typeable inside `/` queries.
	if m.workspaceOwnsFocus() && !m.sectionHasFilterFocus() {
		switch msg.String() {
		case "1":
			m.returnToMain()
			return m, nil
		case "left", "right":
			return m.cycleWorkspaceTab(msg.String() == "right")
		}
		if m.isImmersiveSwitchKey(msg) {
			// Teardown current immersive in-place, then fall through
			// to the rest of handleKey. peek stays true so
			// workspaceOwnsFocus() never flips false mid-dispatch —
			// the previous recursion path cleared peek before
			// re-entering, causing a one-frame "tree should be
			// focused" intermediate the terminal caught as a flicker.
			m.cleanupImmersiveState()
			m.state = stateMain
			m.treeFocusInImmersive = false
		}
	}

	// When Tab moved focus to the tree while an immersive surface is
	// rendered, bypass the section's Update and fall through to the
	// stateMain keymap.
	treeOwnsKeysInImmersive := m.treeFocusInImmersive && m.isImmersiveState()

	switch m.state {
	case stateIncoming:
		return m.handleIncoming(msg)
	case stateMain, stateChatView:
		// Main workspace input is handled below.
	case stateLogView:
		if treeOwnsKeysInImmersive {
			break
		}
		cmd, leave := m.logs.Update(msg, m.ctx)
		if leave {
			m.stopLogStream()
			m.state = stateMain
			return m, nil
		}

		return m, cmd

	case stateEventsView:
		if treeOwnsKeysInImmersive {
			break
		}
		cmd, leave := m.events.Update(msg, m.ctx)
		if leave {
			m.state = stateMain
			return m, nil
		}
		return m, cmd

	case stateDescribeView:
		if treeOwnsKeysInImmersive {
			break
		}
		cmd, leave := m.describe.Update(msg, m.ctx)
		if leave {
			m.state = stateMain
			return m, nil
		}
		return m, cmd

	case stateShellView:
		cmd, leave := m.shell.Update(msg, m.ctx)
		if leave {
			if m.shellSession != nil {
				m.shellJournal.Record("disconnected", "", nil)
				m.shellJournal = nil
				m.shellSession.Close()
				m.shellSession = nil
			}
			m.ctx.ResetBackground()
			m.state = stateMain
			return m, nil
		}
		return m, cmd

	case stateScaleInput:
		return m.handleScaleInput(msg)

	case statePortForwardInput:
		return m.handlePortForwardInput(msg)

	case stateForwardsList:
		if treeOwnsKeysInImmersive {
			break
		}
		return m.handleForwardsList(msg)

	case stateDeleteConfirm, stateRollbackConfirm, stateRestartConfirm, stateRestartPodConfirm,
		stateEditConfirm, stateCordonConfirm, stateDrainConfirm:
		return m, m.handleConfirm(msg)

	case stateDrainProgress:
		return m.handleDrainProgress(msg)

	case stateHelp:
		if key.Matches(msg, m.ctx.Keys.Back) || key.Matches(msg, m.ctx.Keys.Help) || msg.String() == "q" {
			m.state = m.helpReturnState
			return m, nil
		}
		var cmd tea.Cmd
		m.helpViewport, cmd = m.helpViewport.Update(msg)
		return m, cmd

	case stateHotView:
		if treeOwnsKeysInImmersive {
			break
		}
		switch {
		case key.Matches(msg, m.ctx.Keys.Back), msg.String() == "q":
			m.state = stateMain
			return m, nil
		case key.Matches(msg, m.ctx.Keys.Refresh):
			m.recordSelection("refresh")
			m.hot.Reset()
			return m, sections.LoadHot(m.ctx.Background, m.ctx.Manager, m.ctx.ActiveContext)
		case key.Matches(msg, m.ctx.Keys.Up):
			m.hot.CursorUp()
			return m, nil
		case key.Matches(msg, m.ctx.Keys.Down):
			m.hot.CursorDown()
			return m, nil
		}
		return m, nil

	case statePalette:
		return m.handlePalette(msg)

	case stateClusters:
		return m.handleClusterSwitch(msg)

	case stateBrowser:
		return m.handleBrowser(msg)
	default:
		// stateMain / stateChatView — fall through to the post-switch dispatch
		// (Chat textarea + global stateMain keymap below).
	}

	// Chat tab keystroke routing. When the Chat tab is the active workspace
	// surface, the textarea consumes keystrokes. Esc clears a draft, cancels
	// a stream, or closes an idle chat. Tab is handled above and preserves
	// the chat while moving pane focus. Skipped when the operator tabbed
	// to the tree while the chat tab stayed active (treeOwnsKeysInImmersive),
	// so arrows/drill target the tree instead of the textarea.
	if m.state == stateChatView && !treeOwnsKeysInImmersive {
		cmd, submit, leave := m.chat.Update(msg, m.ctx)
		if leave {
			m.chat.SetFocus(false)
			m.tree.Focused = true
			m.state = stateMain
			return m, cmd
		}
		if submit != nil {
			snap := sections.BuildContextSnapshot(&m.tree, m.ctx.ActiveContext)
			full := submit.Prompt
			if snap.Block != "" {
				full = snap.Block + "\n\n" + submit.Prompt
			}
			return m, tea.Batch(cmd, m.startChatStream(full))
		}
		return m, cmd
	}

	// Main state — global keys first, then tree. Chat drawer focus is
	// handled above (the drawer captures keystrokes when open + focused);
	// here the operator is interacting with the tree / workspace.
	if m.tree.FilterActive() && msg.String() != "ctrl+c" {
		cmd := m.tree.Update(msg, m.ctx)
		return m, tea.Batch(cmd, m.syncSelection())
	}

	switch {
	case key.Matches(msg, m.ctx.Keys.Quit):
		m.shutdown()
		m.ctx.Cancel()
		return m, tea.Quit

	case msg.String() == ":" && !m.tree.FilterActive():
		// `:` Vim-style command palette. Indexed lazily off the tree's
		// level cache so it gets richer as the operator drills around.
		// FilterActive check keeps `:` literal inside the / filter input.
		m.searchRequest++
		m.searchLoading = false
		entries := sections.BuildPaletteIndex(m.tree.Cache())
		entries = append([]sections.PaletteEntry{{Kind: "Incoming", Name: "Incoming proposals", Hint: "Review agent proposals and broker outcomes"}, {Kind: "History", Name: "Logbook", Hint: "What you did and saw, saved across sessions"}}, entries...)
		m.palette.Open(entries)
		m.recordSelection("search")
		m.state = statePalette
		return m, nil

	case key.Matches(msg, m.ctx.Keys.Help):
		m.helpReturnState = m.state
		m.helpViewport.GotoTop()
		m.state = stateHelp
		return m, nil

	case key.Matches(msg, m.ctx.Keys.Forwards):
		// Forwards list is process-global — accessible from main state
		// regardless of which row the tree cursor is on. Drops you into
		// the modal even when there are zero forwards (empty-state copy
		// teaches the verb).
		m.forwards.Refresh(m.ctx.Manager.Forwards.List())
		m.state = stateForwardsList
		m.treeFocusInImmersive = false
		return m, nil

	case key.Matches(msg, m.ctx.Keys.Hot) && !m.tree.FilterActive():
		// Hot pods modal is process-global, like Forwards — opens
		// regardless of the tree cursor. Gated on FilterActive so the
		// user can type a literal `H` into the filter input.
		if m.ctx.ActiveContext == "" {
			return m, nil
		}
		m.hot.Reset()
		m.state = stateHotView
		m.treeFocusInImmersive = false
		return m, sections.LoadHot(m.ctx.Background, m.ctx.Manager, m.ctx.ActiveContext)

	case key.Matches(msg, m.ctx.Keys.Configure) && !m.tree.FilterActive():
		// Resource Browser — opens from anywhere inside a cluster. At
		// the cluster picker there's no discovery yet, so we no-op
		// rather than launch an empty modal. Gated on FilterActive so
		// the user can type a literal `,` into the filter input.
		if m.ctx.ActiveContext == "" {
			return m, nil
		}
		return m.openBrowser()

	case key.Matches(msg, m.ctx.Keys.Refresh):
		m.recordSelection("refresh")
		if m.ctx.ActiveContext != "" && m.tree.Focus().Kind != k8s.KindContextList {
			m.tree.InvalidateFocus()
			m.tree.Loading = true
			m.tree.StartRefresh()
			return m, m.loadTreeFocus()
		}
		return m, nil

	case key.Matches(msg, m.ctx.Keys.Ask) && !m.tree.FilterActive():
		// Open the Chat tab. The chat is just another workspace tab — Esc
		// (or `1` for Inspector) closes it. FilterActive gate lets the
		// user type a literal `i` into the filter prompt without flipping
		// to chat.
		m.peekWorkspace = false
		m.tree.Focused = false
		m.chat.SetFocus(true)
		m.syncChatContext()
		m.state = stateChatView
		m.treeFocusInImmersive = false
		return m, nil

	case key.Matches(msg, m.ctx.Keys.ClusterSwitch) && !m.tree.FilterActive():
		// Ctrl+K cluster-switcher overlay. Pre-populate with the list we
		// cached at startup; the overlay positions its cursor on the active
		// context so "you are here" is the default.
		m.clusters.Open(m.allContexts, m.ctx.ActiveContext)
		m.state = stateClusters
		return m, nil
	}

	// Action keys (kind-aware) — clear tab-to-tree override and pre-set
	// tree.Focused so it agrees with the next render's workspaceOwnsFocus.
	if cmd, handled := m.handleTreeAction(msg); handled {
		m.treeFocusInImmersive = false
		if m.isImmersiveState() {
			m.tree.Focused = false
		}
		return m, cmd
	}

	// Fall through to the tree itself for navigation / drill / filter.
	cmd := m.tree.Update(msg, m.ctx)
	// Tree state may have moved — refresh chat's context strip + chips,
	// and the right-stack tiles (Inspector / Activity).
	actCmd := m.syncSelection()
	return m, tea.Batch(cmd, actCmd)
}

// handlePalette keeps search text separate from Tab-enabled actions.
func (m *Model) handlePalette(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+s" {
		if m.searchLoading {
			return m, nil
		}
		path := m.tree.Path()
		if entry, ok := m.palette.Selected(); ok && (entry.Kind == k8s.KindNamespace || entry.Kind == k8s.KindNamespaceFolder) {
			path = entry.Path
		}
		for i := len(path) - 1; i >= 0; i-- {
			if path[i].Kind == k8s.KindNamespaceFolder || path[i].Kind == k8s.KindNamespace {
				m.searchRequest++
				m.searchLoading = true
				m.palette.SetScope("Searching " + path[i].Name + "…")
				return m, sections.SearchNamespace(m.ctx.Background, m.ctx.Manager, m.ctx.ActiveContext, path[i].Name, append([]sections.NodeRef(nil), path[:i+1]...), m.searchRequest)
			}
		}
		return m, m.raiseError("Open a namespace first, then use ctrl+s to search its core resources")
	}

	switch {
	case msg.String() == "tab":
		m.palette.ToggleActions()
		return m, nil
	case key.Matches(msg, m.ctx.Keys.Back):
		m.state = stateMain
		return m, nil
	case msg.String() == "up":
		m.palette.CursorUp()
		return m, nil
	case msg.String() == "down":
		m.palette.CursorDown()
		return m, nil
	case key.Matches(msg, m.ctx.Keys.Enter):
		if entry, ok := m.palette.Selected(); ok {
			m.state = stateMain
			if entry.Kind == "Incoming" {
				return m, m.openIncoming()
			}
			if entry.Kind == "History" {
				return m, m.openHistory()
			}
			return m.drillToPaletteEntry(entry)
		}
		return m, nil
	}

	s := msg.String()
	if m.palette.ActionsActive() {
		if entry, ok := m.palette.Selected(); ok {
			for _, v := range sections.PaletteVerbsFor(entry) {
				if s == v.Letter {
					m.state = stateMain
					return m.dispatchPaletteVerb(s, entry)
				}
			}
		}
		return m, nil
	}

	return m, m.palette.UpdateQuery(msg)
}

// openBrowser opens the Resource Browser modal and kicks off a discovery
// fetch if one hasn't landed yet for the current cluster. The modal
// renders immediately with whatever discovery cache the tree already
// has; when the fetch completes the modal re-seeds via the
// DiscoveryLoadedMsg handler.
// handleClusterSwitch dispatches keys while the Ctrl+K cluster-switcher
// overlay is open. Selecting a cluster fires the same drill the tree's
// root pick would (PushContext), so the path that lands on a new context
// is identical to the cold-start flow.
func (m *Model) handleClusterSwitch(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.ctx.Keys.Back):
		m.state = stateMain
		return m, nil
	case msg.String() == "up":
		m.clusters.CursorUp()
		return m, nil
	case msg.String() == "down":
		m.clusters.CursorDown()
		return m, nil
	case key.Matches(msg, m.ctx.Keys.Enter):
		it, ok := m.clusters.Selected()
		if !ok {
			return m, nil
		}
		m.state = stateMain
		// Pop the tree back to root so the breadcrumb starts clean, then
		// push into the chosen context — same flow as the startup picker.
		// If the user picked the already-active context, do nothing (avoids
		// dropping caches for a no-op switch).
		if it.Name == m.ctx.ActiveContext && m.tree.Focus().Kind != k8s.KindContextList {
			return m, nil
		}
		// Reset the tree to root so the new context drill behaves like a
		// cold start (per the "no auto-drill" memory rule, the operator
		// lands at the cluster level — not on a deep saved cursor).
		for m.tree.Focus().Kind != k8s.KindContextList {
			cmd := m.tree.Pop()
			if cmd == nil {
				break
			}
		}
		return m, m.tree.PushContext(it.Name)
	}

	return m, m.clusters.UpdateQuery(msg)
}

func (m *Model) openBrowser() (tea.Model, tea.Cmd) {
	m.browser.Open(m.tree.DiscoveredKinds(), m.tree.EnabledResources())
	m.state = stateBrowser
	if m.ctx.ActiveContext == "" || m.discoveryByContext[m.ctx.ActiveContext] {
		return m, nil
	}
	m.discoveryByContext[m.ctx.ActiveContext] = true
	return m, sections.LoadDiscovery(m.ctx.Background, m.ctx.Manager, m.ctx.ActiveContext)
}

// handleBrowser dispatches keys while the Resource Browser modal is
// open. Single-purpose: the user toggles which resource kinds should
// appear as folds in the tree, then saves. Space toggles, Enter saves
// the selection to ~/.safina/config.yaml and refreshes the tree, Esc
// cancels. Everything else edits the fuzzy query.
func (m *Model) handleBrowser(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.ctx.Keys.Back):
		m.browser.Close()
		m.state = stateMain
		return m, nil
	case msg.String() == "up":
		m.browser.CursorUp()
		return m, nil
	case msg.String() == "down":
		m.browser.CursorDown()
		return m, nil
	case key.Matches(msg, m.ctx.Keys.Enter):
		refs := m.browser.Confirm()
		m.state = stateMain
		m.tree.SetEnabledResources(refs)
		// AppCfg is set by NewModel; guard nil defensively so a future
		// caller that forgets to seed it can't silently swallow the
		// user's choice. The pick MUST hit disk — that's the entire
		// promise of the modal.
		if m.ctx.AppCfg == nil {
			m.ctx.AppCfg = &config.AppConfig{}
		}
		m.ctx.AppCfg.EnabledResources = refs
		if err := config.Save(m.ctx.AppCfg); err != nil {
			debug.Errf("config.save_failed", "err", err.Error())
			return m, m.raiseError("save: " + err.Error())
		}
		// Folds at the namespace level changed shape — invalidate the
		// current focus so the tree re-derives visibleRows and the new
		// fold set lands on screen.
		m.tree.InvalidateFocus()
		return m, tea.Batch(m.loadTreeFocus(), m.raiseStatus("resources saved"))
	}

	s := msg.String()
	if s == "space" || s == " " {
		m.browser.Toggle()
		return m, nil
	}
	return m, m.browser.UpdateQuery(msg)
}

// drillToPaletteEntry selects the result once its parent has finished loading.
func (m *Model) drillToPaletteEntry(e sections.PaletteEntry) (tea.Model, tea.Cmd) {
	m.paletteTarget = nil
	if len(e.Path) <= 1 || e.Kind == k8s.KindContext ||
		(m.paletteAction == "" && (e.Kind == k8s.KindNamespace || e.Kind == k8s.KindNamespaceFolder)) {
		m.paletteAction = ""
		return m, m.tree.NavigateTo(e.Path)
	}
	m.paletteTarget = &e
	return m, m.tree.NavigateTo(e.Path[:len(e.Path)-1])
}

func (m *Model) finishPaletteNavigation() tea.Cmd {
	e := m.paletteTarget
	if e == nil || m.tree.Children() == nil {
		return nil
	}
	m.paletteTarget = nil
	verb := m.paletteAction
	m.paletteAction = ""
	if !slices.Equal(m.tree.Path(), e.Path[:len(e.Path)-1]) {
		return nil // the operator navigated away while the load was pending
	}
	m.tree.SetCursorByRef(e.Kind, e.Namespace, e.Name)
	ref := m.tree.SelectedRef()
	if ref.Kind != e.Kind || ref.Namespace != e.Namespace || ref.Name != e.Name {
		return m.raiseError("resource is no longer available: " + e.Name)
	}
	selection := m.syncSelection()
	if verb == "" {
		return selection
	}
	cmd, _ := m.handleTreeAction(tea.KeyPressMsg{Code: rune(verb[0]), Text: verb})
	return tea.Batch(selection, cmd)
}

// Palette verbs use the same target selection and confirmation paths as
// ordinary tree actions. A cold destination loads before the action fires.
func (m *Model) dispatchPaletteVerb(verb string, e sections.PaletteEntry) (tea.Model, tea.Cmd) {
	m.paletteAction = verb
	return m.drillToPaletteEntry(e)
}

// handleTreeAction routes resource-action keys (s/r/x/l/e/d/Ctrl-D/S) based
// on the kind of the currently highlighted tree row. Returns (cmd, handled).
func (m *Model) handleTreeAction(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	ref := m.tree.SelectedRef()
	switch {
	case key.Matches(msg, m.ctx.Keys.Scale):
		if ref.Kind == k8s.KindApp && (ref.SubKind == k8s.AppKindDeployment || ref.SubKind == k8s.AppKindStatefulSet) {
			m.state = stateScaleInput
			m.scaleError = ""
			// Pre-fill with the current desired count so the input acts as
			// a nudgeable delta (operator usually adjusts by ±1 or ±a few,
			// not a fresh absolute). Falls back to blank when we can't read
			// the count off the row.
			prefill := ""
			if app, ok := m.tree.SelectedAppInfo(); ok {
				if cur := currentDesiredReplicas(app); cur >= 0 {
					prefill = strconv.Itoa(cur)
				}
			}
			m.scaleInput.SetValue(prefill)
			m.scaleInput.CursorEnd()
			m.scaleInput.Focus()
			return nil, true
		}
	case key.Matches(msg, m.ctx.Keys.Rollback):
		if ref.Kind == k8s.KindApp && ref.SubKind == k8s.AppKindDeployment {
			m.state = stateRollbackConfirm
			return nil, true
		}
	case key.Matches(msg, m.ctx.Keys.Restart):
		switch ref.Kind {
		case k8s.KindApp:
			if ref.SubKind == k8s.AppKindDeployment || ref.SubKind == k8s.AppKindStatefulSet || ref.SubKind == k8s.AppKindDaemonSet {
				m.state = stateRestartConfirm
				return nil, true
			}
		case k8s.KindPod:
			// Restart-a-pod is delete-and-let-the-controller-recreate at the
			// API level, but the keymap promised "restart" — keep the verb
			// honest in the modal and banner by routing through a separate
			// confirm state.
			m.state = stateRestartPodConfirm
			return nil, true
		}
	case key.Matches(msg, m.ctx.Keys.Delete):
		if deletableKind(ref) {
			m.state = stateDeleteConfirm
			return nil, true
		}
	case key.Matches(msg, m.ctx.Keys.PortForward):
		var kind string
		switch ref.Kind {
		case k8s.KindPod:
			kind = k8s.PortForwardTargetPod
		case k8s.KindService:
			kind = k8s.PortForwardTargetService
		default:
			return nil, false
		}
		m.pfTarget.Kind = kind
		m.pfTarget.Namespace = ref.Namespace
		m.pfTarget.Name = ref.Name
		m.pfInput.SetValue("")
		m.pfError = ""
		m.pfInput.Focus()
		m.state = statePortForwardInput
		return nil, true
	case key.Matches(msg, m.ctx.Keys.Cordon):
		if ref.Kind != k8s.KindNode {
			return nil, false
		}
		info, ok := m.tree.SelectedNodeInfo()
		if !ok {
			return nil, false
		}
		m.nodeTarget.Name = info.Name
		m.nodeTarget.Schedulable = info.Schedulable
		m.state = stateCordonConfirm
		return nil, true
	case key.Matches(msg, m.ctx.Keys.Drain):
		if ref.Kind != k8s.KindNode {
			return nil, false
		}
		info, ok := m.tree.SelectedNodeInfo()
		if !ok {
			return nil, false
		}
		m.nodeTarget.Name = info.Name
		m.nodeTarget.Schedulable = info.Schedulable
		m.drainConfirmPods = -1
		m.state = stateDrainConfirm
		// Fetch the pod count async; the confirm modal renders "?"
		// while we wait. Fast on most clusters but we don't want to
		// block the keypress on the API.
		return sections.CountPodsOnNode(m.ctx.Background, m.ctx.Manager, m.ctx.ActiveContext, info.Name), true
	case key.Matches(msg, m.ctx.Keys.Logs):
		switch ref.Kind {
		case k8s.KindPod, k8s.KindApp:
			return m.openLogTargets(ref), true
		case k8s.KindContainer:
			pod := m.tree.Focus()
			m.logSource = pod
			return m.startTargetLogs(logTarget{pod: pod.Name, namespace: pod.Namespace, container: ref.Name}, false), true
		}

	case key.Matches(msg, m.ctx.Keys.Shell):
		switch ref.Kind {
		case k8s.KindPod:
			m.state = stateShellView
			m.shell.SetSize(m.ctx.Width, m.ctx.Height)
			m.shell.Reset(m.ctx.ActiveContext, ref.Namespace, ref.Name, "")
			return sections.OpenShell(m.ctx.Background, m.ctx.Manager, m.ctx.ActiveContext, ref.Namespace, ref.Name, ""), true
		case k8s.KindContainer:
			pod := m.tree.Focus()
			m.state = stateShellView
			m.shell.SetSize(m.ctx.Width, m.ctx.Height)
			m.shell.Reset(m.ctx.ActiveContext, pod.Namespace, pod.Name, ref.Name)
			return sections.OpenShell(m.ctx.Background, m.ctx.Manager, m.ctx.ActiveContext, pod.Namespace, pod.Name, ref.Name), true
		}
	case key.Matches(msg, m.ctx.Keys.Events):
		kind, ns, name := actionTarget(ref)
		if ref.Kind == k8s.KindContainer {
			// Containers aren't independent API objects; events live on
			// the parent Pod. The tree's focus at a container row is
			// the pod itself, so route events through it.
			pod := m.tree.Focus()
			kind, ns, name = "Pod", pod.Namespace, pod.Name
		}
		if kind == "" {
			return nil, false
		}
		m.state = stateEventsView
		m.events.Reset(kind, name, ns)
		m.events.SetSize(m.ctx.Width, m.ctx.Height)
		return sections.LoadEvents(m.ctx.Background, m.ctx.Manager, m.ctx.ActiveContext, ns, kind, name), true
	case key.Matches(msg, m.ctx.Keys.Describe):
		kind, ns, name := actionTarget(ref)
		if ref.Kind == k8s.KindContainer {
			// Same parent-pod indirection as Events — describe the pod
			// and let the operator scroll to the container's spec.
			pod := m.tree.Focus()
			kind, ns, name = "Pod", pod.Namespace, pod.Name
		}
		if kind == "" {
			return nil, false
		}
		m.state = stateDescribeView
		m.describe.Reset(kind, name, ns)
		m.describe.SetSize(m.ctx.Width, m.ctx.Height)
		return sections.LoadDescribe(m.ctx.Background, m.ctx.Manager, m.ctx.ActiveContext, ns, kind, name), true
	case key.Matches(msg, m.ctx.Keys.Reveal):
		if ref.Kind != k8s.KindSecret {
			return nil, false
		}
		m.state = stateDescribeView
		// MarkRevealed flips the title verb to "decoded" and renders a
		// yellow banner above the body — both cues together make it hard
		// to mistake a reveal for a default describe.
		m.describe.Reset("Secret", ref.Name, ref.Namespace)
		m.describe.MarkRevealed()
		m.describe.SetSize(m.ctx.Width, m.ctx.Height)
		return sections.LoadSecretReveal(m.ctx.Background, m.ctx.Manager, m.ctx.ActiveContext, ref.Namespace, ref.Name), true
	case key.Matches(msg, m.ctx.Keys.Edit):
		kind := editKindFor(ref)
		if kind == "" {
			return nil, false
		}
		return sections.LoadEditYAML(m.ctx.Background, m.ctx.Manager, m.ctx.ActiveContext, ref.Namespace, kind, ref.Name), true
	}
	return nil, false
}

// editKindFor maps a tree NodeRef onto the kind string the k8s.Edit primitives
// accept, or "" if the row isn't editable. This is the single gate that
// keeps `E` a no-op on rows where edits would either be rejected by the API
// (Pod, Node, PV) or are conceptually wrong (folds, contexts).
func editKindFor(ref sections.NodeRef) string {
	switch ref.Kind {
	case k8s.KindApp:
		// CronJob/Job stay out — Jobs are effectively immutable once created
		// and CronJob edits aren't covered by tests yet.
		switch ref.SubKind {
		case k8s.AppKindDeployment:
			return k8s.EditKindDeployment
		case k8s.AppKindStatefulSet:
			return k8s.EditKindStatefulSet
		case k8s.AppKindDaemonSet:
			return k8s.EditKindDaemonSet
		}
	case k8s.KindConfigMap:
		return k8s.EditKindConfigMap
	case k8s.KindSecret:
		return k8s.EditKindSecret
	case k8s.KindService:
		return k8s.EditKindService
	case k8s.KindIngress:
		return k8s.EditKindIngress
	case k8s.KindPVC:
		return k8s.EditKindPVC
	}
	return ""
}

// startEditor writes the loaded YAML to a 0600 tempfile, stashes the session
// on the model, and returns a tea.Cmd that suspends the program and runs
// $EDITOR. tea.ExecProcess takes care of releasing/restoring the terminal,
// so this is the cleanest visual handoff we can give the user.
func (m *Model) startEditor(msg sections.EditYAMLLoadedMsg) tea.Cmd {
	f, err := os.CreateTemp("", fmt.Sprintf("safina-edit-%s-*.yaml", msg.Name))
	if err != nil {
		return m.raiseError(fmt.Sprintf("edit: %v", err))
	}
	if _, err := f.WriteString(msg.YAML); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return m.raiseError(fmt.Sprintf("edit: %v", err))
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(f.Name())
		return m.raiseError(fmt.Sprintf("edit: %v", err))
	}
	// 0600: tempfile may carry decoded Secret values until apply/cancel.
	_ = os.Chmod(f.Name(), 0o600)

	m.edit = &editSession{
		Kind:      msg.Kind,
		Namespace: msg.Namespace,
		Name:      msg.Name,
		TempPath:  f.Name(),
		Orig:      []byte(msg.YAML),
	}
	editor, args := resolveEditor()
	args = append(args, f.Name())
	c := exec.CommandContext(m.ctx.Background, editor, args...) // #nosec G204 -- Executes the operator-selected editor with the temporary manifest as an argument.
	return tea.ExecProcess(c, func(err error) tea.Msg {
		return sections.EditorExitedMsg{Err: err}
	})
}

// resolveEditor mirrors kubectl's editor discovery: KUBE_EDITOR > VISUAL >
// EDITOR > vi. Splitting on whitespace lets users encode flag arguments in
// the env var (e.g. `EDITOR="code --wait"`).
func resolveEditor() (string, []string) {
	for _, env := range []string{"KUBE_EDITOR", "VISUAL", "EDITOR"} {
		if v := os.Getenv(env); v != "" {
			fields := splitFields(v)
			if len(fields) > 0 {
				return fields[0], fields[1:]
			}
		}
	}
	return "vi", nil
}

// splitFields is a tiny whitespace splitter, kept inline so we don't pull in
// strings.Fields just for one call site.
func splitFields(s string) []string {
	var out []string
	start := -1
	for i, r := range s {
		switch r {
		case ' ', '\t':
			if start >= 0 {
				out = append(out, s[start:i])
				start = -1
			}
		default:
			if start < 0 {
				start = i
			}
		}
	}
	if start >= 0 {
		out = append(out, s[start:])
	}
	return out
}

// handleEditorExit is the post-editor branch point: read the tempfile back,
// decide if the user actually changed anything, and either banner (no-op,
// parse error) or transition to the edit-confirm modal. Tempfile cleanup
// belongs to the *next* step (apply or cancel) — we keep it around until
// the user makes a decision so an accidental "n" doesn't lose their work.
func (m *Model) handleEditorExit(editorErr error) tea.Cmd {
	ed := m.edit
	if ed == nil {
		return nil
	}
	if editorErr != nil {
		m.cleanupEdit()
		return m.raiseError(fmt.Sprintf("editor: %v", editorErr))
	}
	edited, err := os.ReadFile(ed.TempPath)
	if err != nil {
		m.cleanupEdit()
		return m.raiseError(fmt.Sprintf("edit: %v", err))
	}
	if bytes.Equal(edited, ed.Orig) {
		m.cleanupEdit()
		return m.raiseStatus("edit: no changes")
	}
	ed.Edited = edited
	m.editViewport.GotoTop()
	m.state = stateEditConfirm
	return nil
}

// cleanupEdit tears down the in-flight edit session — unlinks the tempfile
// and clears the model pointer. Safe to call multiple times.
func (m *Model) cleanupEdit() {
	if m.edit == nil {
		return
	}
	if m.edit.TempPath != "" {
		_ = os.Remove(m.edit.TempPath)
	}
	m.edit = nil
}

// actionTarget maps a NodeRef onto the (kind, namespace, name) triple that
// the events / describe / scale handlers expect. Returns ("", "", "") when
// the ref doesn't represent a real k8s object.
// actionTarget translates a tree NodeRef into the (kind, namespace, name)
// triple that DescribeYAML and LoadEvents expect. Container is handled
// out-of-band by the Describe/Events branches below — it has no API
// object of its own, so we describe its parent Pod instead.
//
// The kind strings returned are k8s API Kind names (e.g.
// "PersistentVolumeClaim", not the friendlier "PVC" we use in the tree).
// That matches what kubectl prints and what events.involvedObject.kind
// expects on the field selector.
// deletableKind reports whether K (kill) is meaningful on this row. We
// allow the same set of kinds DeleteResource knows how to dispatch on, with
// two intentional omissions: Node (use cordon/drain instead — deleting a
// node from the API doesn't terminate the underlying machine) and Namespace
// (a foot-gun in a prod-only tool; users who need this still have kubectl).
func deletableKind(ref sections.NodeRef) bool {
	switch ref.Kind {
	case k8s.KindPod, k8s.KindConfigMap, k8s.KindSecret,
		k8s.KindService, k8s.KindIngress, k8s.KindPVC:
		return true
	case k8s.KindApp:
		switch ref.SubKind {
		case k8s.AppKindDeployment, k8s.AppKindStatefulSet, k8s.AppKindDaemonSet,
			k8s.AppKindJob, k8s.AppKindCronJob:
			return true
		}
	}
	return false
}

// deleteTarget mirrors actionTarget but skips kinds we refuse to delete
// (Node, Namespace). Returns ("", "", "") when the focus row isn't a
// deletable resource so the confirm handler can no-op safely.
func deleteTarget(ref sections.NodeRef) (kind, namespace, name string) {
	if !deletableKind(ref) {
		return "", "", ""
	}
	return actionTarget(ref)
}

func actionTarget(ref sections.NodeRef) (kind, namespace, name string) {
	switch ref.Kind {
	case k8s.KindApp:
		return ref.SubKind, ref.Namespace, ref.Name
	case k8s.KindPod:
		return "Pod", ref.Namespace, ref.Name
	case k8s.KindNode:
		return "Node", "", ref.Name
	case k8s.KindNamespace, k8s.KindNamespaceFolder:
		return "Namespace", "", ref.Name
	case k8s.KindConfigMap:
		return "ConfigMap", ref.Namespace, ref.Name
	case k8s.KindSecret:
		return "Secret", ref.Namespace, ref.Name
	case k8s.KindService:
		return "Service", ref.Namespace, ref.Name
	case k8s.KindIngress:
		return "Ingress", ref.Namespace, ref.Name
	case k8s.KindPVC:
		return "PersistentVolumeClaim", ref.Namespace, ref.Name
	case k8s.KindPV:
		return "PersistentVolume", "", ref.Name
	}
	return "", "", ""
}

func (m *Model) handleScaleInput(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		val := m.scaleInput.Value()
		replicas, err := strconv.ParseInt(val, 10, 32)
		if err != nil || replicas < 0 {
			m.scaleError = "Enter a whole number from 0 to 2147483647."
			return m, nil
		}
		ref := m.tree.SelectedRef()
		if ref.Kind != k8s.KindApp {
			return m, nil
		}
		m.state = stateMain
		m.scaleInput.Blur()
		return m, sections.ScaleWorkload(m.ctx.Background, m.ctx.Manager, m.ctx.ActiveContext, ref.Namespace, ref.SubKind, ref.Name, int32(replicas))
	case "esc":
		m.state = stateMain
		m.scaleInput.Blur()
		return m, nil
	}
	s := msg.String()
	if len(s) == 1 && (s < "0" || s > "9") && s != "backspace" {
		return m, nil
	}
	var cmd tea.Cmd
	m.scaleError = ""
	m.scaleInput, cmd = m.scaleInput.Update(msg)
	return m, cmd
}

// handlePortForwardInput accepts "L:R" or "R" and kicks off StartPortForward.
// Parse failures keep the modal open (banner explains why); a successful
// parse closes the modal immediately — the start is async and the success/
// failure banner arrives later via PortForwardStartedMsg.
func (m *Model) handlePortForwardInput(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		val := m.pfInput.Value()
		local, remote, err := k8s.ParsePortPair(val)
		if err != nil {
			m.pfError = "invalid port pair: " + err.Error()
			return m, nil
		}
		m.state = stateMain
		m.pfInput.Blur()
		return m, sections.StartPortForward(
			m.ctx.Background, m.ctx.Manager, m.ctx.ActiveContext,
			m.pfTarget.Namespace, m.pfTarget.Kind, m.pfTarget.Name,
			local, remote,
		)
	case "esc":
		m.state = stateMain
		m.pfInput.Blur()
		return m, nil
	}
	// Allow digits, colon, and backspace; anything else (letters, punctuation)
	// is silently dropped so a stray keypress doesn't garble the pair.
	s := msg.String()
	if len(s) == 1 && (s < "0" || s > "9") && s != ":" && s != "backspace" {
		return m, nil
	}
	var cmd tea.Cmd
	m.pfError = ""
	m.pfInput, cmd = m.pfInput.Update(msg)
	return m, cmd
}

// handleDrainProgress consumes keys while the drain-progress modal is open.
// Only ctrl+c does anything (cancels) — esc is intentionally a no-op so the
// user can't half-drain a node by accident. The cancel handler relies on
// DrainNode's deferred uncordon, so a cancelled drain leaves the node
// uncordoned (the recoverable state) rather than cordoned-with-half-evicted.
func (m *Model) handleDrainProgress(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+c" {
		m.drain.Cancel()
		return m, nil
	}
	return m, nil
}

// handleForwardsList routes keys while the active-forwards modal is open.
// `K` (kill) removes the cursor row; ↑/↓ moves the cursor; esc/enter closes.
func (m *Model) handleForwardsList(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.ctx.Keys.Back), msg.String() == "enter":
		m.state = stateMain
		return m, nil
	case key.Matches(msg, m.ctx.Keys.Up):
		m.forwards.MoveCursor(-1)
		return m, nil
	case key.Matches(msg, m.ctx.Keys.Down):
		m.forwards.MoveCursor(1)
		return m, nil
	case key.Matches(msg, m.ctx.Keys.Delete):
		id := m.forwards.CursorRowID()
		if id == "" {
			return m, nil
		}
		return m, sections.StopPortForward(m.ctx.Manager, id)
	}
	return m, nil
}

func (m *Model) handleConfirm(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "y", "Y":
		ref := m.tree.SelectedRef()
		// This feature applies only to the states listed; all others use the fallback.
		//exhaustive:ignore
		switch m.state {
		case stateDeleteConfirm:
			kind, ns, name := deleteTarget(ref)
			if kind != "" {
				m.state = stateMain
				return sections.DeleteResource(m.ctx.Background, m.ctx.Manager, m.ctx.ActiveContext, kind, ns, name)
			}
		case stateRollbackConfirm:
			if ref.Kind == k8s.KindApp {
				m.state = stateMain
				return sections.RollbackDeployment(m.ctx.Background, m.ctx.Manager, m.ctx.ActiveContext, ref.Namespace, ref.Name)
			}
		case stateRestartConfirm:
			if ref.Kind == k8s.KindApp {
				m.state = stateMain
				return sections.RestartApp(m.ctx.Background, m.ctx.Manager, m.ctx.ActiveContext, ref.Namespace, ref.SubKind, ref.Name)
			}
		case stateRestartPodConfirm:
			if ref.Kind == k8s.KindPod {
				m.state = stateMain
				return sections.RestartPod(m.ctx.Background, m.ctx.Manager, m.ctx.ActiveContext, ref.Namespace, ref.Name)
			}
		case stateEditConfirm:
			if m.edit == nil {
				m.state = stateMain
				return nil
			}
			ed := m.edit
			m.edit = nil
			m.state = stateMain
			cmd := sections.ApplyEdit(m.ctx.Background, m.ctx.Manager, m.ctx.ActiveContext, ed.Namespace, ed.Kind, ed.Name, ed.Edited)
			// Tempfile is no longer needed; the bytes are in flight.
			if ed.TempPath != "" {
				_ = os.Remove(ed.TempPath)
			}
			return cmd
		case stateCordonConfirm:
			if m.nodeTarget.Name == "" {
				m.state = stateMain
				return nil
			}
			// Schedulable=true means "currently accepts pods, so user
			// pressed C to cordon" — and vice versa. Pass the *desired*
			// next state to the helper.
			desired := !m.nodeTarget.Schedulable // flip
			m.state = stateMain
			return sections.CordonNode(m.ctx.Background, m.ctx.Manager, m.ctx.ActiveContext, m.nodeTarget.Name, desired)
		case stateDrainConfirm:
			if m.nodeTarget.Name == "" {
				m.state = stateMain
				return nil
			}
			node := m.nodeTarget.Name
			m.drain.Reset(node, func() {}) // real cancel arrives via DrainStartedMsg
			m.state = stateDrainProgress
			return sections.StartDrain(m.ctx.Background, m.ctx.Manager, m.ctx.ActiveContext, node)
		default:
			// Non-confirm states fall through to the post-switch reset.
		}
		m.state = stateMain
		return nil
	case "n", "N", "esc":
		if m.state == stateEditConfirm {
			m.cleanupEdit()
		}
		m.state = stateMain
		return nil
	}
	if m.state == stateEditConfirm {
		var cmd tea.Cmd
		m.editViewport, cmd = m.editViewport.Update(msg)
		return cmd
	}
	return nil
}

type logEventMsg struct {
	channel <-chan k8s.PodLogEvent
	event   k8s.PodLogEvent
}

func (m *Model) nextLogLine() tea.Cmd {
	if m.logCh == nil {
		return nil
	}
	ch := m.logCh
	return func() tea.Msg {
		event, ok := <-ch
		if !ok {
			return logEventMsg{channel: ch, event: k8s.PodLogEvent{Done: true}}
		}
		return logEventMsg{channel: ch, event: event}
	}
}

// shellDrainCap is the upper bound on bytes coalesced into one
// ShellOutputMsg. Bigger batches cut renders-per-second under busy output
// (think `cat largefile`), but a too-large cap delays the first redraw and
// makes the UI feel stale. 64KB matches the typical SPDY/WebSocket frame
// size — one redraw per frame is plenty.
const shellDrainCap = 64 * 1024

// nextShellEvent reads from the session's event channel, greedy-draining any
// Bytes events that are immediately available so multiple chunks coalesce
// into one ShellOutputMsg. Without this, every chunk pays a full
// goroutine→Update→render round-trip — a busy pod easily produces hundreds
// of small chunks per second and chokes the event loop.
//
// If a Done event is encountered during the drain we return a ShellExitMsg
// with the buffered tail attached, so the pod's last output isn't lost.
func (m *Model) nextShellEvent() tea.Cmd {
	sess := m.shellSession
	if sess == nil {
		return nil
	}
	return func() tea.Msg {
		ev, ok := <-sess.Events
		if !ok {
			return sections.ShellExitMsg{}
		}
		if ev.Done {
			return sections.ShellExitMsg{Err: ev.Err, NoShell: ev.NoShell}
		}
		buf := ev.Bytes
		for len(buf) < shellDrainCap {
			select {
			case more, ok := <-sess.Events:
				if !ok {
					return sections.ShellExitMsg{Bytes: buf}
				}
				if more.Done {
					return sections.ShellExitMsg{Bytes: buf, Err: more.Err, NoShell: more.NoShell}
				}
				buf = append(buf, more.Bytes...)
			default:
				return sections.ShellOutputMsg{Bytes: buf}
			}
		}
		return sections.ShellOutputMsg{Bytes: buf}
	}
}

func (m *Model) autoRefreshTick() tea.Cmd {
	return tea.Tick(refreshInterval*time.Second, func(time.Time) tea.Msg { return refreshMsg{} })
}

// actionSuccessMsg renders a friendly one-liner for the post-action banner.
// We keep the verb identical to the keymap label (Jakob's Law: same word in
// the footer help, the confirm modal title, and the success banner) and
// fall back to a generic "done" when the action name is empty.
func actionSuccessMsg(action, subject string) string {
	if action == "" {
		action = "done"
	}
	if subject == "" {
		return action
	}
	return action + " · " + subject
}

// tryYank intercepts the Yank key (`y`) before per-state dispatch in any
// state where copying a payload makes sense. Returns a batched cmd that
// emits the OSC52 clipboard sequence and flashes a "copied" banner; the
// boolean tells the caller to short-circuit the rest of the handler.
//
// Skipped in:
//   - stateShellView (forward to pod's tty),
//   - stateScaleInput / stateHelp / confirm states (the keystream is
//     consumed by their tiny modal grammars),
//   - stateMain when chat is focused (textarea owns the keystream).
//
// In every other state, if the section's filter is active the keystroke
// falls through as a literal — yank should not fight a `/y` query.
func (m *Model) tryYank(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	if !key.Matches(msg, m.ctx.Keys.Yank) {
		return nil, false
	}

	var payload string
	// This feature applies only to the states listed; all others use the fallback.
	//exhaustive:ignore
	switch m.state {
	case stateLogView:
		if m.logs.FilterActive() {
			return nil, false
		}
		payload = m.logs.YankPayload()
	case stateEventsView:
		if m.events.FilterActive() {
			return nil, false
		}
		payload = m.events.YankPayload()
	case stateDescribeView:
		if m.describe.FilterActive() {
			return nil, false
		}
		payload = m.describe.YankPayload()
	case stateMain:
		if m.chat.Focused {
			// In the chat textarea `y` is a literal character — paste-target
			// belongs to the textarea, not the clipboard helper.
			return nil, false
		}
		if m.tree.FilterActive() {
			return nil, false
		}
		payload = m.tree.YankPayload()
		// At main with the tree focused but nothing selectable to paste,
		// fall back to the latest assistant turn so `y` still does
		// something useful when the chat just streamed an answer.
		if payload == "" {
			payload = m.chat.YankPayload()
		}
	default:
		return nil, false
	}

	if payload == "" {
		return nil, false
	}
	return tea.Batch(copyToClipboard(payload), m.raiseStatus("copied")), true
}

// copyToClipboard prefers a system clipboard tool over OSC52. Some
// terminals (ghostty, kitty with permissive=ask) prompt the user every
// time a program writes OSC52 — a "copy" notification with a confirm
// button shows up over the alt-screen. xclip / wl-copy / pbcopy bypass
// that prompt because the write happens via the OS clipboard API instead
// of an escape sequence the terminal interprets.
//
// We probe in the order: macOS native first, Wayland next, then X11.
// OSC52 stays as the fallback — over SSH or in a stripped container
// where none of these tools are installed, the operator still gets a
// copy (modulo the terminal's prompt). This way the common-case dev
// loop is friction-free and the edge case still works.
func copyToClipboard(payload string) tea.Cmd {
	candidates := [][]string{
		{"pbcopy"},                           // macOS
		{"wl-copy"},                          // Wayland
		{"xclip", "-selection", "clipboard"}, // X11
		{"xsel", "--clipboard", "--input"},   // X11 alt
	}
	for _, args := range candidates {
		path, err := exec.LookPath(args[0])
		if err != nil {
			continue
		}
		bin, rest := path, args[1:]
		body := payload
		return func() tea.Msg {
			// 2s ceiling: pbcopy / wl-copy / xclip should return in
			// milliseconds — if one hangs (rare, but x11 selection servers
			// can wedge) we drop the copy instead of stalling the UI.
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, bin, rest...) // #nosec G204 -- Executes a detected clipboard helper with fixed arguments and payload on stdin.
			cmd.Stdin = strings.NewReader(body)
			_ = cmd.Run()
			return nil
		}
	}
	// No system clipboard tool — fall back to the OSC52 sequence. The
	// terminal may prompt the user; that's better than silently dropping
	// the copy in an SSH session where there is no other path.
	return tea.SetClipboard(payload)
}

// childCount returns the visible row count for a Children payload. Used only
// by the debug log to give an at-a-glance "how big is this list" signal.
func childCount(c *sections.Children) int {
	if c == nil {
		return 0
	}
	switch c.Kind {
	case k8s.KindContextList:
		return len(c.Contexts)
	case k8s.KindApp:
		return len(c.Apps)
	case k8s.KindPod:
		return len(c.Pods)
	case k8s.KindContainer:
		return len(c.Containers)
	case k8s.KindNodePool:
		return len(c.Pools)
	case k8s.KindNode:
		return len(c.Nodes)
	case k8s.KindNamespace:
		return len(c.Namespaces)
	case k8s.KindNamespaceFolder:
		return len(c.UserNS)
	}
	return 0
}
