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
	"github.com/YunusAndreasson/safina/internal/tui/textw"
	"github.com/YunusAndreasson/safina/internal/tui/theme"
	"github.com/YunusAndreasson/safina/internal/version"
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
	m.spinnerShown = m.ctx.TakeSpinnerDrawn()
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
	// This feature applies only to the states listed; all others use the fallback.
	//exhaustive:ignore
	switch m.state {
	case stateShellView:
		v.Cursor = m.shell.Cursor(m.ctx.Width, m.ctx.Height)
	case stateChatView:
		v.Cursor = m.chat.Cursor()
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
	m.settlePage()
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

	// Overlays compose ON TOP of the page, which stays visible underneath
	// a dimmed backdrop: nothing swallows the screen except shells.
	// Confirms, pickers, palette, help and the cluster switcher all float.
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

	// With room, the full header leads: which cluster and as whom, how it
	// is doing, the page's keys and the mark. The location row under it (or
	// the one-row topbar on a small screen) says where the operator is and
	// how this thing is doing, and carries transient feedback, so the page
	// never jumps when it comes and goes.
	top := m.topbarInput()
	if m.fullHeader() {
		// Under the full header the page sits in a frame. The location is
		// its title, on its own line above it; a resource page's tabs are
		// the frame's top edge and the footer is its bottom edge, so the
		// page reads as one pane under the masthead.
		t := m.ctx.Theme
		top.Brandless = true
		top.Health = components.ClusterHealth{}
		inset := strings.Repeat(" ", components.HeaderMargin-1)
		parts = append(parts,
			components.PageHeader(t, w, m.headerInput()),
			inset+components.Topbar(t, w-2*len(inset), top)+inset)
		used += components.HeaderRows + 1
		if m.hasBanner() {
			parts = append(parts, inset+m.renderBanner(w-2*len(inset))+inset)
			used++
		}
		// The page's first row is its tab rule on a resource page; it
		// becomes the frame's top edge. Elsewhere the edge is a plain rule.
		edgeIsRule := m.pageHasTabRule()
		pageH := max(8, h-used) // the top edge and the page under it
		m.frameTopInset = 0
		if !edgeIsRule {
			m.frameTopInset = 1
			pageH--
		}
		page := strings.Split(m.renderPage(w-2, pageH), "\n")
		edge := components.FrameEdge(t, "", w, true)
		if edgeIsRule && len(page) > 0 {
			edge = lipgloss.NewStyle().Foreground(t.P.Dim).Render("╭─") + ansi.Cut(page[0], 1, w-3) + lipgloss.NewStyle().Foreground(t.P.Dim).Render("─╮")
			page = page[1:]
		}
		if m.modalOpen() {
			page = strings.Split(m.overlayModal(strings.Join(page, "\n"), w-2, len(page)), "\n")
		}
		return textw.JoinVertical(lipgloss.Left, append(parts,
			edge,
			components.FrameSides(t, strings.Join(page, "\n")),
			components.FrameEdge(t, m.renderFooter(), w, false))...)
	}
	parts = append(parts, components.Topbar(m.ctx.Theme, w, top))
	used++
	if m.hasBanner() {
		parts = append(parts, m.renderBanner(w))
		used++
	}

	botH := h - used
	if botH < 8 {
		botH = 8
	}

	page := m.renderPage(w, botH)
	if m.modalOpen() {
		page = m.overlayModal(page, w, botH)
	}
	parts = append(parts, page, m.renderFooter())
	return textw.JoinVertical(lipgloss.Left, parts...)
}

