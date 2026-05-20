package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/yunus/safina/internal/k8s"
	"github.com/yunus/safina/internal/tui/components"
	"github.com/yunus/safina/internal/tui/keys"
	"github.com/yunus/safina/internal/tui/sections"
	"github.com/yunus/safina/internal/tui/theme"
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
		Content:     content,
		AltScreen:   true,
		MouseMode:   tea.MouseModeCellMotion,
		ReportFocus: true,
		WindowTitle: m.title,
		KeyboardEnhancements: tea.KeyboardEnhancements{
			ReportAlternateKeys: true,
		},
	}
	// Hardware cursor is shown only on the Chat tab while it's active.
	// Every other surface has its caret implied by the row highlight.
	if m.state == stateChatView {
		v.Cursor = m.chat.Cursor()
	}
	return v
}

func (m *Model) render() string {
	w, h := m.ctx.Width, m.ctx.Height
	if w < 50 || h < 14 {
		return m.ctx.Theme.DimPad.Render("terminal too small")
	}

	// Shell takes the full screen — it owns a pty session, so the tree /
	// workspace composition is meaningless during a shell session.
	if m.state == stateShellView {
		return m.shell.View(m.ctx, w, h)
	}

	base := m.renderBase(w, h)

	// Overlays compose ON TOP of the base — the tree and workspace stay
	// visible underneath a dimmed backdrop. This is the cosmic-horizon
	// design choice: nothing swallows the screen except shells. Confirms,
	// pickers, palette, help, cluster switcher all float.
	switch m.state {
	case stateHelp:
		return m.composeCard(base, m.renderHelpModal())
	case statePalette:
		return m.composeOverlay(base, m.palette.View(m.ctx, w, h))
	case stateClusters:
		return m.composeOverlay(base, m.clusters.View(m.ctx, w, h, m.ctx.ActiveContext))
	case stateBrowser:
		return m.composeOverlay(base, m.browser.View(m.ctx, w, h))
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

// composeOverlay layers an overlay (palette / browser / cluster switcher)
// over the base via lipgloss.Compositor. Each overlay is a full-screen
// Place-positioned string today (its own dotted backdrop), so layering it
// at z=1 over base at z=0 is a faithful preservation of the current look
// — the value is the z-buffered architecture, which lets future overlays
// drop the backdrop and have the live tree show through underneath.
//
// The render side-effects of base (sizing, selection sync, tab hitbox
// stashing) still run because base is always rendered; we now also draw
// it as the bottom layer instead of throwing it away.
func (m *Model) composeOverlay(base, overlay string) string {
	w, h := m.ctx.Width, m.ctx.Height
	comp := lipgloss.NewCompositor(
		lipgloss.NewLayer(base).X(0).Y(0).Z(0),
		lipgloss.NewLayer(overlay).X(0).Y(0).Z(1),
	)
	return lipgloss.NewCanvas(w, h).Compose(comp).Render()
}

// composeCard layers a self-contained card (a small confirm / scale /
// help / edit / port-forward modal that doesn't carry its own backdrop)
// centred over the base, with the standard dim-dot backdrop drawn as the
// middle layer. The card layer reads "floating over data" instead of
// "data swapped for a static fullscreen frame" — proper z-ordering with
// lipgloss.Compositor.
//
// Centring math: x = (w - cardW) / 2, y = (h - cardH) / 2 — same result
// lipgloss.Place computes, but exposed as a Layer so the card paints
// only its own bounds (no fullscreen overwrite of base).
func (m *Model) composeCard(base, card string) string {
	w, h := m.ctx.Width, m.ctx.Height
	cardW, cardH := lipgloss.Size(card)
	cx := (w - cardW) / 2
	if cx < 0 {
		cx = 0
	}
	cy := (h - cardH) / 2
	if cy < 0 {
		cy = 0
	}
	// Backdrop layer: a full-screen field of dim dots that paints over the
	// base so the modal reads as gated ("don't keep typing into the tree").
	// Same visual signature as the lipgloss.Place backdrop the old path
	// produced; rendered explicitly here so the layer order is obvious.
	backdrop := m.renderModalBackdrop(w, h)
	comp := lipgloss.NewCompositor(
		lipgloss.NewLayer(base).X(0).Y(0).Z(0),
		lipgloss.NewLayer(backdrop).X(0).Y(0).Z(1),
		lipgloss.NewLayer(card).X(cx).Y(cy).Z(2),
	)
	return lipgloss.NewCanvas(w, h).Compose(comp).Render()
}

// renderModalBackdrop paints a w×h field of dim dots used as the
// translucent-feeling backdrop layer under a modal card. Same dots and
// dim-foreground style the lipgloss.Place backdrop used to draw via
// WithWhitespaceChars — extracted so the Compositor path can layer it
// explicitly.
func (m *Model) renderModalBackdrop(w, h int) string {
	t := m.ctx.Theme
	dot := lipgloss.NewStyle().Foreground(t.P.BG3).Render(strings.Repeat("·", w))
	rows := make([]string, h)
	for i := range rows {
		rows[i] = dot
	}
	return strings.Join(rows, "\n")
}

// renderBase composes the steady-state frame: topbar / optional banner /
// main split / footer. The chat drawer is part of the main split when open.
func (m *Model) renderBase(w, h int) string {
	parts := []string{}
	used := 1 // footer (reserved)

	// Topbar: brand + breadcrumb (with brightness ramp) + clock / refresh /
	// health on the right. No mode capsule — the focused border already
	// answers "what surface owns my keystrokes".
	fwdCount := 0
	if m.ctx.Manager != nil && m.ctx.Manager.Forwards != nil {
		fwdCount = m.ctx.Manager.Forwards.Count()
	}
	parts = append(parts, components.Topbar(
		m.ctx.Theme, w,
		m.tree.BreadcrumbCrumbs(),
		m.refreshAge(),
		m.now.Format("15:04:05"),
		fwdCount,
		m.topbarClusterHealth(),
	))
	used++

	if m.bannerMsg != "" {
		parts = append(parts, m.renderBanner(w))
		used++
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
func (m *Model) renderMainSplit(w, h int) string {
	const treeMin = 28
	treeW, wsW := theme.PhiSplit(w, 0, treeMin)
	if wsW < 32 {
		wsW = 32
		treeW = w - wsW
		if treeW < treeMin {
			treeW = treeMin
		}
	}

	workspaceFocused := m.workspaceOwnsFocus()
	// Only one pane wears the gradient border. Sync the tree's Focused field
	// so its View() draws a flat dim border when the workspace owns focus.
	// Restored on return so update.go's state transitions remain authoritative.
	prevTreeFocused := m.tree.Focused
	m.tree.Focused = !workspaceFocused
	defer func() { m.tree.Focused = prevTreeFocused }()

	// Chat lives inside the workspace tab; record its screen origin so the
	// hardware cursor lands at the right pixel when the user types.
	originX := treeW
	originY := 1
	if m.bannerMsg != "" {
		originY = 2
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
	m.tabHitboxes = sections.WorkspaceTabHitboxes(tabs, sections.WorkspaceBodyWidth(wsW))

	treeView := m.tree.View(m.ctx, treeW, h)
	workspaceView := m.renderWorkspace(wsW, h, workspaceFocused)
	return lipgloss.JoinHorizontal(lipgloss.Top, treeView, workspaceView)
}

// renderWorkspace builds the right-hand workspace pane: a tab strip + the
// active tab's body, wrapped in a single panel that takes the focus
// gradient when active. The tab strip lives in the panel's first content
// row (rather than the title bar) so per-tab styling survives.
func (m *Model) renderWorkspace(w, h int, focused bool) string {
	t := m.ctx.Theme
	active := m.activeWorkspaceTab()
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
func (m *Model) workspaceOwnsFocus() bool {
	if m.peekWorkspace {
		return true
	}
	switch m.state {
	case stateLogView, stateEventsView, stateDescribeView,
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
	switch m.state {
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
	av := sections.WSTabAvailability{}
	switch ref.Kind {
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
	// Pinned tabs: appear only while their state is active. The user can
	// switch back to Inspector by pressing 1 (or Esc on the tab).
	switch m.state {
	case stateHotView:
		av.Hot = true
	case stateForwardsList:
		av.Forwards = true
	case stateDrainProgress:
		av.Drain = true
	default:
		// No pinned tab for non-immersive states.
	}
	return av
}

// workspaceTitle is the panel title — a contextual one-liner anchored to
// the active tab. Inspector shows the breadcrumb leaf; Logs/Events/Yaml
// show their own section title; pinned tabs show the operation context.
func (m *Model) workspaceTitle(tab sections.WorkspaceTab) string {
	switch tab {
	case sections.WSTabLogs:
		return m.logs.PaneTitle()
	case sections.WSTabEvents:
		return m.events.PaneTitle()
	case sections.WSTabYaml:
		return m.describe.PaneTitle()
	case sections.WSTabChat:
		return m.chat.PaneTitle()
	case sections.WSTabHot:
		return m.hot.PaneTitle()
	case sections.WSTabForwards:
		return m.forwards.PaneTitle()
	case sections.WSTabDrain:
		return m.drain.PaneTitle()
	case sections.WSTabInspector:
		// Falls through to the breadcrumb path below.
	default:
	}
	// Inspector: leaf of the breadcrumb (the operator's current focus).
	crumbs := m.tree.BreadcrumbCrumbs()
	if len(crumbs) > 0 {
		return crumbs[len(crumbs)-1].Label
	}
	return "workspace"
}

// renderTabBody dispatches to the section that owns the active tab's
// content. Width and height are the workspace pane's inner content area
// (the panel border + tab strip + hairline are already subtracted by
// WorkspaceBodyWidth/Height).
func (m *Model) renderTabBody(tab sections.WorkspaceTab, w, h int) string {
	t := m.ctx.Theme
	switch tab {
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
		return m.forwards.Body(w)
	case sections.WSTabDrain:
		return m.drain.Body(w)
	case sections.WSTabInspector:
		// Falls through to the Inspector default below.
	default:
	}
	// Default: Inspector tab — selection summary stack.
	if m.ctx.ActiveContext == "" {
		// At the cluster picker no selection exists yet; show a calm
		// orientation block instead of an empty Inspector.
		return sections.IdlePlaceholder(t, w, h, "pick a cluster on the left to begin")
	}
	return sections.RenderInspectorTab(m.ctx, w, h, &m.inspector, &m.activity, &m.connected)
}

// ── banner / topbar helpers ───────────────────────────────────────────

// renderBanner draws the transient status/error line above the main split.
// Errors render in red (✗), successful action confirmations in green (✓).
func (m *Model) renderBanner(w int) string {
	t := m.ctx.Theme
	if m.bannerErr {
		return t.ErrLine.Width(w).Render("  ✗  " + m.bannerMsg)
	}
	return t.OkLine.Width(w).Render("  ✓  " + m.bannerMsg)
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

// refreshAge returns the "Ns" / "Nm" age of the last successful tree load,
// or "" when there's nothing live to display (we're at the picker, or the
// first load hasn't landed yet). The topbar uses this for the "↻ Ns" pill.
func (m *Model) refreshAge() string {
	if m.lastRefresh.IsZero() {
		return ""
	}
	d := m.now.Sub(m.lastRefresh)
	if d < 0 {
		d = 0
	}
	secs := int(d.Seconds())
	if secs < 60 {
		return fmt.Sprintf("%ds", secs)
	}
	mins := secs / 60
	if mins < 60 {
		return fmt.Sprintf("%dm", mins)
	}
	return fmt.Sprintf("%dh", mins/60)
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
	var view string
	switch m.state {
	case stateChatView:
		view = m.renderChatFooter()
	case stateLogView, stateEventsView, stateDescribeView,
		stateHotView, stateForwardsList:
		view = m.renderImmersiveFooter()
	case stateDrainProgress:
		view = m.renderDrainFooter()
	default:
		view = m.renderTreeFooter()
	}
	return lipgloss.NewStyle().
		Background(t.P.BG2).
		Width(m.ctx.Width).
		MaxHeight(1).
		Padding(0, 1).
		Render(view)
}

// renderTreeFooter renders contextual short help with two visual layers:
// action verbs are bright; chrome keys (refresh/filter/help/quit) are dim
// so global affordances read as a separate visual register.
func (m *Model) renderTreeFooter() string {
	t := m.ctx.Theme
	bindings := m.contextualShortHelp()
	chromeKeys := map[string]bool{
		m.ctx.Keys.Refresh.Help().Key: true,
		m.ctx.Keys.Filter.Help().Key:  true,
		m.ctx.Keys.Help.Help().Key:    true,
		m.ctx.Keys.Quit.Help().Key:    true,
		m.ctx.Keys.Enter.Help().Key:   true,
	}
	dim := lipgloss.NewStyle().Foreground(t.P.Dim)
	parts := make([]string, 0, len(bindings))
	for _, b := range bindings {
		h := b.Help()
		if chromeKeys[h.Key] {
			parts = append(parts, dim.Render(h.Key)+" "+dim.Render(h.Desc))
		} else {
			parts = append(parts, t.HelpKey.Render(h.Key)+" "+t.HelpDesc.Render(h.Desc))
		}
	}
	sep := t.DimTxt.Render(" · ")
	return strings.Join(parts, sep)
}

// renderChatFooter mirrors contextualShortHelp's role for chat.
func (m *Model) renderChatFooter() string {
	t := m.ctx.Theme
	pair := func(k, d string) string {
		return t.HelpKey.Render(k) + " " + t.HelpDesc.Render(d)
	}
	sep := t.HelpDesc.Render(" · ")
	send := pair("↵", "send")
	if m.chat.Streaming() {
		send = pair("esc", "cancel")
	}
	parts := []string{
		send,
		pair("⇧↵", "newline"),
		pair("tab", "tree"),
		pair("⌃l", "clear"),
		pair("⌃n", "new"),
	}
	return strings.Join(parts, sep)
}

// renderImmersiveFooter renders just the global chrome row for any
// workspace-tab state. Each immersive section (logs / events / describe /
// hot / forwards) already paints a verb-hint row inside its own body, so
// the footer's job is reduced to the universal "you can always leave / get
// help / quit" affordances. The two-register rule from renderTreeFooter
// still applies — the chrome stays dim so it reads as ambient.
func (m *Model) renderImmersiveFooter() string {
	t := m.ctx.Theme
	dim := lipgloss.NewStyle().Foreground(t.P.Dim)
	pair := func(k, d string) string {
		return dim.Render(k) + " " + dim.Render(d)
	}
	sep := dim.Render(" · ")
	return strings.Join([]string{
		pair("esc", "back"),
		pair("?", "help"),
		pair("q", "quit"),
	}, sep)
}

// renderDrainFooter omits esc/help/quit — esc is intentionally a no-op
// while a drain is in flight (the operator shouldn't be able to half-drain
// a node by accident), and the only useful key is ctrl+c to cancel. The
// body already labels the cancel chord; the footer reads as a dim echo so
// the row isn't blank.
func (m *Model) renderDrainFooter() string {
	t := m.ctx.Theme
	dim := lipgloss.NewStyle().Foreground(t.P.Dim)
	return dim.Render("⌃c") + " " + dim.Render("cancel")
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
	tail := []key.Binding{k.Filter, k.Help, k.Quit}
	if m.tree.Focus().Kind != k8s.KindContextList {
		tail = append([]key.Binding{k.Refresh}, tail...)
	}

	ref := m.tree.SelectedRef()
	reads := []key.Binding{}
	mutators := []key.Binding{}
	switch ref.Kind {
	case k8s.KindContext:
		// Cluster picker: the inline "↵ open" hint in the tree body already
		// teaches the drill verb. Don't restate Enter here — Jakob's rule.
		return tail
	case k8s.KindApp:
		reads = append(reads, k.Describe, k.Logs, k.Events)
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
		reads = append(reads, k.Describe, k.Logs, k.Events, k.Shell, k.PortForward)
		mutators = append(mutators, k.Restart)
	case k8s.KindContainer:
		reads = append(reads, k.Describe, k.Logs, k.Events, k.Shell)
	case k8s.KindNode:
		reads = append(reads, k.Describe, k.Events)
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
	t := m.ctx.Theme
	deploy := m.deployName()
	subtitle := ""
	if app, ok := m.tree.SelectedAppInfo(); ok {
		if cur := currentDesiredReplicas(app); cur >= 0 {
			subtitle = fmt.Sprintf("now: %d replicas", cur)
		}
	}
	parts := []string{t.BlueBold.Render("scale " + deploy)}
	if subtitle != "" {
		parts = append(parts, t.DimTxt.Render(subtitle))
	}
	parts = append(parts,
		"",
		m.scaleInput.View(),
		"",
		components.RenderHintRow(t, []components.KeyDesc{
			{Key: "↵", Desc: "confirm"},
			{Key: "esc", Desc: "cancel"},
		}),
	)
	content := lipgloss.JoinVertical(lipgloss.Left, parts...)
	return t.Modal.Render(content)
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
	t := m.ctx.Theme
	subject := strings.ToLower(m.pfTarget.Kind) + " " + m.pfTarget.Name
	content := lipgloss.JoinVertical(lipgloss.Left,
		t.BlueBold.Render("port-forward "+subject),
		"",
		m.pfInput.View(),
		"",
		t.Hint.Render("L:R or single port (same on both sides)"),
		components.RenderHintRow(t, []components.KeyDesc{
			{Key: "↵", Desc: "confirm"},
			{Key: "esc", Desc: "cancel"},
		}),
	)
	return t.Modal.Render(content)
}

func (m *Model) renderEditConfirmModal() string {
	t := m.ctx.Theme
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

	yellow := lipgloss.NewStyle().Foreground(t.P.Yellow).Bold(true)
	red := lipgloss.NewStyle().Foreground(t.P.Red).Bold(true)
	parts := []string{
		yellow.Render(subject),
		"",
		red.Render(resource),
		"",
		stat,
	}
	if body != "" {
		parts = append(parts, "", body)
	}
	parts = append(parts, "", components.RenderConfirmHint(t, "apply"))

	content := lipgloss.JoinVertical(lipgloss.Left, parts...)
	return t.Modal.Render(content)
}

func (m *Model) renderConfirmModal(action, resource string) string {
	t := m.ctx.Theme
	yellow := lipgloss.NewStyle().Foreground(t.P.Yellow).Bold(true)
	red := lipgloss.NewStyle().Foreground(t.P.Red).Bold(true)
	content := lipgloss.JoinVertical(lipgloss.Left,
		yellow.Render(action),
		"",
		red.Render(resource),
		"",
		components.RenderConfirmHint(t, "confirm"),
	)
	return t.Modal.Render(content)
}

func (m *Model) renderHelpModal() string {
	t := m.ctx.Theme
	groups := m.ctx.Keys.Groups()
	gutter := lipgloss.NewStyle().PaddingRight(4)
	cols := make([]string, 0, len(groups))
	for i, g := range groups {
		c := renderHelpGroup(t, g)
		if i < len(groups)-1 {
			c = gutter.Render(c)
		}
		cols = append(cols, c)
	}
	body := lipgloss.JoinHorizontal(lipgloss.Top, cols...)
	bodyW := lipgloss.Width(body)
	content := lipgloss.JoinVertical(lipgloss.Left,
		t.Divider("keybindings", bodyW),
		"",
		body,
		"",
		components.RenderHintRow(t, []components.KeyDesc{
			{Key: "esc", Desc: "close"},
			{Key: "?", Desc: "close"},
		}),
	)
	return t.Modal.Padding(1, 2).Render(content)
}

func renderHelpGroup(t *theme.Theme, g keys.Group) string {
	keyW := widestKey(g)
	lines := make([]string, 0, 2+len(g.Pairs)+len(g.Bindings))
	lines = append(lines,
		t.CyanBold.Render(g.Title),
		t.DimTxt.Render(strings.Repeat("─", 8)),
	)
	for _, p := range g.Pairs {
		lines = append(lines, helpRow(t, keyW, p.Key, p.Desc))
	}
	for _, b := range g.Bindings {
		h := b.Help()
		lines = append(lines, helpRow(t, keyW, h.Key, h.Desc))
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

func helpRow(t *theme.Theme, keyW int, k, desc string) string {
	return t.HelpKey.Width(keyW).Render(k) + t.HelpDesc.Render(desc)
}
