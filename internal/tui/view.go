package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/YunusAndreasson/safina/internal/k8s"
	"github.com/YunusAndreasson/safina/internal/tui/components"
	"github.com/YunusAndreasson/safina/internal/tui/keys"
	"github.com/YunusAndreasson/safina/internal/tui/sections"
	"github.com/YunusAndreasson/safina/internal/tui/theme"
)

// View returns the rendered frame plus the declarative terminal-state fields
// Bubble Tea v2 applies on top of it. Alt-screen and mouse cell motion live
// here (rather than as Program options) because v2 unified all terminal
// features into the per-frame tea.View — the renderer just makes the terminal
// match whatever fields we set every tick.
//
// KeyboardEnhancements requests the Kitty/CSI-u protocol where supported so
// the chat textarea can disambiguate Enter (send) from Shift+Enter (newline)
// — impossible in v1 because every terminal collapsed both onto the same
// byte. Terminals that don't support the protocol fall back gracefully to
// v1 semantics; nothing breaks.
//
// ReportFocus opts us into FocusMsg / BlurMsg from the terminal so update.go
// can pause auto-refresh while the operator is in another window — saves API
// quota and lets the data freeze at the moment they walked away, so when
// they come back the topbar's "↻ Ns" tells the truth about staleness.
//
// Bracketed paste mode is enabled by default in tea v2 — multi-line pastes
// arrive as a single PasteMsg instead of fragmented keystrokes, which is
// what the chat textarea relies on for YAML / log snippets.
func (m *Model) View() tea.View {
	content := m.render()
	v := tea.View{
		Content:       content,
		AltScreen:     true,
		MouseMode:     tea.MouseModeCellMotion,
		ReportFocus:   true,
		WindowTitle:   m.title,
		ProgramStatus: m.programStatus(),
		KeyboardEnhancements: tea.KeyboardEnhancements{
			ReportAlternateKeys:        true,
			ReportEventTypes:           m.state != stateShellView,
			ReportAllKeysAsEscapeCodes: m.state != stateShellView,
			ReportAssociatedText:       m.state != stateShellView,
		},
	}
	// Hardware cursor is shown on the Chat tab and on the input-bearing
	// modals (scale, port-forward). Everywhere else the caret is implied
	// by the row highlight. For the modals, composeCard + the per-modal
	// renderer have already stashed the card's screen position and the
	// row offset of the input within the card content; here we just
	// translate the textinput's own cursor X back into absolute coords.
	//
	// Chat cursor is suppressed when the operator has tabbed to the
	// tree (treeFocusInImmersive) — keys land on the tree, so a blinking
	// chat cursor would be a lie about where input is going. The chat
	// surface stays rendered with its blurred-input styling; pressing
	// Tab again restores the cursor and the textarea focus.
	// This feature applies only to the states listed; all others use the fallback.
	//exhaustive:ignore
	switch m.state {
	case stateShellView:
		v.Cursor = m.shell.Cursor(m.ctx.Width, m.ctx.Height)
	case stateChatView:
		if !m.treeFocusInImmersive {
			v.Cursor = m.chat.Cursor()
		}
	case stateScaleInput:
		v.Cursor = m.modalInputCursor(m.scaleInput.Cursor())
	case statePortForwardInput:
		v.Cursor = m.modalInputCursor(m.pfInput.Cursor())
	default:
		// remaining states carry no textinput cursor to project
	}
	if m.choices != nil {
		v.Cursor = nil
	}
	return v
}

// programStatus lets supporting terminals show activity in background tabs.
// Keep messages generic so resource names and chat content stay in the TUI.
func (m *Model) programStatus() *tea.ProgramStatus {
	status := &tea.ProgramStatus{App: "safina", State: tea.ProgramStateIdle}
	// This feature applies only to the states listed; all others use the fallback.
	//exhaustive:ignore
	switch m.state {
	case stateDeleteConfirm, stateRollbackConfirm, stateRestartConfirm,
		stateRestartPodConfirm, stateEditConfirm, stateCordonConfirm, stateDrainConfirm:
		status.State = tea.ProgramStateBlocked
		status.Kind = tea.ProgramStatusKindPermission
		status.Message = "Waiting for confirmation"
	default:
		if m.chat.Streaming() {
			status.State = tea.ProgramStateWorking
			status.Message = "Chat is responding"
		}
	}
	return status
}

// modalInputCursor translates a textinput's locally-rooted cursor (X
// counted from the textinput's prompt origin, Y always 0 for single-line
// inputs) into screen coordinates inside the centered modal card. The
// per-frame stamps from composeCard (modalCardX/Y) and the per-modal
// render fn (modalInputRow) give us the card origin and the input's row
// offset; t.Modal contributes a 1-cell border + 1×2 padding on each side
// of its content area.
func (m *Model) modalInputCursor(cur *tea.Cursor) *tea.Cursor {
	if cur == nil {
		return nil
	}
	// Border (1) + horizontal padding (2) = 3 cells before content[0,0]'s X.
	// Border (1) + vertical padding (1) = 2 rows before content[0,0]'s Y.
	cur.X += m.modalCardX + 3
	cur.Y = m.modalCardY + 2 + m.modalInputRow
	return cur
}

