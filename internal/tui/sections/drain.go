package sections

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/yunus/safina/internal/k8s"
	"github.com/yunus/safina/internal/tui/theme"
	"github.com/yunus/safina/internal/tui/tuictx"
)

// Drain is the modal that shows live progress for a running drain. It
// renders the last DrainEvent it received and the spinner the model
// already ticks for other async ops. The section doesn't own the drain
// goroutine — the model launches a tea.Cmd that pumps DrainEvent values
// through DrainProgressMsg.
type Drain struct {
	node     string
	latest   k8s.DrainEvent
	started  bool
	finished bool
	cancel   context.CancelFunc
	theme    *theme.Theme
}

// NewDrain builds the section.
func NewDrain(c *tuictx.Ctx) Drain {
	return Drain{theme: c.Theme}
}

// Reset arms the modal for a new node. The cancel func tears down the
// drain goroutine when the user presses ctrl+c (esc is intentionally
// a no-op — drain is destructive enough that close-by-accident is a worse
// failure than "I can't press esc to close").
func (d *Drain) Reset(node string, cancel context.CancelFunc) {
	d.node = node
	d.latest = k8s.DrainEvent{}
	d.started = true
	d.finished = false
	d.cancel = cancel
}

// Cancel asks the running drain to abort. The DrainNode function uncordons
// the node on cancel so we don't leave it stuck in a cordoned state.
func (d *Drain) Cancel() {
	if d.cancel != nil {
		d.cancel()
	}
}

// Apply records a new progress event. Returns true if this was the final
// event (Done=true), which the root reads to flip back to stateMain.
func (d *Drain) Apply(e k8s.DrainEvent) bool {
	d.latest = e
	if e.Done {
		d.finished = true
	}
	return e.Done
}

// Body returns the inline unframed content for the workspace tab.
func (d *Drain) Body(w int) string { return d.View(w) }

// PaneTitle returns the workspace tab title.
func (d *Drain) PaneTitle() string { return "drain · " + d.node }