// renderPage draws the one page on screen into w × h cells: the Harbour,
// a list, a resource page or a tool page. There is no split and no focus
// border; the page owns every key.
func (m *Model) renderPage(w, h int) string {
	m.connected.Hide()
	m.tabHitboxes = nil
	m.tree.Focused = true
	m.inspector.HeaderOutside = true
	// Body origin (top-left of the page) for the mouse router: below the
	// topbar and the error banner, if one is up.
	m.bodyOriginX, m.bodyOriginY = 0, 1
	if m.fullHeader() {
		m.bodyOriginX, m.bodyOriginY = 1, 1+components.HeaderRows+m.frameTopInset
	}
	if m.hasBanner() {
		m.bodyOriginY++
	}
	base := m.baseState()
	_, reading := stateReadingTab(base)
	reading = reading && !m.modal // a view in the modal floats over the page
	switch {
	case isToolState(base):
		return m.renderToolPage(base, w, h)
	case m.tree.Focus().Kind == k8s.KindContextList:
		return m.renderHarbour(w, h)
	case reading:
		// A reading view always belongs to a resource page; this only
		// covers a view opened before its page finished arriving.
		return components.PageFrame(m.ctx.Theme, m.pageTab.Title(""), w, h,
			m.renderTabBody(m.pageTab, components.PanelContentWidth(w), h-1), m.ctx.Theme.P.FGFade, false)
	case m.fullHeader():
		// The location row above names the list; one title per page.
		return m.tree.Body(m.ctx, w, h)
	default:
		return m.tree.View(m.ctx, w, h)
	}
}

// pageHasTabRule reports whether the page renderPage draws opens with its
// tab rule: a resource page, not a tool page or the Harbour.
func (m *Model) pageHasTabRule() bool {
	return false // pages are lists; their views open as modals
}

// renderHarbour is the cluster picker: the sail, the name and the tagline
// above the list of clusters.
func (m *Model) renderHarbour(w, h int) string {
	listW := sections.HarbourListWidth(max(1, w-2))
	rows := m.tree.RowCount() + 3 // the prompt, column headings and a spare
	if m.tree.FilterActive() {
		rows++
	}
	list := m.tree.Content(m.ctx, listW, min(h, max(2, rows)))
	return sections.Harbour(m.ctx.Theme, w, h, list)
}

// tabState is the open tab's own state for the right end of its rule:
// the stream, the count, the window. Empty when the tab has none.
func (m *Model) tabState(tab sections.WorkspaceTab) string {
	var ind []components.Indicator
	//exhaustive:ignore
	switch tab {
	case sections.WSTabChildren:
		if n := m.tree.RowCount(); n > 0 && !m.tree.Loading {
			ind = []components.Indicator{{Text: fmt.Sprintf("%d %s", n, strings.ToLower(tab.Title(m.tree.Focus().Kind)))}}
		}
	case sections.WSTabLogs:
		ind = m.logs.Indicators(m.ctx)
	case sections.WSTabEvents:
		ind = m.events.Indicators()
	case sections.WSTabYaml:
		ind = m.describe.Indicators()
	case sections.WSTabMetrics:
		if sections.MetricsSupported(m.metrics.Ref) {
			ind = m.metrics.Indicators(time.Now())
		}
	}
	return components.RenderIndicators(m.ctx.Theme, ind)
}

// tabHints are the open tab's own keys, for the footer.
func (m *Model) tabHints() []components.KeyDesc {
	// The viewers page with ctrl+f/b, as in less; the key is listed so it
	// can be found.
	page := components.KeyDesc{Key: "ctrl+f/b", Desc: "page"}
	//exhaustive:ignore
	switch m.state {
	case stateLogView:
		return append(m.logs.Hints(), page)
	case stateEventsView:
		return append(m.events.Hints(), page)
	case stateDescribeView:
		return append(m.describe.Hints(), page)
	case stateMetricsView:
		return m.metrics.Hints()
	case stateHotView:
		return []components.KeyDesc{{Key: m.ctx.Keys.Refresh.Help().Key, Desc: "refresh"}}
	}
	return nil
}