func (m *Model) render() string {
	w, h := m.ctx.Width, m.ctx.Height
	if w < 50 || h < 14 {
		return m.ctx.Theme.DimPad.Render(fmt.Sprintf("Resize terminal to at least 50 × 14.\nCurrent size: %d × %d. ", w, h) + components.RenderModalKeysRow(m.ctx.Theme, []components.KeyDesc{{Key: "ctrl+c", Desc: "quit"}}))
	}

	// Shell takes the full screen — it owns a pty session, so the tree /
	// workspace composition is meaningless during a shell session.
	if m.state == stateShellView {
		return m.shell.View(m.ctx, w, h)
	}

	if m.state == stateIncoming {
		return m.renderIncoming()
	}
	overlayState := m.state
	m.state = m.baseState()
	base := m.renderBase(w, h)
	m.state = overlayState
	if m.choices != nil {
		return m.composeCard(base, m.choicesCard())
	}

	// Overlays compose ON TOP of the base — the tree and workspace stay
	// visible underneath a dimmed backdrop. This is the cosmic-horizon
	// design choice: nothing swallows the screen except shells. Confirms,
	// pickers, palette, help, cluster switcher all float.
	// This feature applies only to the states listed; all others use the fallback.
	//exhaustive:ignore
	switch m.state {
	case stateHelp:
		return m.composeCard(base, m.renderHelpModal())
	case statePalette:
		return m.composeCard(base, m.palette.Card(m.ctx, w, h))
	case stateClusters:
		return m.composeCard(base, m.clusters.Card(m.ctx, w, h, m.ctx.ActiveContext))
	case stateBrowser:
		return m.composeCard(base, m.browser.Card(m.ctx, w, h))
	case stateScaleInput:
		return m.composeCard(base, m.renderScaleModal())
	case stateDeleteConfirm:
		verb, subject := m.deleteConfirmText()
		return m.composeCard(base, m.renderConfirmModal(verb, subject))
	case stateRollbackConfirm:
		return m.composeCard(base, m.renderConfirmModal("rollback deployment", m.deployName()))
	case stateRestartConfirm:
		return m.composeCard(base, m.renderConfirmModal("restart app", m.deployName()))
	case stateRestartPodConfirm:
		return m.composeCard(base, m.renderConfirmModal("restart pod", m.tree.SelectedRef().Name))
	case stateEditConfirm:
		return m.composeCard(base, m.renderEditConfirmModal())
	case statePortForwardInput:
		return m.composeCard(base, m.renderPortForwardModal())
	case stateCordonConfirm:
		verb := "cordon"
		if !m.nodeTarget.Schedulable {
			verb = "uncordon"
		}
		return m.composeCard(base, m.renderConfirmModal(verb+" node", m.nodeTarget.Name))
	case stateDrainConfirm:
		subj := m.nodeTarget.Name
		if m.drainConfirmPods >= 0 {
			subj = fmt.Sprintf("%s · evicts %d pods", m.nodeTarget.Name, m.drainConfirmPods)
		}
		return m.composeCard(base, m.renderConfirmModal("drain node", subj))
	default:
		// stateMain + immersive workspace tabs — no overlay; the base render is final.
		return base
	}
}

// composeCard keeps the current screen visible but subdued behind a floating
// dialog. Lip Gloss owns cell clipping and z-order, including the offset shadow.
func (m *Model) composeCard(base, card string) string {
	w, h := m.ctx.Width, m.ctx.Height
	cardW, cardH := lipgloss.Size(card)
	cx, cy := max(0, (w-cardW)/2), max(0, (h-cardH)/2)
	m.modalCardX, m.modalCardY = cx, cy
	t := m.ctx.Theme
	backdrop := lipgloss.NewStyle().Foreground(t.P.Dim).Background(t.P.BG).Render(ansi.Strip(base))
	shadow := lipgloss.NewStyle().Background(t.P.BG).Width(cardW).Height(cardH).Render("")
	comp := lipgloss.NewCompositor(
		lipgloss.NewLayer(backdrop).Z(0),
		lipgloss.NewLayer(shadow).X(cx+2).Y(cy+1).Z(1),
		lipgloss.NewLayer(card).X(cx).Y(cy).Z(2),
	)
	canvas := lipgloss.NewCanvas(w, h).Compose(comp)
	// Nested styled text resets to the terminal's default background. Fill only
	// unspecified backgrounds inside the card, preserving explicit input/error
	// surfaces and leaving the rounded border untouched. The library still owns
	// ANSI parsing, wide cells and composition.
	for y := cy + 1; y < min(h, cy+cardH-1); y++ {
		for x := cx + 1; x < min(w, cx+cardW-1); x++ {
			cell := canvas.CellAt(x, y)
			if cell != nil && cell.Width > 0 && cell.Style.Bg == nil {
				filled := *cell
				filled.Style.Bg = t.P.BG2
				canvas.SetCell(x, y, &filled)
			}
		}
	}
	return canvas.Render()
}

