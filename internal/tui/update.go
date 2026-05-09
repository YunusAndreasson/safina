package tui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"

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
			m.lastRefresh = time.Time{} // reset staleness clock for the new cluster
			m.saveConfig()
		}
		if focus.Kind == k8s.KindContextList {
			// Popped back to the picker — there's no "data" to be stale,
			// so hide the refresh indicator until the user picks again.
			m.lastRefresh = time.Time{}
		}
		// Restamp the window title from the current path so terminal tabs
		// stay accurate as the operator drills in or pops back out.
		m.title = windowTitleFor(m.tree.BreadcrumbCrumbs())
		m.syncChatContext()
		if m.tree.Children() == nil {
			m.tree.Loading = true
			return m, m.loadTreeFocus()
		}
		return m, nil

	case sections.TreeChildrenLoadedMsg:
		m.tree.Loading = false
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
		m.syncChatContext()
		debug.Log("tree.loaded",
			"kind", string(focus.Kind),
			"name", focus.Name,
			"children", childCount(msg.Children))
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
		if m.state == stateMain && m.ctx.ActiveContext != "" &&
			m.tree.Focus().Kind != k8s.KindContextList {
			cmd = m.loadTreeFocus()
			debug.Log("refresh.tick", "context", m.ctx.ActiveContext, "focus", string(m.tree.Focus().Kind))
		}
		return m, tea.Batch(m.autoRefreshTick(), cmd)

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
		// that's the chat textarea; the scale modal's input is short
		// enough that v1 fragmented-keystroke behaviour is fine. Modal
		// states (logs/events/describe/shell/help/confirm) don't accept
		// paste, so we drop it there.
		if m.state == stateMain && m.chat.Focused {
			cmd, _, _ := m.chat.Update(msg, m.ctx)
			return m, cmd
		}
		return m, nil
	}

	return m, nil
}

// handleKey is the top-level key dispatcher. Modal states (logs, events,
// scale, confirm, help) consume keys before the main view sees them.
func (m *Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	// Yank (`y`) is intercepted before the per-state dispatch in every state
	// where it makes sense — the section exposes its payload via YankPayload
	// and a FilterActive guard so the user can still type a literal `y` into
	// the filter prompt. Shell forwards `y` to the pod, so it's excluded.
	if cmd, yanked := m.tryYank(msg); yanked {
		return m, cmd
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

	case stateDeleteConfirm, stateRollbackConfirm, stateRestartConfirm, stateEditConfirm:
		return m, m.handleConfirm(msg)

	case stateHelp:
		if key.Matches(msg, m.ctx.Keys.Back) || key.Matches(msg, m.ctx.Keys.Help) || msg.String() == "q" {
			m.state = stateMain
		}
		return m, nil

	case statePalette:
		return m.handlePalette(msg)
	}

	// Main state — global keys first, then chat (when focused), then tree.

	// Chat-focused: chat owns the keystream. Letter keybindings the tree
	// uses (q/?/l/e/d/s/r/x/S/F/i/R) must reach the textarea as literal
	// characters when the user is typing a question — otherwise pressing
	// `?` mid-sentence opens the help modal and "scale" becomes "cale".
	// Only Ctrl+C survives as a universal panic-exit; everything else
	// flows into the chat handler. Tab/Esc inside the chat return
	// leave=true to hand focus back to the tree.
	if m.chat.Focused {
		if msg.String() == "ctrl+c" {
			m.saveConfig()
			m.ctx.Cancel()
			return m, tea.Quit
		}
		cmd, submit, leave := m.chat.Update(msg, m.ctx)
		if leave {
			m.chat.SetFocus(false)
			m.tree.Focused = true
			return m, cmd
		}
		if submit != nil {
			// Chat already appended a placeholder turn; start the stream.
			snap := sections.BuildContextSnapshot(&m.tree, m.ctx.ActiveContext)
			full := submit.Prompt
			if snap.Block != "" {
				full = snap.Block + "\n\n" + submit.Prompt
			}
			return m, tea.Batch(cmd, sections.NewChatStream(m.ctx.Background, m.claudeSvc, full))
		}
		return m, cmd
	}

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

	case key.Matches(msg, m.ctx.Keys.Refresh):
		if m.ctx.ActiveContext != "" && m.tree.Focus().Kind != k8s.KindContextList {
			m.tree.InvalidateFocus()
			m.tree.Loading = true
			return m, m.loadTreeFocus()
		}
		return m, nil

	case msg.String() == "tab",
		key.Matches(msg, m.ctx.Keys.Ask) && !m.tree.FilterActive():
		// Tree → chat. Tab is the universal "switch panel" key; `i`
		// (Ask) is the vim-discoverable alias surfaced in the footer.
		// Both jump straight into typing in the chat. We gate Ask on
		// FilterActive so the user can still type a literal `i` into the
		// filter prompt without jumping to chat.
		m.tree.Focused = false
		m.chat.SetFocus(true)
		m.syncChatContext()
		return m, nil
	}

	// Resource-action keys (kind-aware dispatch via the tree).
	if cmd, handled := m.handleTreeAction(msg); handled {
		return m, cmd
	}

	// Fall through to the tree itself for navigation / drill / filter.
	cmd := m.tree.Update(msg, m.ctx)
	// Tree state may have moved — refresh chat's context strip + chips.
	m.syncChatContext()
	return m, cmd
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
	m.syncChatContext()
	return m, cmd
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
			m.scaleInput.SetValue("")
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
			m.state = stateDeleteConfirm
			return nil, true
		}
	case key.Matches(msg, m.ctx.Keys.Delete):
		if ref.Kind == k8s.KindPod {
			m.state = stateDeleteConfirm
			return nil, true
		}
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
func actionTarget(ref sections.NodeRef) (kind, namespace, name string) {
	switch ref.Kind {
	case k8s.KindApp:
		return ref.SubKind, ref.Namespace, ref.Name
	case k8s.KindPod:
		return "Pod", ref.Namespace, ref.Name
	case k8s.KindNode:
		return "Node", "", ref.Name
	case k8s.KindNamespace:
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

func (m *Model) handleConfirm(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "y", "Y":
		ref := m.tree.SelectedRef()
		switch m.state {
		case stateDeleteConfirm:
			if ref.Kind == k8s.KindPod {
				m.state = stateMain
				return sections.DeletePod(m.ctx.Background, m.ctx.Manager, m.ctx.ActiveContext, ref.Namespace, ref.Name)
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
	return tea.Batch(tea.SetClipboard(payload), m.raiseStatus("copied")), true
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
	}
	return 0
}
