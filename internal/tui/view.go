package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/yunus/safina/internal/k8s"
	"github.com/yunus/safina/internal/tui/components"
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
func (m *Model) View() tea.View {
	// Render first so the chat's layout state (input row offset) is fresh
	// before we read its hardware-cursor position.
	content := m.render()
	v := tea.View{
		Content:     content,
		AltScreen:   true,
		MouseMode:   tea.MouseModeCellMotion,
		WindowTitle: m.title,
		KeyboardEnhancements: tea.KeyboardEnhancements{
			ReportAlternateKeys: true,
		},
	}
	// Hardware cursor only when the user is actively typing into the chat
	// textarea, on the main view. Modal states (logs/events/describe/shell/
	// help/confirm) leave Cursor=nil so the cursor stays hidden.
	if m.state == stateMain {
		v.Cursor = m.chat.Cursor()
	}
	return v
}

func (m *Model) render() string {
	switch m.state {
	case stateLogView:
		return m.logs.View(m.ctx, m.ctx.Width, m.ctx.Height)
	case stateEventsView:
		return m.events.View(m.ctx, m.ctx.Width, m.ctx.Height)
	case stateDescribeView:
		return m.describe.View(m.ctx, m.ctx.Width, m.ctx.Height)
	case stateShellView:
		return m.shell.View(m.ctx, m.ctx.Width, m.ctx.Height)
	case stateHelp:
		return m.renderHelpModal()
	case stateScaleInput:
		return m.renderScaleModal()
	case stateDeleteConfirm:
		return m.renderConfirmModal("restart pod", m.podName())
	case stateRollbackConfirm:
		return m.renderConfirmModal("revert deployment", m.deployName())
	case stateRestartConfirm:
		return m.renderConfirmModal("restart app", m.deployName())
	default:
		return m.renderMain()
	}
}