// renderBase composes the steady-state frame: topbar / optional banner /
// main split / footer. The chat drawer is part of the main split when open.
func (m *Model) renderBase(w, h int) string {
	// Stamp the staleness flag on the shared Ctx before any section
	// renders this frame — metric cells (sparklines + colored values)
	// desaturate when this is true so the data carries its own age signal
	// directly. Two triggers: refresh-age has
	// stretched past the auto-refresh cadence, OR the terminal lost OS
	// focus and refreshes are paused.
	m.ctx.Stale = m.dataLooksStale()
	parts := []string{}
	used := 1 // footer (reserved)

	// Topbar: brand + breadcrumb (with brightness ramp) +
	// health on the right. No mode capsule — the focused border already
	// answers "what surface owns my keystrokes".
	fwdCount := 0
	if m.ctx.Manager != nil && m.ctx.Manager.Forwards != nil {
		fwdCount = m.ctx.Manager.Forwards.Count()
	}
	parts = append(parts, components.Topbar(
		m.ctx.Theme, w,
		m.tree.BreadcrumbCrumbs(),
		fwdCount,
		m.topbarClusterHealth(),
		m.overviewPreview,
	))
	used++
	if m.incoming.inbox.Pending > 0 {
		parts = append(parts, m.ctx.Theme.CyanBold.Render(fmt.Sprintf("Incoming proposals: %d · ", m.incoming.inbox.Pending))+components.RenderModalKeysRow(m.ctx.Theme, []components.KeyDesc{{Key: "ctrl+p", Desc: "search Incoming proposals"}}))
		used++
	}

	if m.hasFeedback() {
		banner := m.renderBanner(w)
		parts = append(parts, banner)
		used += lipgloss.Height(banner)
	}

	botH := h - used
	if botH < 8 {
		botH = 8
	}

	parts = append(parts, m.renderMainSplit(w, botH), m.renderFooter())
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

// renderMainSplit composes the body region: a clean two-pane φ split.
// Tree on the left (38.2%), workspace on the right (61.8%). Chat lives
// inside the workspace as a tab — no third pane, no drawer geometry. The
// active surface (tree vs workspace) is implied by which tab is active:
// Inspector tab → tree drives, immersive tabs (Logs/Events/Yaml/Chat/Hot
// /Forwards/Drain) → workspace drives.
const minSplitWidth = 60

func (m *Model) renderMainSplit(w, h int) string {
	m.connected.Hide()
	workspaceFocused := m.workspaceOwnsFocus()
	treeW, wsW := theme.PhiSplit(w, 0, 28)
	if wsW < 32 {
		wsW = 32
		treeW = w - wsW
	}
	if w < minSplitWidth {
		if !workspaceFocused {
			m.tree.Focused = true
			m.tabHitboxes = nil
			return m.tree.View(m.ctx, w, h)
		}
		treeW, wsW = 0, w
	}
	if m.workspaceExpanded && m.canExpandWorkspace() && !m.treeFocusInImmersive {
		treeW, wsW = 0, w
	}

	// Only one pane wears the gradient border. Tree.Focused is the
	// persistent canonical value — set it here so a single source of
	// truth flows both rendering (border / row highlight) and update.go
	// (state-transition logic that reads m.tree.Focused). The previous
	// defer-restore pattern left a different value persistent than the
	// one rendered, which produced single-frame inconsistencies right
	// after entering an immersive tab (the tree briefly looked focused
	// because update.go's pre-render value hadn't been synced down).
	m.tree.Focused = !workspaceFocused

	// Chat lives inside the workspace tab; record its screen origin so the
	// hardware cursor lands at the right pixel when the user types.
	originX := treeW
	originY := 1
	if m.hasFeedback() {
		originY += lipgloss.Height(m.renderBanner(w))
	}
	m.chat.SetScreenOrigin(originX, originY)

	// Stash workspace tab strip geometry for the mouse-click router. The
	// strip lives on the first content row inside the workspace panel: one
	// cell right of the panel's left border, one cell down from the top
	// border. Hit-testing in update.go translates a click column back into
	// the WorkspaceTab the operator wants to activate.
	m.tabStripOriginX = treeW + 1
	m.tabStripOriginY = originY + 1
	availability := m.workspaceTabAvailability()
	tabs := sections.AvailableTabs(availability)
	m.tabHitboxes = sections.WorkspaceTabHitboxes(tabs, m.activeWorkspaceTab(), sections.WorkspaceBodyWidth(wsW))
	if m.showWelcomeSail() {
		m.tabHitboxes = nil
	}

	workspaceView := m.renderWorkspace(wsW, h, workspaceFocused)
	if treeW == 0 {
		return workspaceView
	}
	treeView := m.tree.View(m.ctx, treeW, h)
	return lipgloss.JoinHorizontal(lipgloss.Top, treeView, workspaceView)
}

// renderWorkspace builds the right-hand workspace pane: a tab strip + the
// active tab's body, wrapped in a single panel that takes the focus
// gradient when active. The tab strip lives in the panel's first content
// row (rather than the title bar) so per-tab styling survives.
func (m *Model) renderWorkspace(w, h int, focused bool) string {
	t := m.ctx.Theme
	if m.showWelcomeSail() {
		return components.PanelWithAccent(t, "", w, h,
			sections.WelcomeSail(t, sections.WorkspaceBodyWidth(w), max(1, h-2)), focused, t.P.Cyan, t.P.Dim)
	}
	active := m.activeWorkspaceTab()
	m.connected.Focused = focused && active == sections.WSTabInspector
	availability := m.workspaceTabAvailability()
	tabs := sections.AvailableTabs(availability)
	if !containsTab(tabs, active) {
		active = sections.WSTabInspector
	}

	inner := sections.WorkspaceBodyWidth(w)
	bodyH := sections.WorkspaceBodyHeight(h)
	tabStrip := sections.RenderWorkspaceTabStrip(t, tabs, active, inner)
	title := m.workspaceTitle(active)
	body := m.renderTabBody(active, inner, bodyH)
	return sections.RenderWorkspacePane(t, w, h, title, tabStrip, body, focused)
}

func (m *Model) showWelcomeSail() bool {
	return m.tree.Focus().Kind == k8s.KindContextList && m.activeWorkspaceTab() == sections.WSTabInspector
}

func containsTab(tabs []sections.WorkspaceTab, want sections.WorkspaceTab) bool {
	for _, t := range tabs {
		if t == want {
			return true
		}
	}
	return false
}

// workspaceOwnsFocus returns true when the model state designates the
// workspace pane as the active surface — either an immersive tab is open
// and consuming keystrokes, or the operator pressed Tab to peek at the
// workspace without changing the active view. The tree stays visible but
// the gradient border migrates to the workspace.
//
// treeFocusInImmersive is the override: when set, the operator pressed
// Tab while an immersive section was active to redirect keys to the
// tree. The section keeps rendering but the gradient border migrates
// back to the tree, matching where the keys are actually landing.
func (m *Model) workspaceOwnsFocus() bool {
	if m.treeFocusInImmersive {
		return false
	}
	if m.peekWorkspace {
		return true
	}
	// This feature applies only to the states listed; all others use the fallback.
	//exhaustive:ignore
	switch m.state {
	case stateMetricsView, stateLogView, stateEventsView, stateDescribeView,
		stateHotView, stateForwardsList, stateDrainProgress,
		stateChatView:
		return true
	default:
		return false
	}
}

// activeWorkspaceTab derives which tab the workspace should display from
// the model's current state. Immersive states (logs / events / describe /
// hot / forwards / drain) drive the matching tab; everything else falls
// back to Inspector (the default).
func (m *Model) activeWorkspaceTab() sections.WorkspaceTab {
	// This feature applies only to the states listed; all others use the fallback.
	//exhaustive:ignore
	switch m.state {
	case stateMetricsView:
		return sections.WSTabMetrics
	case stateLogView:
		return sections.WSTabLogs
	case stateEventsView:
		return sections.WSTabEvents
	case stateDescribeView:
		return sections.WSTabYaml
	case stateChatView:
		return sections.WSTabChat
	case stateHotView:
		return sections.WSTabHot
	case stateForwardsList:
		return sections.WSTabForwards
	case stateDrainProgress:
		return sections.WSTabDrain
	default:
		return sections.WSTabInspector
	}
}

// workspaceTabAvailability returns which tabs are exposed in the strip
// given the current selection and active operations. Standard tabs
// (Logs / Events / Yaml) appear when the selected resource supports them;
// pinned tabs (Hot / Forwards / Drain) appear only while their op is live.
func (m *Model) workspaceTabAvailability() sections.WSTabAvailability {
	ref := m.tree.SelectedRef()
	av := sections.WSTabAvailability{Metrics: sections.MetricsSupported(ref) || m.state == stateMetricsView}
	switch ref.Kind {
	case k8s.KindGenericResource:
		av.Events, av.Yaml = ref.SubKind != "", !ref.GVR.Empty()
	case k8s.KindPod, k8s.KindContainer:
		av.Logs, av.Events, av.Yaml = true, true, true
	case k8s.KindApp:
		av.Logs, av.Events, av.Yaml = true, true, true
	case k8s.KindNode:
		av.Events, av.Yaml = true, true
	case k8s.KindNamespace, k8s.KindNamespaceFolder,
		k8s.KindService, k8s.KindIngress, k8s.KindPVC,
		k8s.KindConfigMap, k8s.KindSecret, k8s.KindPV:
		av.Events, av.Yaml = true, true
	}
	// Chat is always offered once a cluster is picked — the LLM is a tool
	// the operator always has on the right side of the screen, not a mode.
	if m.ctx.ActiveContext != "" {
		av.Chat = true
	}
	// An open reading view stays visible while the tree moves to another
	// resource or folder. The tab title continues to identify its own source.
	//exhaustive:ignore
	switch m.state {
	case stateLogView:
		av.Logs = true
	case stateEventsView:
		av.Events = true
	case stateDescribeView:
		av.Yaml = true
	case stateHotView:
		av.Hot = true
	case stateForwardsList:
		av.Forwards = true
	case stateDrainProgress:
		av.Drain = true
	}

	return av
}

// The tab names the view; the border names only its resource. Use the
// section source while reading so tree navigation cannot mislabel old content.
func (m *Model) workspaceTitle(tab sections.WorkspaceTab) string {
	//exhaustive:ignore
	switch tab {
	case sections.WSTabMetrics:
		return m.metrics.Ref.Name
	case sections.WSTabLogs:
		return m.logs.PaneTitle()
	case sections.WSTabEvents:
		return m.events.PaneTitle()
	case sections.WSTabYaml:
		return m.describe.PaneTitle()
	case sections.WSTabHot:
		return ""
	case sections.WSTabForwards:
		return ""
	case sections.WSTabDrain:
		return strings.TrimPrefix(m.drain.PaneTitle(), "drain · ")
	default:
		if m.tree.Focus().Kind == k8s.KindContextList {
			return ""
		}
		ref := m.tree.SelectedRef()
		if ref.Kind == k8s.KindAllNamespacesFold {
			return "All user namespaces"
		}
		if ref.Kind == k8s.KindSystemFold {
			return "System namespaces"
		}
		return ref.Name
	}
}

func (m *Model) canExpandWorkspace() bool {
	if m.state == stateMain && m.peekWorkspace && !m.showWelcomeSail() {
		return true
	}
	return m.state == stateMetricsView || m.state == stateLogView || m.state == stateEventsView || m.state == stateDescribeView ||
		m.state == stateHotView || m.state == stateForwardsList
}

// renderTabBody dispatches to the section that owns the active tab's
// content. Width and height are the workspace pane's inner content area
// (the panel border + tab strip + hairline are already subtracted by
// WorkspaceBodyWidth/Height).
func (m *Model) renderTabBody(tab sections.WorkspaceTab, w, h int) string {
	t := m.ctx.Theme
	switch tab {
	case sections.WSTabMetrics:
		return m.metrics.Body(t, w, h, time.Now())
	case sections.WSTabLogs:
		m.logs.SetPaneSize(w, h)
		return m.logs.Body(m.ctx)
	case sections.WSTabEvents:
		m.events.SetPaneSize(w, h)
		return m.events.Body(m.ctx)
	case sections.WSTabYaml:
		m.describe.SetPaneSize(w, h)
		return m.describe.Body(m.ctx)
	case sections.WSTabChat:
		return m.chat.Body(m.ctx, w, h)
	case sections.WSTabHot:
		return m.hot.Body(m.ctx, w, h)
	case sections.WSTabForwards:
		return m.forwards.Body(w, h)
	case sections.WSTabDrain:
		return m.drain.Body(w)
	case sections.WSTabInspector:
		// Falls through to the Inspector default below.
	default:
	}
	// Default: Inspector tab — selection summary stack.
	if m.tree.Focus().Kind == k8s.KindContextList {
		// At the cluster picker no selection exists yet; show a calm
		// orientation block instead of an empty Inspector.
		return sections.WelcomeSail(t, w, h)
	}
	ref := m.tree.SelectedRef()
	if ref.Kind == "" {
		m.connected.Hide()
		switch {
		case m.tree.LoadErr != nil:
			return components.IllustratedEmpty(t, w, h, components.NoEmptyArt, "Resource details unavailable", "Check the resource list for the error.")
		case m.tree.Loading:
			return components.IllustratedEmpty(t, w, h, components.NoEmptyArt, "Loading resources…", "")
		case m.tree.FilterValue() != "":
			return components.IllustratedEmpty(t, w, h, components.NoEmptyArt, "No matching resources", "Change or clear the filter.")
		case m.tree.Children() != nil:
			return components.IllustratedEmpty(t, w, h, components.BeaconArt, "No resources to inspect", "Choose another resource type or namespace.")
		}
	}
	if body, ok := sections.ResourceOrientation(t, ref, w, h); ok {
		m.connected.Hide()
		return body
	}
	return sections.RenderInspectorTab(m.ctx, w, h, &m.inspector, &m.activity, &m.connected)
}

// ── banner / topbar helpers ───────────────────────────────────────────

// renderBanner draws the transient status/error line above the main split.
// Errors render in red (✗), successful action confirmations in green (✓).
func (m *Model) renderBanner(w int) string {
	if warning := m.ctx.Manager.History.Health(); warning != "" {
		return m.ctx.Theme.ErrLine.Width(w).Render(ansi.Truncate(warning, max(1, w), "…"))
	}
	if m.bannerMsg == "" {
		return m.ctx.Theme.Hint.Width(w).MaxHeight(3).Render("  ◌  " + m.operationSummary())
	}
	t := m.ctx.Theme
	if m.bannerErr {
		text := m.bannerMsg
		if m.bannerContext != "" && m.bannerContext != m.ctx.ActiveContext {
			text = m.bannerContext + ": " + text
		}
		hints := components.RenderBoundedHints(t, []components.KeyDesc{{Key: "ctrl+d", Desc: "dismiss"}, {Key: "ctrl+n", Desc: "notifications"}}, max(0, w-2), components.KeyDesc{})
		return t.ErrLine.Width(w).Render("  ✗  " + ansi.Truncate(strings.ReplaceAll(text, "\n", " "), max(1, w-5), "…") + "\n  " + hints)
	}
	return t.OkLine.Width(w).MaxHeight(3).Render("  ✓  " + m.bannerMsg)
}

// topbarClusterHealth packages the model's cluster-health snapshot into
// the topbar component's render shape. Show=false at the picker, before
// the first fetch lands, or right after a context switch — the topbar
// then drops the pill rather than rendering stale numbers.
func (m *Model) topbarClusterHealth() components.ClusterHealth {
	if m.clusterHealthCtx == "" || m.clusterHealthCtx != m.ctx.ActiveContext || !m.clusterHealth.HasData() {
		return components.ClusterHealth{}
	}
	h := m.clusterHealth
	cpuFrac := 0.0
	if h.CPUAlloc > 0 {
		cpuFrac = float64(h.CPUUsed) / float64(h.CPUAlloc)
	}
	memFrac := 0.0
	if h.MemAlloc > 0 {
		memFrac = float64(h.MemUsed) / float64(h.MemAlloc)
	}
	return components.ClusterHealth{
		Show:          true,
		NodesReady:    h.NodesReady,
		NodesTotal:    h.NodesTotal,
		PodsRunning:   h.PodsRunning,
		PodsCrashing:  h.PodsCrashing,
		PodsPending:   h.PodsPending,
		PodsTotal:     h.PodsTotal,
		CPUFrac:       cpuFrac,
		MemFrac:       memFrac,
		PressureCount: h.PressureCount,
	}
}

// dataLooksStale reports whether live data on screen is older than the
// auto-refresh cadence enough to warrant desaturating the metric cells.
// Two triggers: terminal lost OS focus (refreshes are paused, so even a
// freshly-loaded value will rot), OR last refresh is older than the
// freshness threshold. Sub-threshold values are
// considered fresh — desaturating "just refreshed" data would punish
// every normal tick.
func (m *Model) dataLooksStale() bool {
	if m.overviewPreview {
		return true
	}
	if m.ctx.ActiveContext == "" {
		return false
	}
	if !m.focused {
		return true
	}
	if m.lastRefresh.IsZero() {
		return false
	}
	// Desaturate data just past the 5s auto-refresh interval.
	return m.now.Sub(m.lastRefresh) >= 6*time.Second
}

func (m *Model) deployName() string {
	if a, ok := m.tree.SelectedAppInfo(); ok {
		return a.Name
	}
	return ""
}

// deleteConfirmText builds the kind-aware (verb, subject) pair shown in
// the delete confirm modal.
func (m *Model) deleteConfirmText() (verb, subject string) {
	ref := m.tree.SelectedRef()
	subject = ref.Name
	switch ref.Kind {
	case k8s.KindPod:
		return "delete pod", subject
	case k8s.KindConfigMap:
		return "delete configmap", subject
	case k8s.KindSecret:
		return "delete secret", subject
	case k8s.KindService:
		return "delete service", subject
	case k8s.KindIngress:
		return "delete ingress", subject
	case k8s.KindPVC:
		return "delete pvc · releases storage", subject
	case k8s.KindApp:
		return "delete " + strings.ToLower(ref.SubKind), subject
	}
	return "delete", subject
}

// ── footer ────────────────────────────────────────────────────────────

func (m *Model) renderFooter() string {
	t := m.ctx.Theme
	if ((m.state == stateMain || m.treeFocusInImmersive) && m.tree.FilterActive()) ||
		(m.state != stateChatView && m.sectionHasFilterFocus()) {
		return t.HelpDesc.Render(" type to filter · ") + components.RenderBoundedHints(t, []components.KeyDesc{{Key: "↵", Desc: "apply"}, {Key: "esc", Desc: "cancel"}}, max(0, m.ctx.Width-20), components.KeyDesc{Key: "esc", Desc: "cancel"})
	}
	var view string
	// This feature applies only to the states listed; all others use the fallback.
	//exhaustive:ignore
	switch {
	case m.state == stateDrainProgress && !m.treeFocusInImmersive:
		view = m.renderDrainFooter()
	case m.treeFocusInImmersive && m.isImmersiveState():
		view = m.renderTreeFooter()
	case m.state == stateMain && m.peekWorkspace && m.connected.Count() > 0 && m.connected.PanelHeight > 0:
		view = components.RenderBoundedHints(t, []components.KeyDesc{{Key: "↑/↓", Desc: "links"}, {Key: "↵", Desc: "open"}, {Key: "←/tab", Desc: "tree"}, {Key: "→", Desc: "tabs"}, {Key: "esc", Desc: "back"}, {Key: "z", Desc: "expand"}}, m.ctx.Width-2, components.KeyDesc{Key: "?", Desc: "help"})
	case m.state == stateMain && m.peekWorkspace:
		view = components.RenderBoundedHints(t, []components.KeyDesc{{Key: "←/tab", Desc: "tree"}, {Key: "→", Desc: "tabs"}, {Key: "1", Desc: "back"}, {Key: "?", Desc: "help"}}, m.ctx.Width-2, components.KeyDesc{Key: "?", Desc: "help"})
	case m.state == stateChatView:
		view = m.renderChatFooter()
	case m.state == stateMetricsView || m.state == stateLogView || m.state == stateEventsView || m.state == stateDescribeView ||
		m.state == stateHotView || m.state == stateForwardsList:
		view = m.renderImmersiveFooter()
	default:
		view = m.renderTreeFooter()
	}
	return lipgloss.NewStyle().
		Background(t.P.BG).
		Width(m.ctx.Width).
		MaxHeight(1).
		Padding(0, 1).
		Render(view)
}

// renderTreeFooter follows the selected resource even while another tab stays open.
func (m *Model) renderChatFooter() string {
	action := components.KeyDesc{Key: "↵", Desc: "send"}
	if m.chat.Streaming() {
		action = components.KeyDesc{Key: "esc", Desc: "cancel"}
	}
	return components.RenderBoundedHints(m.ctx.Theme, []components.KeyDesc{
		action,
		{Key: "tab", Desc: "tree"},
		{Key: "⇧↵", Desc: "newline"},
		{Key: "⌃l", Desc: "clear"},
		{Key: "⌃n", Desc: "new"},
	}, m.ctx.Width-2, components.KeyDesc{})
}

func (m *Model) renderImmersiveFooter() string {
	hints := []components.KeyDesc{{Key: "tab", Desc: "tree"}, {Key: "esc/q", Desc: "close"}}
	if m.ctx.Width >= minSplitWidth {
		zoom := "expand"
		if m.workspaceExpanded {
			zoom = "split"
		}
		hints = append(hints, components.KeyDesc{Key: "z", Desc: zoom})
	}
	hints = append(hints, components.KeyDesc{Key: "←/→", Desc: "tabs"}, components.KeyDesc{Key: "?", Desc: "help"})
	return components.RenderBoundedHints(m.ctx.Theme, hints, m.ctx.Width-2, components.KeyDesc{Key: "?", Desc: "help"})
}

// renderDrainFooter distinguishes leaving progress from cancelling work.
func (m *Model) renderDrainFooter() string {
	return components.RenderBoundedHints(m.ctx.Theme, []components.KeyDesc{
		{Key: "esc", Desc: "leave running"},
		{Key: "tab", Desc: "tree"},
		{Key: "⌃c", Desc: "cancel", Write: true},
	}, m.ctx.Width-2, components.KeyDesc{})
}

// contextualShortHelp builds the footer keymap from the current tree row.
//
// Convention:
//
//  1. Universal nav (↑/↓/↵/esc) is never restated — Jakob's rule.
//  2. Reads (lowercase) come first, mutators (uppercase / ctrl-chord)
//     come after. Within each half, alphabetical by source-of-truth
//     definition order.
//  3. Tail chrome (refresh, filter, help, quit) sits at the right, dim,
//     so global affordances read as a separate visual register.
func (m *Model) contextualShortHelp() []key.Binding {
	k := m.ctx.Keys
	tail := []key.Binding{k.Search, k.Filter, k.Help, k.Quit}
	if m.tree.SelectedRef().Kind != "" && m.tree.SelectedRef().Kind != k8s.KindContainer {
		tail = append([]key.Binding{key.NewBinding(key.WithKeys("enter"), key.WithHelp("↵", "open"))}, tail...)
	}
	if m.tree.Focus().Kind != k8s.KindContextList {
		tail = append([]key.Binding{k.Refresh, key.NewBinding(key.WithKeys("left"), key.WithHelp("←", "parent"))}, tail...)
	}

	ref := m.tree.SelectedRef()
	reads := []key.Binding{}
	mutators := []key.Binding{}
	switch ref.Kind {
	case k8s.KindGenericResource:
		if !ref.GVR.Empty() {
			reads = append(reads, k.Describe)
		}
		if ref.SubKind != "" {
			reads = append(reads, k.Events)
		}
	case k8s.KindContext:
		// Cluster picker: the inline "↵ open" hint in the tree body already
		// teaches the drill verb. Don't restate Enter here — Jakob's rule.
		return tail
	case k8s.KindApp:
		reads = append(reads, k.Describe, k.Logs, k.Events, k.Metrics)
		if ref.SubKind == k8s.AppKindDeployment || ref.SubKind == k8s.AppKindStatefulSet {
			mutators = append(mutators, k.Scale)
		}
		if ref.SubKind == k8s.AppKindDeployment {
			mutators = append(mutators, k.Rollback)
		}
		if ref.SubKind == k8s.AppKindDeployment ||
			ref.SubKind == k8s.AppKindStatefulSet ||
			ref.SubKind == k8s.AppKindDaemonSet {
			mutators = append(mutators, k.Restart)
		}
	case k8s.KindPod:
		reads = append(reads, k.Describe, k.Logs, k.Events, k.Metrics, k.Shell, k.PortForward)
		mutators = append(mutators, k.Restart)
	case k8s.KindContainer:
		reads = append(reads, k.Describe, k.Logs, k.Events, k.Shell)
	case k8s.KindNode:
		reads = append(reads, k.Describe, k.Events, k.Metrics)
		cordonVerb := k.Cordon
		if info, ok := m.tree.SelectedNodeInfo(); ok && !info.Schedulable {
			cordonVerb = key.NewBinding(
				key.WithKeys(k.Cordon.Help().Key),
				key.WithHelp(k.Cordon.Help().Key, "uncordon"),
			)
		}
		mutators = append(mutators, cordonVerb, k.Drain)
	case k8s.KindNamespace, k8s.KindNamespaceFolder:
		reads = append(reads, k.Describe, k.Events)
	case k8s.KindConfigMap:
		reads = append(reads, k.Describe)
	case k8s.KindSecret:
		reads = append(reads, k.Describe, k.Reveal)
	case k8s.KindService:
		reads = append(reads, k.Describe, k.Events, k.PortForward)
	case k8s.KindPVC:
		reads = append(reads, k.Describe, k.Events)
	case k8s.KindIngress:
		reads = append(reads, k.Describe, k.Events)
	case k8s.KindPV:
		reads = append(reads, k.Describe, k.Events)
	}
	if editKindFor(ref) != "" {
		mutators = append(mutators, k.Edit)
	}
	if deletableKind(ref) {
		mutators = append(mutators, k.Delete)
	}
	return append(append(reads, mutators...), tail...)
}

// ── modals ────────────────────────────────────────────────────────────

func (m *Model) renderScaleModal() string {
	deploy := m.deployName()
	subtitle := "Replicas"
	if app, ok := m.tree.SelectedAppInfo(); ok {
		if cur := currentDesiredReplicas(app); cur >= 0 {
			subtitle = fmt.Sprintf("Replicas · current: %d", cur)
		}
	}
	return m.renderInputDialog("Scale "+deploy, m.tree.SelectedRef().Namespace,
		subtitle, m.scaleInput.View(), "0 stops all replicas", m.scaleError, "scale", true)
}

func currentDesiredReplicas(app k8s.AppInfo) int {
	parts := strings.SplitN(app.Ready, "/", 2)
	if len(parts) != 2 {
		return -1
	}
	n := 0
	for _, r := range parts[1] {
		if r < '0' || r > '9' {
			return -1
		}
		n = n*10 + int(r-'0')
	}
	return n
}

func (m *Model) renderPortForwardModal() string {
	subject := strings.ToLower(m.pfTarget.Kind) + " " + m.pfTarget.Name
	return m.renderInputDialog("Port forward "+subject, m.pfTarget.Namespace,
		"Ports · local:remote", m.pfInput.View(), "One port uses the same port on both sides", m.pfError, "start", false)
}

// renderInputDialog owns form spacing, scope, feedback, and cursor placement.
// Measure the rendered header so wrapped resource names cannot move the cursor
// away from its input. Both forms share the same modal envelope; a write
// (scale, not port-forward) takes the danger red, like every confirmation.
func (m *Model) renderInputDialog(title, namespace, label, input, help, validation, action string, write bool) string {
	t := m.ctx.Theme
	w := max(1, min(66, m.ctx.Width-8))
	titleStyle, frame := t.CyanBold, t.Modal
	if write {
		titleStyle, frame = t.Danger, t.ModalDanger
	}
	parts := []string{titleStyle.Width(w).Render(title), t.DimTxt.Width(w).Render(m.actionScope(namespace))}
	if label != "" {
		parts = append(parts, t.DimTxt.Width(w).Render(label))
	}
	m.modalInputRow = lipgloss.Height(strings.Join(parts, "\n")) + 1
	feedback := t.Hint.Width(w).Render(help)
	if validation != "" {
		feedback = t.ErrLine.Width(w).Render(validation)
	}
	parts = append(parts, "", input, feedback, "", components.RenderHintRow(t, []components.KeyDesc{
		{Key: "↵", Desc: action, Write: write}, {Key: "esc", Desc: "cancel"},
	}))
	return frame.Width(w + 6).Render(lipgloss.JoinVertical(lipgloss.Left, parts...))
}

func (m *Model) renderEditConfirmModal() string {
	t := m.ctx.Theme
	w := max(10, min(100, m.ctx.Width-10))
	subject := "edit"
	resource := ""
	stat := ""
	body := ""
	if m.edit != nil {
		added, removed := m.edit.LineDiffStat()
		subject = "apply edits to " + m.edit.Kind
		resource = m.edit.Namespace + "/" + m.edit.Name
		stat = lipgloss.NewStyle().Foreground(t.P.Green).Render(fmt.Sprintf("+%d", added)) +
			"  " +
			lipgloss.NewStyle().Foreground(t.P.Red).Render(fmt.Sprintf("−%d", removed)) +
			t.DimTxt.Render(" lines")
		body = renderUnifiedDiff(t, m.edit.Orig, m.edit.Edited)
	}
	m.editViewport.SetWidth(w)
	m.editViewport.SetHeight(max(1, min(20, m.ctx.Height-17)))
	m.editViewport.SoftWrap = true
	m.editViewport.SetContent(body)

	resourceStyle := lipgloss.NewStyle().Foreground(t.P.FG).Bold(true)
	parts := []string{
		t.Danger.Width(w).Render(subject),
		"",
		resourceStyle.Width(w).Render(resource),
		t.DimTxt.Width(w).Render("cluster: " + m.ctx.ActiveContext),
		"",
		stat,
	}
	if body != "" {
		parts = append(parts, "", m.editViewport.View())
	}
	parts = append(parts, "", components.RenderConfirmHint(t, "apply"), components.RenderModalKeysRow(t, []components.KeyDesc{{Key: "↑/↓/pgup/pgdn", Desc: "scroll diff"}}))

	content := lipgloss.JoinVertical(lipgloss.Left, parts...)
	return t.ModalDanger.Render(content)
}

// renderConfirmModal is the y/n gate for every write but edit and scale:
// delete, rollback, restart, cordon, drain. Framed in the danger red.
func (m *Model) renderConfirmModal(action, resource string) string {
	t := m.ctx.Theme
	resourceStyle := lipgloss.NewStyle().Foreground(t.P.FG).Bold(true)
	content := lipgloss.JoinVertical(lipgloss.Left,
		t.Danger.Render(action),
		"",
		resourceStyle.Render(resource),
		t.DimTxt.Render(m.actionScope(m.tree.SelectedRef().Namespace)),
		"",
		components.RenderConfirmHint(t, strings.Fields(action)[0]),
	)
	return t.ModalDanger.Width(max(20, min(72, m.ctx.Width-2))).Render(content)
}

// actionScope makes the target explicit even when the dialog hides the header.
func (m *Model) actionScope(namespace string) string {
	scope := "cluster: " + m.ctx.ActiveContext
	if namespace != "" {
		scope += "\nnamespace: " + namespace
	}
	return scope
}

func (m *Model) renderHelpModal() string {
	t := m.ctx.Theme
	w := max(1, min(94, m.ctx.Width-8))
	groups := m.ctx.Keys.Groups()
	cols := make([]string, 0, len(groups))
	for _, g := range groups {
		cols = append(cols, renderHelpGroup(t, g))
	}
	body := lipgloss.JoinHorizontal(lipgloss.Top,
		lipgloss.NewStyle().PaddingRight(3).Render(cols[0]),
		lipgloss.NewStyle().PaddingRight(3).Render(cols[1]), cols[2])
	if lipgloss.Width(body) > w {
		body = strings.Join(cols, "\n\n")
	}
	m.helpViewport.SetWidth(w)
	m.helpViewport.SetHeight(max(1, min(lipgloss.Height(body), m.ctx.Height-10)))
	m.helpViewport.SoftWrap = true
	m.helpViewport.SetContent(body)
	content := lipgloss.JoinVertical(lipgloss.Left,
		t.CyanBold.Render("Keyboard shortcuts"), "", m.helpViewport.View(), "",
		components.RenderBoundedHints(t, []components.KeyDesc{{Key: "↑↓/pgup/pgdn", Desc: "scroll"}, {Key: "esc/?", Desc: "close"}}, w, components.KeyDesc{Key: "esc/?", Desc: "close"}))
	return t.Modal.Padding(1, 2).Width(w + 6).Render(content)
}

func renderHelpGroup(t *theme.Theme, g keys.Group) string {
	keyW := widestKey(g)
	title, keyStyle := t.CyanBold, t.HelpKey
	if g.Writes {
		title, keyStyle = t.Danger, t.Danger
	}
	lines := make([]string, 0, 2+len(g.Pairs)+len(g.Bindings))
	lines = append(lines,
		title.Render(g.Title),
		t.DimTxt.Render(strings.Repeat("─", 8)),
	)
	for _, p := range g.Pairs {
		lines = append(lines, helpRow(t, keyStyle, keyW, p.Key, p.Desc))
	}
	for _, b := range g.Bindings {
		h := b.Help()
		lines = append(lines, helpRow(t, keyStyle, keyW, h.Key, h.Desc))
	}
	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

func widestKey(g keys.Group) int {
	w := 4
	measure := func(s string) {
		if n := lipgloss.Width(s); n > w {
			w = n
		}
	}
	for _, p := range g.Pairs {
		measure(p.Key)
	}
	for _, b := range g.Bindings {
		measure(b.Help().Key)
	}
	return w
}

func helpRow(t *theme.Theme, keyStyle lipgloss.Style, keyW int, k, desc string) string {
	return keyStyle.Width(keyW+2).Render(k) + t.HelpDesc.Render(desc)
}