// renderToolPage draws chat, hot pods, forwards or drain over the page it
// was opened from.
func (m *Model) renderToolPage(s state, w, h int) string {
	t := m.ctx.Theme
	innerW, innerH := components.PanelContentWidth(w), max(1, h-1)
	var title, body string
	//exhaustive:ignore
	switch s {
	case stateChatView:
		title = "Chat · " + pageName(m.chatSubject())
		m.chat.SetScreenOrigin(m.bodyOriginX+1, m.bodyOriginY+1)
		body = m.chat.Body(m.ctx, innerW, innerH)
	case stateHotView:
		title = "Hot pods"
		body = m.hot.Body(m.ctx, innerW, innerH)
	case stateForwardsList:
		title = "Port forwards"
		body = m.forwards.Body(innerW, innerH)
	case stateDrainProgress:
		title = m.drain.PaneTitle()
		body = m.drain.Body(innerW)
	}
	return components.PageFrame(t, title, w, h, body, t.P.FGFade, false)
}

// chatSubject is what the chat is about: the page's resource, or the
// highlighted row on a list.
func (m *Model) chatSubject() sections.NodeRef {
	if ref := m.tree.SelectedRef(); ref.Kind != "" {
		return ref
	}
	return m.tree.Focus()
}

// renderTabBody dispatches to the section that owns a tab's content, in
// w × h cells.
func (m *Model) renderTabBody(tab sections.WorkspaceTab, w, h int) string {
	t := m.ctx.Theme
	//exhaustive:ignore
	switch tab {
	case sections.WSTabChildren:
		return m.tree.Content(m.ctx, w, h)
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
	default:
		return sections.IdlePlaceholder(t, w, h, "Nothing to show here yet.")
	}
}

// ── banner / topbar helpers ───────────────────────────────────────────

// renderBanner draws the one row an error earns above the page: the history
// store's health warning, or a failed action with its dismiss keys at the
// right edge. Confirmations and operations in flight go to the topbar.
func (m *Model) renderBanner(w int) string {
	t := m.ctx.Theme
	if warning := m.ctx.Manager.History.Health(); warning != "" {
		return t.ErrLine.Width(w).Render(ansi.Truncate("✗  "+warning, max(1, w-2), "…"))
	}
	text := m.bannerMsg
	if m.bannerContext != "" && m.bannerContext != m.ctx.ActiveContext {
		text = m.bannerContext + ": " + text
	}
	text = "✗  " + strings.ReplaceAll(text, "\n", " ")
	hints := components.RenderModalKeysRow(t, []components.KeyDesc{{Key: "ctrl+d", Desc: "dismiss"}, {Key: "ctrl+n", Desc: "notifications"}})
	// ErrLine pads a cell on each side; the row's text has w-2 cells.
	inner := max(1, w-2)
	if hw := textw.Width(hints); hw+20 <= inner {
		text = ansi.Truncate(text, inner-hw-3, "…")
		text += strings.Repeat(" ", max(1, inner-hw-textw.Width(text))) + hints
	} else {
		text = ansi.Truncate(text, inner, "…")
	}
	return t.ErrLine.Width(w).MaxHeight(1).Render(text)
}

// fullHeader reports whether the screen has room for the full header: a
// cluster is open and the terminal is at least MinHeaderWidth ×
// MinHeaderHeight. Otherwise the one-row topbar carries the essentials.
func (m *Model) fullHeader() bool {
	return m.ctx.ActiveContext != "" && m.tree.Focus().Kind != k8s.KindContextList &&
		m.ctx.Width >= components.MinHeaderWidth && m.ctx.Height >= components.MinHeaderHeight
}

// headerInput gathers the full header: the cluster's identity from the
// kubeconfig, its version and health, and the keys that work on this page.
func (m *Model) headerInput() components.HeaderInput {
	in := components.HeaderInput{
		Context:       m.ctx.ActiveContext,
		ServerVersion: m.serverVersion,
		Health:        m.topbarClusterHealth(),
		Keys:          m.headerKeys(),
		AppVersion:    version.Version,
	}
	for _, c := range m.allContexts {
		if c.Name == m.ctx.ActiveContext {
			in.Cluster, in.User = c.Cluster, c.User
			break
		}
	}
	return in
}

