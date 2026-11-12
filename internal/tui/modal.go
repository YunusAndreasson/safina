package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/YunusAndreasson/safina/internal/tui/components"
	"github.com/YunusAndreasson/safina/internal/tui/sections"
	"github.com/YunusAndreasson/safina/internal/tui/textw"
	"github.com/YunusAndreasson/safina/internal/tui/theme"
)

// The modal. A reading key on a list row — d describe, y yaml, l logs, as
// in k9s — opens that view over the list instead of opening the row's
// page. The list stays underneath, dimmed, and Esc closes the modal with
// the cursor where it was. On a resource page's own tabs the same keys
// switch tabs instead.

// openSheet opens the describe view of row in the modal.
func (m *Model) openSheet(row sections.NodeRef) tea.Cmd {
	kind, ns, name := activityTarget(row, &m.tree)
	m.modal = true
	m.state = stateSheetView
	return m.sheet.Open(m.ctx.Background, m.ctx.Manager, m.ctx.ActiveContext, &m.tree, kind, ns, name)
}

// isModalState reports the states the modal can show.
func (m *Model) isModalState(s state) bool {
	//exhaustive:ignore
	switch s {
	case stateSheetView, stateLogView, stateDescribeView, stateEventsView, stateMetricsView:
		return true
	default:
		return false
	}
}

// modalOpen reports whether the modal is up under whatever overlay (help,
// palette) may sit on top of it.
func (m *Model) modalOpen() bool {
	return m.modal && m.isModalState(m.baseState())
}

// closeModal closes the modal and leaves the list as it was.
func (m *Model) closeModal() {
	m.cleanupImmersiveState()
	m.state = stateMain
	m.modal = false
	m.tree.Focused = true
	m.syncPageSubject()
}

// handleModalKey takes the keys that mean something different while the
// modal is up: Esc, q and ← close it, the tab keys do nothing (the page beneath
// keeps its tab), and from the describe view the other reading keys open
// that view of the same row. It reports whether it took the key.
func (m *Model) handleModalKey(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	if !m.modal || !m.isModalState(m.state) || m.sectionHasFilterFocus() {
		return nil, false
	}
	switch s := msg.String(); {
	case s == "esc" || s == "q" || s == "left":
		// q closes a viewer, as in less; the logs and YAML views do the same.
		m.closeModal()
		return nil, true
	case s == "right" || s == "tab" || s == "shift+tab":
		return nil, true
	case len(s) == 1 && s[0] >= '1' && s[0] <= '9':
		return nil, true
	}
	if m.state != stateSheetView {
		return nil, false
	}
	if _, ok := m.verbTab(msg); ok {
		m.closeModal()
		return m.handleTreeAction(msg)
	}
	// The describe view reads; the row's other verbs (shell, forward,
	// edit, the writes) still work on it.
	if cmd, handled := m.handleRowVerb(msg); handled {
		return cmd, true
	}
	return nil, true
}

// modalTitle names what the modal shows and whose it is, for its top edge.
func (m *Model) modalTitle() (string, string) {
	//exhaustive:ignore
	switch m.state {
	case stateSheetView:
		return "Describe", m.sheet.Ref().Name
	case stateLogView:
		return "Logs", m.logSource.Name
	case stateDescribeView:
		return "YAML", m.describe.PaneTitle()
	case stateEventsView:
		return "Events", m.events.PaneTitle()
	case stateMetricsView:
		return "Metrics", m.metrics.Ref.Name
	}
	return "", ""
}

// modalBody is the open view at w × h.
func (m *Model) modalBody(w, h int) string {
	if m.state == stateSheetView {
		return m.sheet.View(m.ctx, w, h)
	}
	tab, _ := stateReadingTab(m.state)
	lines := strings.Split(m.renderTabBody(tab, components.PanelContentWidth(w), h), "\n")
	for i, ln := range lines {
		lines[i] = " " + ln
	}
	return strings.Join(lines, "\n")
}

// modalRight is what the modal's top edge carries on the right: the view's
// own state (follow, line counts), or the described resource's status.
func (m *Model) modalRight() string {
	if m.state == stateSheetView {
		return m.sheet.Status(m.ctx)
	}
	tab, _ := stateReadingTab(m.state)
	return m.tabState(tab)
}