// View renders the modal contents. Caller wraps in the modal frame.
func (d *Drain) View(w int) string {
	t := d.theme
	title := lipgloss.NewStyle().Foreground(t.P.Yellow).Bold(true).Render("◆ drain " + d.node)

	phase := d.latest.Phase
	if phase == "" {
		phase = "starting"
	}
	phaseLine := lipgloss.NewStyle().Foreground(t.P.Cyan).Render("● " + phase)

	stats := ""
	if d.latest.PodsRemaining > 0 {
		stats = fmt.Sprintf("%d pods remaining", d.latest.PodsRemaining)
	}
	if d.latest.Msg != "" {
		if stats != "" {
			stats = stats + " — " + d.latest.Msg
		} else {
			stats = d.latest.Msg
		}
	}
	statsLine := lipgloss.NewStyle().Foreground(t.P.FG).Render(stats)

	remain := ""
	if len(d.latest.RemainingPods) > 0 {
		// Show up to 3 names so the user knows what's blocking them
		// (typically: a pdb-protected workload).
		preview := d.latest.RemainingPods
		more := 0
		if len(preview) > 3 {
			more = len(preview) - 3
			preview = preview[:3]
		}
		remain = strings.Join(preview, "\n")
		if more > 0 {
			remain = remain + lipgloss.NewStyle().Foreground(t.P.Dim).Render(fmt.Sprintf("\n…and %d more", more))
		}
		remain = lipgloss.NewStyle().Foreground(t.P.FGFade).Render(remain)
	}

	elapsed := ""
	if !d.latest.StartTimestamp.IsZero() {
		elapsed = lipgloss.NewStyle().Foreground(t.P.Dim).Render(fmt.Sprintf("elapsed %ds", int(time.Since(d.latest.StartTimestamp).Seconds())))
	}

	footer := t.DimTxt.Italic(true).Render("ctrl+c cancel (uncordons before exit)")

	parts := []string{title, "", phaseLine, statsLine}
	if remain != "" {
		parts = append(parts, "", remain)
	}
	if elapsed != "" {
		parts = append(parts, "", elapsed)
	}
	parts = append(parts, "", footer)
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

// ── Commands / messages ──────────────────────────────────────────────

// DrainProgressMsg carries one tick from a running drain. The root routes
// each into the Drain section and re-asks for the next via the channel.
type DrainProgressMsg struct {
	Event k8s.DrainEvent
}

// DrainStream is the typed alias for the event channel a drain emits on.
// Exported so the model can keep a reference between DrainStartedMsg and
// the final Done event — every NextDrainEvent reads from the same chan.
type DrainStream = chan k8s.DrainEvent

// DrainStartedMsg ships the cancel + channel pair the model needs to
// drive the progress modal.
type DrainStartedMsg struct {
	Node    string
	Cancel  context.CancelFunc
	Channel DrainStream
}

// CordonNode toggles cordon ↔ uncordon based on the desired state. The
// caller passes desiredSchedulable (i.e. true = uncordon). One verb in the
// API, one keybinding in the UI.
func CordonNode(ctx context.Context, mgr *k8s.Manager, contextName, name string, desiredSchedulable bool) tea.Cmd {
	return func() tea.Msg {
		action := "cordon"
		if desiredSchedulable {
			action = "uncordon"
		}
		client, ok := mgr.GetClient(contextName)
		if !ok {
			return ActionDoneMsg{Action: action, Subject: name, Err: fmt.Errorf("context not found")}
		}
		var err error
		if desiredSchedulable {
			err = k8s.UncordonNode(ctx, client.Clientset, name)
		} else {
			err = k8s.CordonNode(ctx, client.Clientset, name)
		}
		return ActionDoneMsg{Action: action, Subject: name, Err: err}
	}
}

// StartDrain kicks off the eviction loop in a goroutine and returns a
// DrainStartedMsg so the model can capture the cancel + channel. The
// goroutine emits DrainEvent into the channel until the drain ends; the
// model is responsible for pumping events through nextDrainEvent.
func StartDrain(parentCtx context.Context, mgr *k8s.Manager, contextName, nodeName string) tea.Cmd {
	return func() tea.Msg {
		client, ok := mgr.GetClient(contextName)
		if !ok {
			ch := make(chan k8s.DrainEvent, 1)
			ch <- k8s.DrainEvent{Phase: "done", Err: fmt.Errorf("context not found"), Done: true}
			close(ch)
			return DrainStartedMsg{Node: nodeName, Cancel: func() {}, Channel: ch}
		}
		// cancel is intentionally returned to the model — the user can hit
		// ctrl+c in the progress modal to abort. The model wires it onto
		// the Drain section; DrainNode's deferred-uncordon hook makes
		// abort safe (no half-cordoned-half-drained limbo).
		ctx, cancel := context.WithCancel(parentCtx) //nolint:gosec // ownership transfers via DrainStartedMsg
		ch := make(chan k8s.DrainEvent, 16)
		go func() {
			_ = k8s.DrainNode(ctx, client.Clientset, nodeName, k8s.DefaultDrainOptions(), ch)
		}()
		return DrainStartedMsg{Node: nodeName, Cancel: cancel, Channel: ch}
	}
}

// NextDrainEvent reads one event from the channel. The model loops on this
// until DrainProgressMsg.Event.Done is true.
func NextDrainEvent(ch DrainStream) tea.Cmd {
	return func() tea.Msg {
		e, ok := <-ch
		if !ok {
			// Channel closed without a Done event — treat as a graceful end.
			return DrainProgressMsg{Event: k8s.DrainEvent{Phase: "done", Done: true}}
		}
		return DrainProgressMsg{Event: e}
	}
}

// CountPodsOnNode is a small cmd for the drain confirm modal — fetches
// the eviction-eligible pod count for nodeName so the prompt can show
// "will evict N pods" honestly. Reports the count + an opaque error via
// PodsOnNodeMsg.
func CountPodsOnNode(ctx context.Context, mgr *k8s.Manager, contextName, nodeName string) tea.Cmd {
	return func() tea.Msg {
		client, ok := mgr.GetClient(contextName)
		if !ok {
			return PodsOnNodeMsg{Node: nodeName, Err: fmt.Errorf("context not found")}
		}
		n, err := k8s.PodsOnNode(ctx, client.Clientset, nodeName, k8s.DefaultDrainOptions())
		return PodsOnNodeMsg{Node: nodeName, Count: n, Err: err}
	}
}

// PodsOnNodeMsg ships the pod count back to the drain confirm modal.
type PodsOnNodeMsg struct {
	Node  string
	Count int
	Err   error
}
