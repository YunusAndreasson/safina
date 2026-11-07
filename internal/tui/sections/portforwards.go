package sections

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/yunus/safina/internal/history"
	"github.com/yunus/safina/internal/k8s"
	"github.com/yunus/safina/internal/tui/components"
	"github.com/yunus/safina/internal/tui/theme"
	"github.com/yunus/safina/internal/tui/tuictx"
)

// Forwards is the modal that lists every active port-forward this process
// owns and lets the user `d`elete one. Lives as a section (rather than
// inline in view.go) so the input + list pair stays close to other
// section-shaped modals (Events, Describe) and the model file doesn't grow
// another bespoke render path.
type Forwards struct {
	rows  []k8s.PortForwardSnapshot
	cur   int
	theme *theme.Theme
}

// NewForwards wires the section to the theme. The manager is read on every
// Refresh, not captured, so a forward started elsewhere shows up the next
// time the modal opens.
func NewForwards(c *tuictx.Ctx) Forwards {
	return Forwards{theme: c.Theme}
}

// Refresh pulls a fresh snapshot from the manager. Called by the root every
// time the modal opens (and after a delete) — the manager's List() is the
// source of truth.
func (f *Forwards) Refresh(snaps []k8s.PortForwardSnapshot) {
	f.rows = snaps
	if f.cur >= len(f.rows) {
		f.cur = 0
	}
}

// CursorRowID returns the id of the row under the highlight, "" when empty.
func (f *Forwards) CursorRowID() string {
	if f.cur < 0 || f.cur >= len(f.rows) {
		return ""
	}
	return f.rows[f.cur].ID
}

// MoveCursor adjusts the highlight by delta (+1 down, -1 up). Wraps so the
// modal stays one-eyed-friendly (no off-end / off-start dead presses).
func (f *Forwards) MoveCursor(delta int) {
	if len(f.rows) == 0 {
		f.cur = 0
		return
	}
	f.cur = (f.cur + delta + len(f.rows)) % len(f.rows)
}

// Empty reports whether there are zero active forwards. The root uses this
// to render an alternate "no forwards" message rather than an empty box.
func (f *Forwards) Empty() bool { return len(f.rows) == 0 }

// Body returns the inline unframed content for embedding in the workspace
// tab area. Same content as View(w) — kept as an alias so the workspace's
// tab dispatch reads consistently with other sections.
func (f *Forwards) Body(w int) string { return f.View(w) }

// PaneTitle returns the workspace tab title.
func (f *Forwards) PaneTitle() string { return "port forwards" }