// overlayModal floats the modal over a page w × h cells. The page dims
// beneath it except the highlighted row, which stays lit with the modal
// opening just below it (or above it, near the bottom), so the screen
// shows which row the modal is about.
func (m *Model) overlayModal(page string, w, h int) string {
	t := m.ctx.Theme
	const marginX, marginY = 3, 1
	const minBoxH = 8
	lines := strings.Split(page, "\n")
	sel := selectedLine(t, lines)
	boxW, boxH, y := w-2*marginX, h-2*marginY, marginY
	if sel >= 0 {
		below, above := h-marginY-(sel+1), sel-marginY
		switch {
		case below >= minBoxH:
			y, boxH = sel+1, below
		case above >= minBoxH:
			boxH = above
		}
	}
	if boxW < 30 || boxH < 6 {
		boxW, boxH, y = w, h, 0
	}
	accent := lipgloss.NewStyle().Foreground(t.P.Blue)
	what, whose := m.modalTitle()
	title := lipgloss.NewStyle().Foreground(t.P.FG).Bold(true).Render(what)
	if whose != "" {
		title += t.DimTxt.Render(" · ") + lipgloss.NewStyle().Foreground(t.P.FG).Render(whose)
	}
	// The top edge: ╭─ title ───── right ─╮, the title and the view's own
	// state set into the rule. Esc is named in the top bar and the footer.
	right := m.modalRight()
	if textw.Width(right) > 0 && textw.Width(title)+textw.Width(right)+10 > boxW {
		right = ""
	}
	if textw.Width(title) > boxW-6 {
		title = ansi.Truncate(title, boxW-6, "…")
	}
	var top string
	if right == "" {
		top = accent.Render("╭─") + " " + title + " " + accent.Render(strings.Repeat("─", max(0, boxW-5-textw.Width(title)))+"╮")
	} else {
		top = accent.Render("╭─") + " " + title + " " + accent.Render(strings.Repeat("─", max(0, boxW-8-textw.Width(title)-textw.Width(right)))) + " " + right + " " + accent.Render("─╮")
	}
	bottom := accent.Render("╰" + strings.Repeat("─", max(0, boxW-2)) + "╯")
	body := strings.Split(m.modalBody(boxW-2, boxH-2), "\n")
	rows := make([]string, 0, boxH)
	rows = append(rows, components.FitLine(top, boxW))
	side := accent.Render("│")
	for i := range boxH - 2 {
		ln := ""
		if i < len(body) {
			ln = body[i]
		}
		rows = append(rows, side+components.FitLine(ln, boxW-2)+side)
	}
	rows = append(rows, components.FitLine(bottom, boxW))
	box := strings.Join(rows, "\n")

	dim := lipgloss.NewStyle().Foreground(t.P.Dim)
	backdrop := make([]string, len(lines))
	for i, ln := range lines {
		backdrop[i] = dim.Render(ansi.Strip(ln))
		if i == sel {
			backdrop[i] = ln // the row the modal is about stays lit
		}
	}
	x := (w - boxW) / 2
	canvas := lipgloss.NewCanvas(w, h).Compose(lipgloss.NewCompositor(
		lipgloss.NewLayer(strings.Join(backdrop, "\n")).Z(0),
		lipgloss.NewLayer(box).X(x).Y(y).Z(1),
	))
	// The canvas can come back a cell short on a row it keeps as drawn;
	// every row is fitted to the page's width so the frame's side lines up.
	out := strings.Split(canvas.Render(), "\n")
	for i := range out {
		out[i] = components.FitLine(out[i], w)
	}
	return strings.Join(out, "\n")
}

// modalKeys are the keys that work in the open modal, for the top bar: the
// view's own, and from the describe view the row's other views and verbs.
func (m *Model) modalKeys() []components.KeyDesc {
	if m.state != stateSheetView {
		return m.tabHints()
	}
	k := m.ctx.Keys
	var keys []components.KeyDesc
	for _, b := range []key.Binding{k.Yaml, k.Logs, k.Shell, k.PortForward, k.Yank} {
		if b.Enabled() && m.rowTakes(b) {
			keys = append(keys, components.KeyDesc{Key: b.Help().Key, Desc: b.Help().Desc})
		}
	}
	return keys
}

// rowTakes reports whether the highlighted row answers a verb key, as the
// list's own key hints do.
func (m *Model) rowTakes(b key.Binding) bool {
	for _, h := range m.contextualShortHelp() {
		if h.Help().Key == b.Help().Key {
			return true
		}
	}
	return false
}

// selectedLine is the index of the line painted as the highlighted row,
// read off the selection's background, or -1 when no row is on show.
func selectedLine(t *theme.Theme, lines []string) int {
	r, g, b, _ := t.P.BlueDark.RGBA()
	paint := fmt.Sprintf("48;2;%d;%d;%d", r>>8, g>>8, b>>8)
	for i, ln := range lines {
		if strings.Contains(ln, paint) {
			return i
		}
	}
	return -1
}
