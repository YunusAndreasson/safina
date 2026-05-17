package tui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"

	"github.com/yunus/safina/internal/config"
	"github.com/yunus/safina/internal/debug"
	"github.com/yunus/safina/internal/k8s"
	"github.com/yunus/safina/internal/tui/sections"
)

// isCtxDone reports whether err is the kind of cancellation we expect during
// normal flow — switching contexts or rapid drill+pop races against in-flight
// loaders. We suppress those cleanly so they don't surface as an error banner.
func isCtxDone(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.ctx.Width = msg.Width
		m.ctx.Height = msg.Height
		m.help.SetWidth(msg.Width)
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
		// The tree pushed or popped a level. If we landed on a context (the
		// user picked / switched a cluster, or auto-drilled on startup), wire
		// it up: set ActiveContext, drop stale caches, re-stamp the window
		// title, and persist. The load fires from the cache-miss check below.
		focus := msg.Path[len(msg.Path)-1]
		debug.Log("tree.focus",
			"kind", string(focus.Kind),
			"name", focus.Name,
			"namespace", focus.Namespace,
			"depth", len(msg.Path))
		if focus.Kind == k8s.KindContext && m.ctx.ActiveContext != focus.Name {
			m.ctx.ActiveContext = focus.Name
			m.ctx.ResetBackground()
			m.tree.ResetForContextSwitch()
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
			// Popped back to the picker — there's no "data" to be stale,
			// so hide the refresh indicator until the user picks again.
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
			return m, tea.Batch(m.loadTreeFocus(), actCmd, discoCmd)
		}
		return m, tea.Batch(actCmd, discoCmd)

	case sections.TreeChildrenLoadedMsg:
		m.tree.Loading = false
		m.tree.Refreshing = false
		focus := msg.Path[len(msg.Path)-1]
		if msg.Err != nil {
			if isCtxDone(msg.Err) {
				debug.Log("tree.load_canceled", "kind", string(focus.Kind), "name", focus.Name)
				return m, nil
			}
			debug.Errf("tree.load_failed", "kind", string(focus.Kind), "name", focus.Name, "err", msg.Err.Error())
			// Record on the tree so the body can show a retry hint inline
			// (P4.5: distinguish "load failed" from "API returned empty"),
			// AND raise the global banner so an error during a background
			// auto-refresh — when stale rows are still on screen — isn't
			// invisible.
			m.tree.LoadErr = msg.Err
			return m, m.raiseError(msg.Err.Error())
		}
		m.tree.SetChildren(msg.Path, msg.Children)
		m.lastRefresh = time.Now()
		actCmd := m.syncSelection()
		debug.Log("tree.loaded",
			"kind", string(focus.Kind),
			"name", focus.Name,
			"children", childCount(msg.Children))
		return m, actCmd

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
		m.state = stateLogView
		m.logs.Reset(msg.Name, msg.Namespace)
		m.logs.SetSize(m.ctx.Width, m.ctx.Height)
		return m, sections.StreamLogs(m.ctx.Background, m.ctx.Manager, m.ctx.ActiveContext, msg.Namespace, msg.Name, msg.Container)

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
		statusCmd := m.raiseStatus(actionSuccessMsg(msg.Action, msg.Subject))
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
		return m, tea.Batch(statusCmd, m.loadTreeFocus())

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
		m.logCh = msg.Ch
		return m, m.nextLogLine()

	case sections.ShellSessionStartedMsg:
		// Abort race: the user can press esc during phaseConnect (which
		// returns leave=true and resets the modal) before the dial goroutine
		// has emitted this message. If we're no longer in the shell view,
		// the session is orphaned — close it so we don't leak the stream.
		if m.state != stateShellView {
			msg.Session.Close()
			return m, nil
		}
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

	case k8s.PodLogEvent:
		if msg.Done {
			if msg.Err != nil && !isCtxDone(msg.Err) {
				return m, m.raiseError(fmt.Sprintf("log: %v", msg.Err))
			}
			return m, nil
		}
		if msg.Err != nil {
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
			m.tree.Refreshing = true
			cmd = m.loadTreeFocus()
			debug.Log("refresh.tick", "context", m.ctx.ActiveContext, "focus", string(m.tree.Focus().Kind))
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
		if msg.epoch == m.bannerEpoch {
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
		return m, sections.NewChatStream(m.ctx.Background, m.claudeSvc, full)

	case sections.ChatStreamStartedMsg:
		m.chatStreamCh = msg.Ch
		return m, sections.NextChatEvent(msg.Ch)

	case sections.ChatTokenMsg:
		m.chat.IngestToken(msg.Delta)
		if m.chatStreamCh != nil {
			return m, sections.NextChatEvent(m.chatStreamCh)
		}
		return m, nil

	case sections.ChatRetryMsg:
		m.chat.IngestRetry(msg.Attempt, msg.Of, msg.DelayMS, msg.Reason)
		if m.chatStreamCh != nil {
			return m, sections.NextChatEvent(m.chatStreamCh)
		}
		return m, nil

	case sections.ChatTurnDoneMsg:
		m.chat.FinishTurn(msg.Err)
		m.chatStreamCh = nil
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.ctx.Spinner, cmd = m.ctx.Spinner.Update(msg)
		return m, cmd

	case tea.PasteMsg:
		// Bracketed paste only matters where there's a text input. Today
		// that's the chat textarea (the Chat workspace tab); the scale
		// modal's input is short enough that v1 fragmented-keystroke
		// behaviour is fine. Other modals don't accept paste.
		if m.state == stateChatView {
			cmd, _, _ := m.chat.Update(msg, m.ctx)
			return m, cmd
		}
		return m, nil

	case tea.MouseClickMsg:
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
		// Mouse wheel is the natural escape-hatch for "I'm reading and want
		// to scroll without finding the right keybinding." The scrollable
		// workspace tabs (logs / events / yaml / chat) each wrap a bubbles
		// viewport that handles MouseWheelMsg internally — we just forward.
		// Tree pane doesn't use a viewport, so wheel there is a no-op; that
		// matches the v1 behaviour and avoids a confusing partial scroll.
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
		}
		return m, nil

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
		return tea.KeyPressMsg{Text: "H", Code: 'H'}, true
	case sections.WSTabForwards:
		// Ctrl+F: Text is empty for non-printable chords; the matcher reads
		// Code + Mod.
		return tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl}, true
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
	next := idx
	if forward {
		next = idx + 1
	} else {
		next = idx - 1
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
	// returnToMain cleans up the prior immersive tab (drains log channel,
	// blurs chat, etc.) before we enter the next one. Inspector is the
	// landing state; for other targets we then re-dispatch the activation
	// hotkey through handleKey to run the right load path.
	m.returnToMain()
	if target == sections.WSTabInspector {
		// Preserve peek so the workspace keeps the gradient border and the
		// next ←/→ moves to the next tab instead of jumping back to tree.
		m.peekWorkspace = true
		m.tree.Focused = false
		return m, nil
	}
	synth, ok := tabActivationKey(target)
	if !ok {
		return m, nil
	}
	// Preserve peek across the activation so the operator can keep cycling
	// without re-pressing Tab between each ←/→.
	m.peekWorkspace = true
	m.tree.Focused = false
	return m.handleKey(synth)
}

// returnToMain closes whatever immersive workspace tab is active and
// returns focus to the tree. Same cleanup the individual section-Esc
// handlers do, lifted here so the global `1` tab-switch can call it
// without duplicating per-state teardown.
func (m *Model) returnToMain() {
	switch m.state {
	case stateLogView:
		m.ctx.ResetBackground()
		if ch := m.logCh; ch != nil {
			go func() {
				for range ch {
				}
			}()
		}
		m.logCh = nil
	case stateChatView:
		m.chat.SetFocus(false)
		m.tree.Focused = true
	}
	m.state = stateMain
}

func (m *Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	// Yank (`y`) is intercepted before the per-state dispatch in every state
	// where it makes sense — the section exposes its payload via YankPayload
	// and a FilterActive guard so the user can still type a literal `y` into
	// the filter prompt. Shell forwards `y` to the pod, so it's excluded.
	if cmd, yanked := m.tryYank(msg); yanked {
		return m, cmd
	}

	// Tab is the global pane-focus toggle. It works from any state where
	// a filter isn't actively eating keystrokes — including immersive tabs
	// like Logs/Events/Yaml, so the operator can always shift focus back
	// to the tree without leaving the tab they're reading. The active
	// view is preserved; only the gradient border + arrow target moves.
	if msg.String() == "tab" && !m.sectionHasFilterFocus() {
		m.peekWorkspace = !m.peekWorkspace
		m.tree.Focused = !m.peekWorkspace
		return m, nil
	}

	// Workspace tab quick-switch: when the workspace owns focus (any
	// immersive tab is active, or the operator pressed Tab to peek),
	// `1` jumps to Inspector and ←/→ cycle through the visible tabs.
	// The Yaml tab is reached by Esc-then-y from immersive tabs since
	// `y` is yank inside those sections; the cluster picker's `i` (Chat)
	// is also reachable only from stateMain since most immersive sections
	// consume `i` as a literal character in their filters / inputs.
	if m.workspaceOwnsFocus() && !m.sectionHasFilterFocus() {
		switch msg.String() {
		case "1":
			m.returnToMain()
			return m, nil
		case "left", "right":
			return m.cycleWorkspaceTab(msg.String() == "right")
		}
	}

	switch m.state {
	case stateLogView:
		cmd, leave := m.logs.Update(msg, m.ctx)
		if leave {
			m.ctx.ResetBackground()
			// Drain the streamer's channel until it closes so its goroutine
			// can return — its loop blocks on `ch <- PodLogEvent{...}` and
			// would otherwise leak after we set m.logCh = nil.
			if ch := m.logCh; ch != nil {
				go func() {
					for range ch {
					}
				}()
			}
			m.logCh = nil
			m.state = stateMain
			return m, nil
		}
		return m, cmd

	case stateEventsView:
		cmd, leave := m.events.Update(msg, m.ctx)
		if leave {
			m.state = stateMain
			return m, nil
		}
		return m, cmd

	case stateDescribeView:
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
		return m.handleForwardsList(msg)

	case stateDeleteConfirm, stateRollbackConfirm, stateRestartConfirm, stateRestartPodConfirm,
		stateEditConfirm, stateCordonConfirm, stateDrainConfirm:
		return m, m.handleConfirm(msg)

	case stateDrainProgress:
		return m.handleDrainProgress(msg)

	case stateHelp:
		if key.Matches(msg, m.ctx.Keys.Back) || key.Matches(msg, m.ctx.Keys.Help) || msg.String() == "q" {
			m.state = stateMain
		}
		return m, nil

	case stateHotView:
		switch {
		case key.Matches(msg, m.ctx.Keys.Back), msg.String() == "q":
			m.state = stateMain
			return m, nil
		case key.Matches(msg, m.ctx.Keys.Refresh):
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
	}

	// Chat tab keystroke routing. When the Chat tab is the active workspace
	// surface, the textarea consumes keystrokes; pressing Esc / Tab returns
	// to the Inspector tab (closes the chat surface) — same affordance as
	// closing any other immersive tab.
	if m.state == stateChatView {
		if msg.String() == "ctrl+c" {
			m.saveConfig()
			m.ctx.Cancel()
			return m, tea.Quit
		}
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
			return m, tea.Batch(cmd, sections.NewChatStream(m.ctx.Background, m.claudeSvc, full))
		}
		return m, cmd
	}

	// Main state — global keys first, then tree. Chat drawer focus is
	// handled above (the drawer captures keystrokes when open + focused);
	// here the operator is interacting with the tree / workspace.

	switch {
	case key.Matches(msg, m.ctx.Keys.Quit):
		m.saveConfig()
		m.ctx.Cancel()
		return m, tea.Quit

	case msg.String() == ":" && !m.tree.FilterActive():
		// `:` Vim-style command palette. Indexed lazily off the tree's
		// level cache so it gets richer as the operator drills around.
		// FilterActive check keeps `:` literal inside the / filter input.
		m.palette.Open(sections.BuildPaletteIndex(m.tree.Cache()))
		m.state = statePalette
		return m, nil

	case key.Matches(msg, m.ctx.Keys.Help):
		m.state = stateHelp
		return m, nil

	case key.Matches(msg, m.ctx.Keys.Forwards):
		// Forwards list is process-global — accessible from main state
		// regardless of which row the tree cursor is on. Drops you into
		// the modal even when there are zero forwards (empty-state copy
		// teaches the verb).
		m.forwards.Refresh(m.ctx.Manager.Forwards.List())
		m.state = stateForwardsList
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
		if m.ctx.ActiveContext != "" && m.tree.Focus().Kind != k8s.KindContextList {
			m.tree.InvalidateFocus()
			m.tree.Loading = true
			m.tree.Refreshing = true
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
		return m, nil

	case key.Matches(msg, m.ctx.Keys.ClusterSwitch) && !m.tree.FilterActive():
		// Ctrl+K cluster-switcher overlay. Pre-populate with the list we
		// cached at startup; the overlay positions its cursor on the active
		// context so "you are here" is the default.
		m.clusters.Open(m.allContexts, m.ctx.ActiveContext)
		m.state = stateClusters
		return m, nil
	}

	// Resource-action keys (kind-aware dispatch via the tree).
	if cmd, handled := m.handleTreeAction(msg); handled {
		return m, cmd
	}

	// Fall through to the tree itself for navigation / drill / filter.
	cmd := m.tree.Update(msg, m.ctx)
	// Tree state may have moved — refresh chat's context strip + chips,
	// and the right-stack tiles (Inspector / Activity).
	actCmd := m.syncSelection()
	return m, tea.Batch(cmd, actCmd)
}

// handlePalette dispatches keys while the `:` command palette is open.
// The palette has three keypress categories:
//
//  1. Navigation — ↑/↓ moves the result cursor; ↵ submits the highlighted
//     entry (drill); esc cancels.
//  2. Verb dispatch — the user types a verb letter (no Tab needed in this
//     simplified flow) and we route directly to the corresponding action
//     for the highlighted entry. This is the smart shortcut — operator
//     fuzzy-matches a Pod, types `l`, sees logs.
//  3. Query editing — anything else mutates the search query.
//
// Verbs are chosen for unambiguous 1-letter mapping per kind. We resolve
// against the entry's PaletteVerbsFor list so a wrong-kind keypress is a
// no-op rather than a surprise (e.g. `s` on a Secret does nothing — Secret
// has no scale verb).
func (m *Model) handlePalette(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.ctx.Keys.Back):
		m.state = stateMain
		return m, nil
	case key.Matches(msg, m.ctx.Keys.Up):
		m.palette.CursorUp()
		return m, nil
	case key.Matches(msg, m.ctx.Keys.Down):
		m.palette.CursorDown()
		return m, nil
	case key.Matches(msg, m.ctx.Keys.Enter):
		if entry, ok := m.palette.Selected(); ok {
			m.state = stateMain
			return m.drillToPaletteEntry(entry)
		}
		return m, nil
	}

	s := msg.String()
	// Verb shortcut: a single letter that maps to a verb on the current
	// entry's kind dispatches the action immediately. We deliberately
	// handle this BEFORE generic letter input — the trade-off is that
	// kind-specific verb letters (l/d/e/x/s/r/v/E/S) won't appear in the
	// query string when the highlighted match supports them. Operators
	// can always backspace and retype if their intent was textual.
	if entry, ok := m.palette.Selected(); ok && len(s) == 1 {
		for _, v := range sections.PaletteVerbsFor(entry) {
			if s == v.Letter {
				m.state = stateMain
				return m.dispatchPaletteVerb(s, entry)
			}
		}
	}

	// Query editing: backspace pops, single chars append.
	if s == "backspace" {
		q := m.palette.Query()
		if len(q) > 0 {
			m.palette.SetQuery(q[:len(q)-1])
		}
		return m, nil
	}
	if len(s) == 1 && s >= " " && s <= "~" {
		m.palette.SetQuery(m.palette.Query() + s)
	}
	return m, nil
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
	case key.Matches(msg, m.ctx.Keys.Up):
		m.clusters.CursorUp()
		return m, nil
	case key.Matches(msg, m.ctx.Keys.Down):
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
		if it.Name == m.ctx.ActiveContext {
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

	s := msg.String()
	if s == "backspace" {
		q := m.clusters.Query()
		if len(q) > 0 {
			m.clusters.SetQuery(q[:len(q)-1])
		}
		return m, nil
	}
	if len(s) == 1 && s >= " " && s <= "~" {
		m.clusters.SetQuery(m.clusters.Query() + s)
	}
	return m, nil
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
	case key.Matches(msg, m.ctx.Keys.Up):
		m.browser.CursorUp()
		return m, nil
	case key.Matches(msg, m.ctx.Keys.Down):
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
	if s == "backspace" {
		q := m.browser.Query()
		if len(q) > 0 {
			m.browser.SetQuery(q[:len(q)-1])
		}
		return m, nil
	}
	if len(s) == 1 && s >= " " && s <= "~" {
		m.browser.SetQuery(m.browser.Query() + s)
	}
	return m, nil
}

// drillToPaletteEntry navigates the tree to the entry's path and places
// the cursor on the matching row. Returns a tea.Cmd that triggers the
// child-load if the destination level isn't already cached.
func (m *Model) drillToPaletteEntry(e sections.PaletteEntry) (tea.Model, tea.Cmd) {
	// The entry's Path includes the entry itself as the leaf — for a drill
	// we want the parent so the row is "highlighted" rather than entered.
	if len(e.Path) <= 1 {
		return m, m.tree.NavigateTo(e.Path)
	}
	parent := e.Path[:len(e.Path)-1]
	cmd := m.tree.NavigateTo(parent)
	m.tree.SetCursorByRef(e.Kind, e.Namespace, e.Name)
	actCmd := m.syncSelection()
	return m, tea.Batch(cmd, actCmd)
}

// dispatchPaletteVerb fires an action against the palette entry without
// going through the tree's navigation. This is the speed win: from the
// palette, the operator can jump straight into logs/describe/edit/etc.
// for any indexed resource — no drilling required. The verb letter is
// authoritative; we trust the caller has already validated it against
// PaletteVerbsFor.
func (m *Model) dispatchPaletteVerb(verb string, e sections.PaletteEntry) (tea.Model, tea.Cmd) {
	ns := e.Namespace
	name := e.Name
	switch verb {
	case "d":
		// Describe is universally available; route through the existing
		// modal so the operator gets the same YAML viewer they'd see
		// from the tree.
		m.state = stateDescribeView
		kind := e.Kind
		if e.SubKind != "" {
			kind = e.SubKind
		}
		m.describe.Reset(kind, name, ns)
		m.describe.SetSize(m.ctx.Width, m.ctx.Height)
		return m, sections.LoadDescribe(m.ctx.Background, m.ctx.Manager, m.ctx.ActiveContext, ns, kind, name)
	case "v":
		if e.Kind != k8s.KindSecret {
			return m, nil
		}
		m.state = stateDescribeView
		m.describe.Reset("Secret", name, ns)
		m.describe.MarkRevealed()
		m.describe.SetSize(m.ctx.Width, m.ctx.Height)
		return m, sections.LoadSecretReveal(m.ctx.Background, m.ctx.Manager, m.ctx.ActiveContext, ns, name)
	case "E":
		kind := editKindForPalette(e)
		if kind == "" {
			return m, nil
		}
		return m, sections.LoadEditYAML(m.ctx.Background, m.ctx.Manager, m.ctx.ActiveContext, ns, kind, name)
	case "l":
		if e.Kind != k8s.KindPod && e.Kind != k8s.KindApp {
			return m, nil
		}
		// For the App case we'd usually drill into the deployment first
		// to pick which pod; from the palette the simpler intent is
		// "logs from any pod of this app" — but to keep behavior
		// predictable with the tree path, route through drill-then-logs:
		// drill, then the operator can press `l` again. Skip for now.
		return m.drillToPaletteEntry(e)
	case "e":
		// Events: same fast-path as describe — we know the resource id.
		return m.drillToPaletteEntry(e)
	default:
		// Any other verb (x/s/r/S) needs the action-key dispatch which
		// reads from m.tree.SelectedRef(). Drill there first; the user
		// can press the key again from the destination.
		return m.drillToPaletteEntry(e)
	}
}

// editKindForPalette mirrors editKindFor but accepts a palette entry —
// the palette's SubKind/Kind shape is already what we need; the tree
// version expects sections.NodeRef.
func editKindForPalette(e sections.PaletteEntry) string {
	switch e.Kind {
	case k8s.KindApp:
		switch e.SubKind {
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

// handleTreeAction routes resource-action keys (s/r/x/l/e/d/Ctrl-D/S) based
// on the kind of the currently highlighted tree row. Returns (cmd, handled).
func (m *Model) handleTreeAction(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	ref := m.tree.SelectedRef()
	switch {
	case key.Matches(msg, m.ctx.Keys.Scale):
		if ref.Kind == k8s.KindApp && (ref.SubKind == k8s.AppKindDeployment || ref.SubKind == k8s.AppKindStatefulSet) {
			m.state = stateScaleInput
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
		case k8s.KindPod:
			m.state = stateLogView
			m.logs.Reset(ref.Name, ref.Namespace)
			m.logs.SetSize(m.ctx.Width, m.ctx.Height)
			return sections.StreamLogs(m.ctx.Background, m.ctx.Manager, m.ctx.ActiveContext, ref.Namespace, ref.Name, ""), true
		case k8s.KindContainer:
			pod := m.tree.Focus()
			m.state = stateLogView
			m.logs.Reset(pod.Name, pod.Namespace)
			m.logs.SetSize(m.ctx.Width, m.ctx.Height)
			return sections.StreamLogs(m.ctx.Background, m.ctx.Manager, m.ctx.ActiveContext, pod.Namespace, pod.Name, ref.Name), true
		case k8s.KindApp:
			return sections.StreamAppLogs(m.ctx.Background, m.ctx.Manager, m.ctx.ActiveContext, k8s.AppRef{
				Kind:      ref.SubKind,
				Namespace: ref.Namespace,
				Name:      ref.Name,
				UID:       ref.UID,
			}), true
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
	c := exec.CommandContext(m.ctx.Background, editor, args...)
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
		m.state = stateMain
		m.scaleInput.Blur()
		if val == "" {
			return m, nil
		}
		replicas, err := strconv.Atoi(val)
		if err != nil || replicas < 0 {
			return m, m.raiseError("invalid replica count")
		}
		ref := m.tree.SelectedRef()
		if ref.Kind != k8s.KindApp {
			return m, nil
		}
		return m, sections.ScaleDeployment(m.ctx.Background, m.ctx.Manager, m.ctx.ActiveContext, ref.Namespace, ref.Name, int32(replicas))
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
			return m, m.raiseError("invalid port pair: " + err.Error())
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
	return nil
}

func (m *Model) nextLogLine() tea.Cmd {
	if m.logCh == nil {
		return nil
	}
	ch := m.logCh
	return func() tea.Msg {
		event, ok := <-ch
		if !ok {
			return k8s.PodLogEvent{Done: true}
		}
		return event
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
			cmd := exec.CommandContext(ctx, bin, rest...)
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