// View renders the modal contents. Caller wraps in the standard modal
// frame and modalBackdrop — same as renderConfirmModal.
func (f *Forwards) View(w int) string {
	t := f.theme
	title := t.Divider("port forwards", w)
	if len(f.rows) == 0 {
		body := lipgloss.NewStyle().Foreground(t.P.FGFade).Render("  (no active forwards)")
		hint := t.Hint.Render("press f on a pod or service row to start one")
		return lipgloss.JoinVertical(lipgloss.Left, title, "", body, "", hint)
	}

	header := lipgloss.NewStyle().Foreground(t.P.Dim).Render(
		fmt.Sprintf("%-2s %-22s %-12s %-22s %-12s %s", "", "context/namespace", "kind", "name", "ports", "uptime"),
	)
	lines := []string{title, ""}
	compact := w < 90
	if !compact {
		lines = append(lines, header)
	}
	now := time.Now()
	for i, r := range f.rows {
		marker := "  "
		if i == f.cur {
			marker = lipgloss.NewStyle().Foreground(t.P.Pink).Bold(true).Render("▸ ")
		}
		ctxNs := truncMid(r.ContextName+"/"+r.Namespace, 22)
		kind := strings.ToLower(r.TargetKind)
		name := truncMid(r.TargetName, 22)
		ports := fmt.Sprintf("%d→%d", r.LocalPort, r.RemotePort)
		up := humanShort(now.Sub(r.StartedAt))
		if compact {
			status := ports + " · " + up
			if r.Stale {
				status += " · stale"
			}
			lines = append(lines,
				marker+t.BlueBold.Render(ansi.Truncate(status, max(1, w-2), "…")),
				t.Hint.Render("  "+ansi.Truncate(kind+"/"+r.TargetName, max(1, w-2), "…")),
				t.Hint.Render("  "+ansi.Truncate(r.ContextName+"/"+r.Namespace, max(1, w-2), "…")),
			)
			continue
		}
		row := fmt.Sprintf("%-22s %-12s %-22s %-12s %s", ctxNs, kind, name, ports, up)
		style := lipgloss.NewStyle().Foreground(t.P.FG)
		if r.Stale {
			row += "  " + lipgloss.NewStyle().Foreground(t.P.Red).Render("stale")
			style = style.Foreground(t.P.FGFade)
		}
		lines = append(lines, marker+style.Render(row))
	}
	lines = append(lines, "")
	// Delete is K (kill) everywhere else in the app — the forwards list
	// is no exception. Keeping lowercase `d` here would steal the global
	// describe letter and break the "uppercase = mutator" convention.
	lines = append(lines, components.RenderHintRow(t, []components.KeyDesc{
		{Key: "K", Desc: "stop"},
		{Key: "esc", Desc: "close"},
	}))
	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

// truncMid keeps the start and end and inserts "…" so the human-meaningful
// context/name fragments stay readable when they overflow.
func truncMid(s string, width int) string {
	if len(s) <= width {
		return s
	}
	if width < 3 {
		return s[:width]
	}
	left := (width - 1) / 2
	right := width - 1 - left
	return s[:left] + "…" + s[len(s)-right:]
}

// humanShort renders a Duration like "3s", "5m", "2h"; matches the topbar's
// refresh pill style so users learn one pattern.
func humanShort(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh", int(d.Hours()))
}

// ── Commands ──────────────────────────────────────────────────────────

// PortForwardStartedMsg lands when StartPortForward completes (success or
// failure). The root surfaces it as a banner — successful starts call out
// the local port we actually bound (which may differ from what the user
// asked for, if there was a collision).
type PortForwardStartedMsg struct {
	Forward   *k8s.PortForward
	Requested int // what the user typed; if != Forward.LocalPort, the banner says "on :X (Y in use)"
	Err       error
}

// PortForwardStoppedMsg fires after Stop. The list modal refreshes on this.
type PortForwardStoppedMsg struct {
	ID  string
	Err error
}

// StartPortForward issues the resolver + dialler dance off the main loop.
// localPort 0 means "pick a free one"; remotePort must be > 0.
func StartPortForward(
	ctx context.Context,
	mgr *k8s.Manager,
	contextName, namespace, targetKind, targetName string,
	localPort, remotePort int,
) tea.Cmd {
	requested := localPort
	return func() tea.Msg {
		op := mgr.History.Begin(mgr.HistoryTarget(contextName, namespace, targetKind, targetName), "port_forward", map[string]string{"localPort": strconv.Itoa(localPort), "remotePort": strconv.Itoa(remotePort)})
		client, ok := mgr.GetClient(contextName)
		if !ok {
			op.Finish(errContextNotFound, "connected")
			return PortForwardStartedMsg{Requested: requested, Err: errContextNotFound}
		}
		pf, err := mgr.Forwards.StartPortForward(
			history.WithOperation(ctx, op), client.Clientset, client.RESTConfig,
			contextName, namespace, targetKind, targetName,
			localPort, remotePort,
		)
		op.Finish(err, "connected")
		if err == nil {
			op.Record("resolved", "", map[string]string{"forward": pf.ID, "pod": pf.ResolvedPod, "localPort": strconv.Itoa(pf.LocalPort)})
			mgr.Forwards.SetHistory(pf.ID, op)
		}
		return PortForwardStartedMsg{Forward: pf, Requested: requested, Err: err}
	}
}

// StopPortForward tears one forward down.
func StopPortForward(mgr *k8s.Manager, id string) tea.Cmd {
	return func() tea.Msg {
		target := history.Target{}
		for _, forward := range mgr.Forwards.List() {
			if forward.ID == id {
				target = mgr.HistoryTarget(forward.ContextName, forward.Namespace, forward.TargetKind, forward.TargetName)
				break
			}
		}
		op := mgr.History.Begin(target, "port_forward_stop", map[string]string{"forward": id})
		err := mgr.Forwards.Stop(id)
		op.Finish(err, "disconnected")
		return PortForwardStoppedMsg{ID: id, Err: err}
	}
}