// headerKeys are the keys that work on this page, for the full header:
// moving between tabs and the open tab's own keys on a reading view, the
// highlighted row's reads on a list. Changes stay behind Shift in the
// footer, and search and help stay at the footer's right edge.
func (m *Model) headerKeys() []components.KeyDesc {
	if m.state == stateChatView || m.state == stateDrainProgress {
		return nil
	}
	var keys []components.KeyDesc
	if m.state == stateMain && m.detailsLinks() {
		keys = append(keys, components.KeyDesc{Key: "↑/↓", Desc: "links"}, components.KeyDesc{Key: "↵", Desc: "open link"})
	}
	if m.modal && m.isModalState(m.state) {
		return append(m.modalKeys(), components.KeyDesc{Key: "esc", Desc: "close"})
	}
	if m.isImmersiveState() {
		return append(keys, m.tabHints()...)
	}
	for _, binding := range m.contextualShortHelp() {
		h := binding.Help()
		if h.Key == m.ctx.Keys.Help.Help().Key || h.Key == m.ctx.Keys.Search.Help().Key || m.ctx.Keys.IsWrite(h.Key) {
			continue
		}
		keys = append(keys, components.KeyDesc{Key: h.Key, Desc: h.Desc})
	}
	return keys
}

// topbarInput gathers what the topbar shows: the breadcrumb, the page's
// status on a resource page, transient feedback, what waits, how fresh the
// data is, and the cluster's health.
func (m *Model) topbarInput() components.TopbarInput {
	t := m.ctx.Theme
	in := components.TopbarInput{
		Crumbs:    m.tree.BreadcrumbCrumbs(),
		Proposals: m.incoming.inbox.Pending,
		Freshness: m.freshness(),
		Health:    m.topbarClusterHealth(),
	}
	if m.ctx.Manager != nil && m.ctx.Manager.Forwards != nil {
		in.Forwards = m.ctx.Manager.Forwards.Count()
	}
	if m.tree.OnResourcePage() {
		in.Status, in.StatusShort = m.inspector.Status(m.ctx)
	}
	switch {
	case m.bannerMsg != "" && !m.bannerErr:
		in.Notice = lipgloss.NewStyle().Foreground(t.P.Green).Bold(true).Render("✓ " + m.bannerMsg)
	case m.operationSummary() != "":
		in.Notice = t.Hint.Render("◌ " + m.operationSummary())
	case m.notice != "":
		in.Notice = lipgloss.NewStyle().Foreground(t.P.Yellow).Render("· " + m.notice)
	}
	return in
}

