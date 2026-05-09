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
	case stateEditConfirm:
		return m.renderEditConfirmModal()
	case statePalette:
		// Palette renders over the main view — caller can see the tree
		// behind the modal backdrop, which helps orient them when they
		// drill through the palette to a faraway resource.
		return m.palette.View(m.ctx, m.ctx.Width, m.ctx.Height)
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
		view = m.renderTreeFooter()
	}
	return lipgloss.NewStyle().
		Background(t.P.BG2).
		Width(m.ctx.Width).
		MaxHeight(1).
		Padding(0, 1).
		Render(view)
}

// renderTreeFooter renders the contextual short help with two visual
// layers: action verbs (the per-row "what can I do" set) get the bright
// HelpKey style; chrome keys (refresh, filter, help, quit) render dim so
// the eye reads them as global app affordances rather than row actions.
// Convention says shortcuts live at the bottom — we keep them there but
// give the operator a hierarchy to scan instead of a flat run of letters.
func (m *Model) renderTreeFooter() string {
	t := m.ctx.Theme
	bindings := m.contextualShortHelp()
	chromeKeys := map[string]bool{
		m.ctx.Keys.Refresh.Help().Key: true,
		m.ctx.Keys.Filter.Help().Key:  true,
		m.ctx.Keys.Help.Help().Key:    true,
		m.ctx.Keys.Quit.Help().Key:    true,
		m.ctx.Keys.Enter.Help().Key:   true, // shows up alone on the cluster picker
	}
	// Both halves of a chrome entry render in the same dim grey — the
	// contrast with HelpKey blue-bold is what the eye reads, not internal
	// key/desc styling within the chrome chunk.
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
	sep := lipgloss.NewStyle().Foreground(t.P.Dim).Render(" · ")
	return strings.Join(parts, sep)
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
//
// Three principles keep the footer scannable instead of overwhelming:
//
//  1. Universal nav (↑/↓/↵/esc) is never restated — Jakob's rule.
//  2. Action verbs follow a single ordering — inspect verbs first
//     (d/l/e/v/S in that order), then change verbs (x/s/r/E). Operators
//     learn one column once and find what they need by position, not by
//     hunting the alphabet.
//  3. Tail chrome is trimmed to row-relevant keys plus help/quit. F
//     (next-fail) and i (ask — Tab is the discoverable alias) live in
//     the `?` modal; they don't need to repeat on every redraw.
func (m *Model) contextualShortHelp() []key.Binding {
	k := m.ctx.Keys

	// Always-on chrome keys. Refresh only matters inside a cluster, so we
	// drop it at the context-list level.
	tail := []key.Binding{k.Filter, k.Help, k.Quit}
	if m.tree.Focus().Kind != k8s.KindContextList {
		tail = append([]key.Binding{k.Refresh}, tail...)
	}

	ref := m.tree.SelectedRef()
	head := []key.Binding{}
	switch ref.Kind {
	case k8s.KindContext:
		// At the cluster picker, ↵ to drill in is the only thing to teach.
		return append([]key.Binding{k.Enter}, tail...)
	case k8s.KindApp:
		head = append(head, k.Describe, k.Logs, k.Events)
		// Restart is only wired for Deployment/StatefulSet/DaemonSet;
		// CronJob/Job don't accept a rolling-restart, so we drop the
		// hint there rather than tease a verb that's a no-op.
		if ref.SubKind == k8s.AppKindDeployment ||
			ref.SubKind == k8s.AppKindStatefulSet ||
			ref.SubKind == k8s.AppKindDaemonSet {
			head = append(head, k.Restart)
		}
		if ref.SubKind == k8s.AppKindDeployment || ref.SubKind == k8s.AppKindStatefulSet {
			head = append(head, k.Scale)
		}
		if ref.SubKind == k8s.AppKindDeployment {
			head = append(head, k.Rollback)
		}
	case k8s.KindPod:
		head = append(head, k.Describe, k.Logs, k.Events, k.Shell, k.Restart)
	case k8s.KindContainer:
		head = append(head, k.Describe, k.Logs, k.Shell)
	case k8s.KindNode, k8s.KindNamespace:
		head = append(head, k.Describe, k.Events)
	case k8s.KindConfigMap:
		head = append(head, k.Describe)
	case k8s.KindSecret:
		head = append(head, k.Describe, k.Reveal)
	case k8s.KindService, k8s.KindPVC:
		head = append(head, k.Describe, k.Events)
	case k8s.KindIngress, k8s.KindPV:
		head = append(head, k.Describe)
	}
	if editKindFor(ref) != "" {
		head = append(head, k.Edit)
	}
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
		// Plus is green, minus is red — matches the diff convention every
		// engineer already has muscle memory for. Stat sits above the
		// full unified diff as a one-line headline; the diff itself
		// answers "what specifically am I changing" with proper color.
		stat = lipgloss.NewStyle().Foreground(t.P.Green).Render(fmt.Sprintf("+%d", added)) +
			"  " +
			lipgloss.NewStyle().Foreground(t.P.Red).Render(fmt.Sprintf("−%d", removed)) +
			t.DimTxt.Render(" lines")
		body = renderUnifiedDiff(t, m.edit.Orig, m.edit.Edited)
	}

	parts := []string{
		lipgloss.NewStyle().Foreground(t.P.Yellow).Bold(true).Render(subject),
		"",
		lipgloss.NewStyle().Foreground(t.P.Red).Bold(true).Render(resource),
		"",
		stat,
	}
	if body != "" {
		parts = append(parts, "", body)
	}
	parts = append(parts,
		"",
		lipgloss.NewStyle().Foreground(t.P.Green).Render("y")+t.DimTxt.Render(" apply  ")+t.DimTxt.Render("n/Esc cancel"),
	)

	content := lipgloss.JoinVertical(lipgloss.Left, parts...)
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
	content := lipgloss.JoinVertical(lipgloss.Left,
		t.CyanBold.Render("◆ keybindings"),
		"",
		body,
		"",
		t.DimTxt.Italic(true).Render("Esc or ? to close"),
	)
	return lipgloss.Place(m.ctx.Width, m.ctx.Height, lipgloss.Center, lipgloss.Center,
		t.Modal.Padding(1, 2).Render(content), m.modalBackdrop()...)
}

// renderHelpGroup formats a single column of the help modal: a dim title,
// a thin separator, then `key  desc` rows. Key glyph is fixed-width so the
// descriptions line up; widest single-glyph in the keymap is "esc" (3 cells).
func renderHelpGroup(t *theme.Theme, g keys.Group) string {
	const keyW = 4
	lines := []string{
		t.CyanBold.Render(g.Title),
		t.DimTxt.Render(strings.Repeat("─", 8)),
	}
	for _, p := range g.Pairs {
		lines = append(lines, helpRow(t, keyW, p.Key, p.Desc))
	}
	for _, b := range g.Bindings {
		h := b.Help()
		lines = append(lines, helpRow(t, keyW, h.Key, h.Desc))
	}
	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

func helpRow(t *theme.Theme, keyW int, k, desc string) string {
	return t.HelpKey.Width(keyW).Render(k) + t.HelpDesc.Render(desc)
}