// renderBanner draws the transient status/error line above the main split.
// Errors render in red (✗), successful action confirmations in green (✓).
// Both share the same row so a fresh banner of either kind replaces whatever
// was there — operators only ever look at one slot for "what just happened".
func (m *Model) renderBanner(w int) string {
	t := m.ctx.Theme
	if m.bannerErr {
		return t.ErrLine.Width(w).Render("  ✗  " + m.bannerMsg)
	}
	return t.OkLine.Width(w).Render("  ✓  " + m.bannerMsg)
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

func (m *Model) podName() string {
	if p, ok := m.tree.SelectedPodInfo(); ok {
		return p.Name
	}
	return ""
}

func (m *Model) deployName() string {
	if a, ok := m.tree.SelectedAppInfo(); ok {
		return a.Name
	}
	return ""
}

// ── main layout ───────────────────────────────────────────────────────

func (m *Model) renderMain() string {
	w, h := m.ctx.Width, m.ctx.Height
	if w < 50 || h < 14 {
		return m.ctx.Theme.DimPad.Render("terminal too small")
	}

	parts := []string{}
	used := 1 // footer (reserved)

	// Topbar carries the breadcrumb of the user's current tree location —
	// the canonical "where am I" answer, hoisted from the panel title so
	// it lives where users instinctively look. Refresh pill confirms data
	// is live; clock floats right.
	parts = append(parts, components.Topbar(
		m.ctx.Theme, w,
		m.tree.BreadcrumbCrumbs(),
		m.refreshAge(),
		m.now.Format("15:04:05"),
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

	// 60/40 split — tree stays the working surface, chat earns more width
	// now that it's a real reading column (40 cols ≈ a comfortable text
	// measure, where 35% of an 80-col term was too tight to read prose).
	spacing := 2
	lw := (w - spacing) * 60 / 100
	rw := w - lw - spacing
	// Pre-size the chat so its textarea/viewport math runs before View.
	m.chat.SetSize(rw, botH)
	// chatY is the screen row of the chat panel's top edge — sum of
	// every preceding row block (topbar + optional banner). The chat
	// uses this to place a real terminal cursor over its textarea.
	chatY := used - 1 // used counts a reserved footer row that lives below the split
	m.chat.SetScreenOrigin(lw+spacing, chatY)
	bottom := lipgloss.JoinHorizontal(lipgloss.Top,
		m.tree.View(m.ctx, lw, botH),
		strings.Repeat(" ", spacing),
		m.chat.View(m.ctx, rw, botH),
	)
	parts = append(parts, bottom, m.renderFooter())

	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

func (m *Model) renderFooter() string {
	t := m.ctx.Theme
	var view string
	if m.chat.Focused {
		view = m.renderChatFooter()
	} else {
		view = m.help.ShortHelpView(m.contextualShortHelp())
	}
	return lipgloss.NewStyle().
		Background(t.P.BG2).
		Width(m.ctx.Width).
		MaxHeight(1).
		Padding(0, 1).
		Render(view)
}

// renderChatFooter mirrors contextualShortHelp's role for chat: a one-glance
// "what can I do here". We hand-roll it instead of going through bubbles/help
// because the chat keymap is tiny and four of the five entries are control
// chords that don't fit the help.Model's Binding model cleanly.
func (m *Model) renderChatFooter() string {
	t := m.ctx.Theme
	pair := func(k, d string) string {
		return t.HelpKey.Render(k) + " " + t.HelpDesc.Render(d)
	}
	sep := t.HelpDesc.Render(" · ")
	send := pair("↵", "send")
	if m.chat.Streaming() {
		send = t.HelpKey.Render("esc") + " " + t.HelpDesc.Render("cancel")
	}
	parts := []string{
		send,
		pair("⇧↵", "newline"),
		pair("tab", "tree"),
		pair("⌃l", "clear"),
		pair("⌃n", "new"),
		pair("?", "help"),
	}
	return strings.Join(parts, sep)
}

// contextualShortHelp builds the footer keymap from the current tree row.
// Progressive disclosure: ↑/↓/↵/esc are universal across every TUI ever
// shipped — Jakob says don't reteach them on every redraw — and action keys
// (l/e/d/s/r/x/S) only carry meaning for some kinds. We hide what doesn't
// apply to the highlighted row so the footer becomes a one-glance "what can
// I do *to this row*" hint instead of a full key index. The full keymap
// stays available behind `?`.
func (m *Model) contextualShortHelp() []key.Binding {
	k := m.ctx.Keys

	// Always-on chrome keys. Refresh is only meaningful inside a cluster,
	// so we drop it at the context-list level.
	tail := []key.Binding{k.Ask, k.Filter, k.Help, k.Quit}
	if m.tree.Focus().Kind != k8s.KindContextList {
		tail = append([]key.Binding{k.Refresh}, tail...)
	}

	ref := m.tree.SelectedRef()
	head := []key.Binding{}
	switch ref.Kind {
	case k8s.KindContext:
		// At the cluster picker, ↵ to drill in is the only thing to teach;
		// the action keys aren't applicable here.
		return append([]key.Binding{k.Enter}, tail...)
	case k8s.KindApp:
		head = append(head, k.Logs, k.Events, k.Describe, k.Restart)
		if ref.SubKind == k8s.AppKindDeployment || ref.SubKind == k8s.AppKindStatefulSet {
			head = append(head, k.Scale)
		}
		if ref.SubKind == k8s.AppKindDeployment {
			head = append(head, k.Rollback)
		}
	case k8s.KindPod:
		head = append(head, k.Logs, k.Shell, k.Events, k.Describe, k.Restart)
	case k8s.KindContainer:
		head = append(head, k.Logs, k.Shell, k.Describe)
	case k8s.KindNode:
		head = append(head, k.Events, k.Describe)
	case k8s.KindNamespace:
		head = append(head, k.Events, k.Describe)
	}
	head = append(head, k.FirstFail)
	return append(head, tail...)
}

// ── modals ────────────────────────────────────────────────────────────

func (m *Model) modalBackdrop() []lipgloss.WhitespaceOption {
	t := m.ctx.Theme
	return []lipgloss.WhitespaceOption{
		lipgloss.WithWhitespaceChars("·"),
		lipgloss.WithWhitespaceStyle(lipgloss.NewStyle().Foreground(t.P.BG3)),
	}
}

func (m *Model) renderScaleModal() string {
	t := m.ctx.Theme
	deploy := m.deployName()
	content := lipgloss.JoinVertical(lipgloss.Left,
		t.BlueBold.Render("scale "+deploy),
		"",
		m.scaleInput.View(),
		"",
		t.DimTxt.Italic(true).Render("Enter confirm  Esc cancel"),
	)
	return lipgloss.Place(m.ctx.Width, m.ctx.Height, lipgloss.Center, lipgloss.Center,
		t.Modal.Render(content), m.modalBackdrop()...)
}

func (m *Model) renderConfirmModal(action, resource string) string {
	t := m.ctx.Theme
	content := lipgloss.JoinVertical(lipgloss.Left,
		lipgloss.NewStyle().Foreground(t.P.Yellow).Bold(true).Render(action),
		"",
		lipgloss.NewStyle().Foreground(t.P.Red).Bold(true).Render(resource),
		"",
		lipgloss.NewStyle().Foreground(t.P.Green).Render("y")+t.DimTxt.Render(" confirm  ")+t.DimTxt.Render("n/Esc cancel"),
	)
	return lipgloss.Place(m.ctx.Width, m.ctx.Height, lipgloss.Center, lipgloss.Center,
		t.Modal.Render(content), m.modalBackdrop()...)
}

func (m *Model) renderHelpModal() string {
	t := m.ctx.Theme
	full := m.help.FullHelpView(m.ctx.Keys.FullHelp())
	content := lipgloss.JoinVertical(lipgloss.Left,
		t.CyanBold.Render("◆ keybindings"),
		"",
		full,
		"",
		t.DimTxt.Italic(true).Render("Esc or ? to close"),
	)
	return lipgloss.Place(m.ctx.Width, m.ctx.Height, lipgloss.Center, lipgloss.Center,
		t.Modal.Padding(1, 2).Render(content), m.modalBackdrop()...)
}