// freshness names how old the data on screen is when it is no longer live:
// paused while the terminal is in the background, the age once a refresh
// is well overdue (twice the 5s cadence, so a slow refresh does not
// flicker), or cached for the overview preview.
func (m *Model) freshness() string {
	switch {
	case m.overviewPreview:
		return "cached"
	case m.ctx.ActiveContext == "":
		return ""
	case !m.focused:
		return "paused"
	case !m.lastRefresh.IsZero() && m.now.Sub(m.lastRefresh) >= 10*time.Second:
		return "↻ " + m.now.Sub(m.lastRefresh).Truncate(time.Second).String()
	}
	return ""
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
	if (m.state == stateMain && m.tree.FilterActive()) ||
		(m.state != stateChatView && m.sectionHasFilterFocus()) {
		return t.HelpDesc.Render(" type to filter · ") + components.RenderBoundedHints(t, []components.KeyDesc{{Key: "↵", Desc: "apply"}, {Key: "esc", Desc: "cancel"}}, max(0, m.ctx.Width-20), components.KeyDesc{Key: "esc", Desc: "cancel"})
	}
	var view string
	// This feature applies only to the states listed; all others use the fallback.
	//exhaustive:ignore
	switch {
	case m.state == stateDrainProgress:
		view = m.renderDrainFooter()
	case m.state == stateChatView:
		view = m.renderChatFooter()
	case m.state == stateMain && m.detailsLinks() && !m.fullHeader():
		view = m.withBack(components.RenderBoundedHints(t, []components.KeyDesc{{Key: "↑/↓", Desc: "links"}, {Key: "↵", Desc: "open link"}}, m.footerBudget(), components.KeyDesc{Key: "?", Desc: "help"}))
	case m.isImmersiveState() && m.fullHeader():
		view = m.quietFooter()
	case m.isImmersiveState():
		view = m.withBack(m.renderImmersiveFooter())
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

// backHint is the footer's first hint: where Esc goes, by name. It is read
// from the same rules Esc follows, so it is always the truth.
func (m *Model) backHint() string {
	if m.modalOpen() {
		t := m.ctx.Theme
		return t.HelpKey.Render("esc") + " " + t.HelpDesc.Render("close") + t.DimTxt.Render("   ")
	}
	label := m.backLabel()
	if label == "" {
		return ""
	}
	t := m.ctx.Theme
	label = ansi.Truncate(label, 24, "…")
	return t.HelpKey.Render("esc") + " " + t.HelpDesc.Render("‹ "+label) + t.DimTxt.Render("   ")
}

// footerBudget is the width left for hints after the back hint.
func (m *Model) footerBudget() int {
	return max(0, m.ctx.Width-2-textw.Width(m.backHint()))
}

// withBack prefixes a row of hints with the back hint.
func (m *Model) withBack(hints string) string {
	return m.backHint() + hints
}

func (m *Model) renderChatFooter() string {
	action := components.KeyDesc{Key: "↵", Desc: "send"}
	if m.chat.Streaming() {
		action = components.KeyDesc{Key: "esc", Desc: "cancel"}
	}
	hints := []components.KeyDesc{action, {Key: "shift+↵", Desc: "newline"}, {Key: "ctrl+l", Desc: "clear"}, {Key: "ctrl+n", Desc: "new"}}
	if !m.chat.Streaming() {
		hints = append(hints, components.KeyDesc{Key: "esc", Desc: "close"})
	}
	return components.RenderBoundedHints(m.ctx.Theme, hints, m.ctx.Width-2, components.KeyDesc{})
}

// detailsLinks reports whether the page is on Details with Connected links
// to walk, where ↑/↓ and ↵ move through the links.
func (m *Model) detailsLinks() bool {
	return m.tree.PageSubject && m.pageTab == sections.WSTabDetails && m.connected.Count() > 0
}

// quietFooter is the footer under the full header, the same on every page:
// the way back on the left, search and commands on the right. The page's
// own keys are in the header.
func (m *Model) quietFooter() string {
	t := m.ctx.Theme
	back := m.backHint()
	right := components.RenderModalKeysRow(t, []components.KeyDesc{{Key: "ctrl+p", Desc: "search"}, {Key: "?", Desc: "help"}})
	return back + strings.Repeat(" ", max(1, m.ctx.Width-2-textw.Width(back)-textw.Width(right))) + right
}

// renderImmersiveFooter lists the open tab's own keys as far as they fit;
// without the full header, the footer carries them.
func (m *Model) renderImmersiveFooter() string {
	hints := append(m.tabHints(), components.KeyDesc{Key: "?", Desc: "help"})
	return components.RenderBoundedHints(m.ctx.Theme, hints, m.footerBudget(), components.KeyDesc{Key: "?", Desc: "help"})
}

// renderDrainFooter distinguishes leaving progress from cancelling work.
func (m *Model) renderDrainFooter() string {
	return components.RenderBoundedHints(m.ctx.Theme, []components.KeyDesc{
		{Key: "esc", Desc: "leave running"},
		{Key: "ctrl+c", Desc: "cancel", Write: true},
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
	if !m.tree.PageSubject && m.tree.SelectedRef().Kind != "" {
		tail = append([]key.Binding{key.NewBinding(key.WithKeys("enter"), key.WithHelp("↵", "open"))}, tail...)
	}
	if m.tree.Focus().Kind != k8s.KindContextList {
		tail = append([]key.Binding{k.Refresh}, tail...)
	}

	ref := m.tree.SelectedRef()
	reads := []key.Binding{}
	mutators := []key.Binding{}
	switch ref.Kind {
	case k8s.KindGenericResource:
		if ref.SubKind != "" {
			reads = append(reads, k.Describe)
		}
		if !ref.GVR.Empty() {
			reads = append(reads, k.Yaml)
		}
	case k8s.KindContext:
		// Cluster picker: the inline "↵ open" hint in the tree body already
		// teaches the drill verb. Don't restate Enter here — Jakob's rule.
		return tail
	case k8s.KindApp:
		reads = append(reads, k.Describe, k.Yaml, k.Logs, k.Metrics)
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
		reads = append(reads, k.Describe, k.Yaml, k.Logs, k.Metrics, k.Shell, k.PortForward)
		mutators = append(mutators, k.Restart)
	case k8s.KindContainer:
		reads = append(reads, k.Describe, k.Yaml, k.Logs, k.Shell)
	case k8s.KindNode:
		reads = append(reads, k.Describe, k.Yaml, k.Metrics)
		cordonVerb := k.Cordon
		if info, ok := m.tree.SelectedNodeInfo(); ok && !info.Schedulable {
			cordonVerb = key.NewBinding(
				key.WithKeys(k.Cordon.Help().Key),
				key.WithHelp(k.Cordon.Help().Key, "uncordon"),
			)
		}
		mutators = append(mutators, cordonVerb, k.Drain)
	case k8s.KindNamespace, k8s.KindNamespaceFolder:
		reads = append(reads, k.Describe, k.Yaml)
	case k8s.KindConfigMap:
		reads = append(reads, k.Describe, k.Yaml)
	case k8s.KindSecret:
		reads = append(reads, k.Describe, k.Yaml, k.Reveal)
	case k8s.KindService:
		reads = append(reads, k.Describe, k.Yaml, k.PortForward)
	case k8s.KindPVC:
		reads = append(reads, k.Describe, k.Yaml)
	case k8s.KindIngress:
		reads = append(reads, k.Describe, k.Yaml)
	case k8s.KindPV:
		reads = append(reads, k.Describe, k.Yaml)
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
	return frame.Width(w + 6).Render(textw.JoinVertical(lipgloss.Left, parts...))
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
	parts = append(parts, "", components.RenderConfirmButtons(t, "Apply edits", m.confirmOnAction), components.RenderModalKeysRow(t, []components.KeyDesc{{Key: "↑/↓/pgup/pgdn", Desc: "scroll diff"}}))

	content := textw.JoinVertical(lipgloss.Left, parts...)
	return t.ModalDanger.Render(content)
}

// confirmLabel names a confirm's action button by what it does and to what:
// "Restart api-2", "Drain node-3".
func confirmLabel(action, resource string) string {
	verb := strings.Fields(action)[0]
	name, _, _ := strings.Cut(resource, " · ")
	return strings.ToUpper(verb[:1]) + verb[1:] + " " + name
}

// renderConfirmModal is the gate for every write but edit and scale:
// delete, rollback, restart, cordon, drain. Framed in the danger red.
func (m *Model) renderConfirmModal(action, resource string) string {
	t := m.ctx.Theme
	resourceStyle := lipgloss.NewStyle().Foreground(t.P.FG).Bold(true)
	content := textw.JoinVertical(lipgloss.Left,
		t.Danger.Render(action),
		"",
		resourceStyle.Render(resource),
		t.DimTxt.Render(m.actionScope(m.tree.SelectedRef().Namespace)),
		"",
		components.RenderConfirmButtons(t, confirmLabel(action, resource), m.confirmOnAction),
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
	body := textw.JoinHorizontal(lipgloss.Top,
		lipgloss.NewStyle().PaddingRight(3).Render(cols[0]),
		lipgloss.NewStyle().PaddingRight(3).Render(cols[1]), cols[2])
	if textw.Width(body) > w {
		body = strings.Join(cols, "\n\n")
	}
	m.helpViewport.SetWidth(w)
	m.helpViewport.SetHeight(max(1, min(lipgloss.Height(body), m.ctx.Height-10)))
	m.helpViewport.SoftWrap = true
	m.helpViewport.SetContent(body)
	content := textw.JoinVertical(lipgloss.Left,
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
	return textw.JoinVertical(lipgloss.Left, lines...)
}

func widestKey(g keys.Group) int {
	w := 4
	measure := func(s string) {
		if n := textw.Width(s); n > w {
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
