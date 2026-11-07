package tui

import (
	"strings"
	"time"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// choiceDialog shares library-owned scrolling, selection and pagination across
// notification history and log targets. It overlays without replacing the view.
type choiceItem struct {
	title, detail string
	value         any
}

func (i choiceItem) Title() string       { return i.title }
func (i choiceItem) Description() string { return i.detail }
func (i choiceItem) FilterValue() string { return i.title }

type (
	choiceDialog struct {
		kind   string
		model  list.Model
		footer string
	}
	notification struct {
		text, context string
		failed        bool
		at            time.Time
		retry         func() tea.Cmd
	}
)

func (m *Model) openChoices(kind, title, footer string, items []list.Item) {
	d := list.NewDefaultDelegate()
	d.SetSpacing(0)
	t := m.ctx.Theme
	d.Styles.NormalTitle = d.Styles.NormalTitle.Foreground(t.P.FG)
	d.Styles.NormalDesc = d.Styles.NormalDesc.Foreground(t.P.FGFade)
	d.Styles.SelectedTitle = d.Styles.SelectedTitle.Foreground(t.P.Cyan).BorderForeground(t.P.Cyan)
	d.Styles.SelectedDesc = d.Styles.SelectedDesc.Foreground(t.P.FG).BorderForeground(t.P.Cyan)
	l := list.New(items, d, 60, 12)
	l.Title = title
	l.Styles.Title = t.CyanBold
	if kind == "notifications" {
		l.SetStatusBarItemName("notification", "notifications yet")
	}
	if kind == "logs" {
		l.SetStatusBarItemName("target", "log targets")
	}
	l.SetFilteringEnabled(false)
	l.SetShowHelp(false)
	l.SetShowStatusBar(false)
	l.DisableQuitKeybindings()
	m.choices = &choiceDialog{kind: kind, model: l, footer: footer}
}

func (m *Model) choicesCard() string {
	w := max(20, min(76, m.ctx.Width-10))
	footer := m.ctx.Theme.Hint.Render(ansi.Wrap(m.choices.footer, w, ""))
	h := max(4, min(18, max(5, len(m.choices.model.Items())*2+4), m.ctx.Height-6-lipgloss.Height(footer)))
	m.choices.model.SetSize(w, h)
	return m.ctx.Theme.Modal.Padding(1, 2).Render(lipgloss.JoinVertical(lipgloss.Left, m.choices.model.View(), footer))
}

func (m *Model) recordNotification(text string, failed bool) {
	cluster := ""
	if m.ctx != nil {
		cluster = m.ctx.ActiveContext
	}
	if len(m.notifications) > 0 {
		last := &m.notifications[len(m.notifications)-1]
		if last.text == text && last.context == cluster {
			last.at = time.Now()
			return
		}
	}
	m.notifications = append(m.notifications, notification{text: text, failed: failed, context: cluster, at: time.Now()})
	if len(m.notifications) > 50 {
		m.notifications = m.notifications[len(m.notifications)-50:]
	}
}

func (m *Model) openNotifications() {
	items := make([]list.Item, 0, len(m.notifications))
	for i := len(m.notifications) - 1; i >= 0; i-- {
		n := m.notifications[i]
		mark := "✓"
		if n.failed {
			mark = "✗"
		}
		items = append(items, choiceItem{title: mark + " " + n.text, detail: n.at.Format("15:04:05") + " · " + n.context, value: n})
	}
	m.openChoices("notifications", "Notifications", "enter details · r retry read · ctrl+d dismiss · esc close", items)
}
func (m *Model) dismissBanner() { m.bannerMsg = ""; m.bannerErr = false; m.bannerEpoch++ }
func (m *Model) handleChoices(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		m.choices = nil
		return m, nil
	case "ctrl+d":
		m.dismissBanner()
		return m, nil
	case "r":
		if item, ok := m.choices.model.SelectedItem().(choiceItem); ok {
			if n, ok := item.value.(notification); ok && n.retry != nil {
				m.choices = nil
				return m, n.retry()
			}
		}
		return m, nil
	case "enter":
		item, ok := m.choices.model.SelectedItem().(choiceItem)
		if !ok {
			return m, nil
		}
		if n, ok := item.value.(notification); ok {
			// Wrap full errors in the same list's scrollable detail surface.
			lines := strings.Split(ansi.Wrap(n.text, max(20, min(70, m.ctx.Width-16)), ""), "\n")
			items := make([]list.Item, 0, len(lines))
			for _, line := range lines {
				items = append(items, choiceItem{title: line, detail: n.context})
			}
			m.openChoices("details", "Notification · "+n.at.Format("15:04:05"), "esc close", items)
			return m, nil
		}
		if target, ok := item.value.(logTarget); ok {
			m.choices = nil
			return m, m.startTargetLogs(target, false)
		}
	case "p":
		if item, ok := m.choices.model.SelectedItem().(choiceItem); ok {
			if target, ok := item.value.(logTarget); ok && target.previous {
				m.choices = nil
				return m, m.startTargetLogs(target, true)
			}
		}
	}
	var cmd tea.Cmd
	m.choices.model, cmd = m.choices.model.Update(msg)
	return m, cmd
}

func (m *Model) raiseReadError(text string, retry func() tea.Cmd) tea.Cmd {
	cmd := m.raiseError(text)
	if len(m.notifications) > 0 {
		m.notifications[len(m.notifications)-1].retry = retry
	}
	return cmd
}
