package sections

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/YunusAndreasson/safina/internal/debug"
	"github.com/YunusAndreasson/safina/internal/k8s"
	"github.com/YunusAndreasson/safina/internal/tui/components"
	"github.com/YunusAndreasson/safina/internal/tui/textw"
	"github.com/YunusAndreasson/safina/internal/tui/tuictx"
)

// Activity is the live "what just happened?" tile at the middle of the
// right stack — a compact recent-events feed for whatever the tree cursor
// is on right now. Promotes the firehose that `e events` opens as a modal
// into an ambient display, on the same instinct as the Inspector: the
// most useful information an on-caller wants should be visible without
// pressing a key.
//
// Fetches are throttled (one in flight per selection, refreshed at most
// activityRefreshInterval) so navigating the tree doesn't hammer the API.
type Activity struct {
	// curKey is the "kind/ns/name" key of the selection the last successful
	// fetch was for. When SetSelection delivers a new key, we clear the
	// items and schedule a fresh load.
	curKey      string
	cancelFetch context.CancelFunc

	// kind/name/namespace echo the load target so a result Msg can be
	// matched to the current selection (if the user navigated away while a
	// fetch was in flight, we discard the stale result rather than rendering
	// events for a row that's no longer selected).
	kind, name, namespace string

	items []k8s.EventInfo
	// gate suppresses the spinner flash on selection-change when the event
	// fetch returns inside the LoadGate.ShowAfter window — common on small
	// clusters where ListEvents is essentially "instant".
	gate components.LoadGate
	err  error

	// lastFetch is when the last load completed (success or error). Used to
	// throttle the auto-refresh tick and to gate redundant SetSelection
	// re-fetches when nothing changed.
	lastFetch time.Time
}

// activityRefreshInterval is how often Activity will re-fetch events for
// the current selection on the auto-refresh tick. Events change less
// often than metrics, and the per-cluster API budget is shared, so this
// is intentionally coarser than the 5s tree-refresh cadence.
const activityRefreshInterval = 15 * time.Second

// ActivityLoadedMsg is the result envelope for the Activity tile's
// LoadActivity command. The (kind, name, namespace) echo lets the model
// drop stale results when the user has moved on.
type ActivityLoadedMsg struct {
	Context               string
	Kind, Name, Namespace string
	Events                []k8s.EventInfo
	Err                   error
}

// NewActivity constructs an empty tile.
func NewActivity() Activity { return Activity{} }

// Clear drops the tile's state — used when leaving a cluster or when the
// selection becomes a non-eventable kind.
func (a *Activity) Clear() {
	cancelSelectionFetch(&a.cancelFetch)
	a.curKey = ""
	a.kind, a.name, a.namespace = "", "", ""
	a.items = nil
	a.gate.Stop()
	a.err = nil
	a.lastFetch = time.Time{}
}

// Target returns the resource the tile is currently tracking, if any.
// Used by the auto-refresh tick to decide whether to issue a re-fetch.
func (a *Activity) Target() (kind, name, namespace string, ok bool) {
	if a.kind == "" {
		return "", "", "", false
	}
	return a.kind, a.name, a.namespace, true
}

// NeedsRefresh reports whether the tile would benefit from a re-fetch
// right now — true iff at least activityRefreshInterval has elapsed since
// the last successful fetch.
func (a *Activity) NeedsRefresh(now time.Time) bool {
	if a.kind == "" {
		return false
	}
	if a.lastFetch.IsZero() {
		return !a.gate.IsLoading()
	}
	return now.Sub(a.lastFetch) >= activityRefreshInterval && !a.gate.IsLoading()
}

// SetSelection points the tile at a new resource. Returns a load Cmd when
// a fetch is needed (different key, or stale). Returns nil when the same
// selection is still in cache and not yet due for refresh — that's the
// common case as the user moves the cursor up/down without changing the
// underlying resource (e.g. flipping focus between panels).
func (a *Activity) SetSelection(ctx context.Context, mgr *k8s.Manager, contextName string, kind, name, namespace string) tea.Cmd {
	if kind == "" || name == "" {
		a.Clear()
		return nil
	}
	key := kind + "/" + namespace + "/" + name
	if key == a.curKey {
		return nil
	}
	a.curKey = key
	a.kind, a.name, a.namespace = kind, name, namespace
	a.items = nil
	a.err = nil
	a.gate.Start()
	a.lastFetch = time.Time{}
	return selectionFetch(ctx, &a.cancelFetch, func(ctx context.Context) tea.Cmd { return loadActivity(ctx, mgr, contextName, kind, name, namespace) })
}

// Refresh re-issues a load against the current selection. No-op when
// nothing is selected. Idempotent if a load is already in flight.
func (a *Activity) Refresh(ctx context.Context, mgr *k8s.Manager, contextName string) tea.Cmd {
	if a.kind == "" || a.gate.IsLoading() {
		return nil
	}
	a.gate.Start()
	kind, name, namespace := a.kind, a.name, a.namespace
	return selectionFetch(ctx, &a.cancelFetch, func(ctx context.Context) tea.Cmd { return loadActivity(ctx, mgr, contextName, kind, name, namespace) })
}

// Apply ingests a load result. Stale results (for a different selection)
// are dropped — the caller already matched on kind/name/ns, this is just
// a defensive second gate.
func (a *Activity) Apply(msg ActivityLoadedMsg) {
	if msg.Kind != a.kind || msg.Name != a.name || msg.Namespace != a.namespace {
		return
	}
	a.gate.Stop()
	a.lastFetch = time.Now()
	a.err = msg.Err
	a.items = msg.Events
}

// loadActivity is the inner Cmd builder — equivalent to LoadEvents but
// wrapped in an ActivityLoadedMsg envelope so the modal Events flow stays
// untouched.
func loadActivity(ctx context.Context, mgr *k8s.Manager, contextName, kind, name, namespace string) tea.Cmd {
	return func() tea.Msg {
		defer debug.Track("activity.events", "context", contextName, "kind", kind, "name", name)()
		out := ActivityLoadedMsg{Context: contextName, Kind: kind, Name: name, Namespace: namespace}
		client, ok := mgr.GetClient(contextName)
		if !ok {
			out.Err = errContextNotFound
			return out
		}
		evts, err := k8s.ListEvents(ctx, client.Clientset, namespace, kind, name)
		out.Events = evts
		out.Err = err
		return out
	}
}

// View renders the tile. Empty state when nothing is selected, a one-line
// loading row on first fetch, or a compact list of recent events.
func (a *Activity) View(c *tuictx.Ctx, w, h int) string {
	t := c.Theme
	innerW := components.PanelContentWidth(w)
	innerH := h - 2
	if innerH < 1 {
		innerH = 1
	}

	body := a.renderBody(c, innerW, innerH)
	title := "Recent events"
	return components.PageFrame(t, title, w, h, body, t.P.FGFade, false)
}

func (a *Activity) renderBody(c *tuictx.Ctx, innerW, innerH int) string {
	t := c.Theme
	switch {
	case a.kind == "":
		return t.Idle.Render("No event preview for this selection.")
	case a.gate.Visible() && len(a.items) == 0:
		return t.Idle.Render(c.SpinnerView() + " loading events…")
	case a.err != nil:
		return lipgloss.NewStyle().Foreground(t.P.Red).
			Render(components.TruncPad("✗ "+a.err.Error(), innerW))
	case len(a.items) == 0:
		return t.Idle.Render("no recent events")
	}

	// Keep time and severity aligned while allowing event text to wrap.
	ageW := 5
	for _, e := range a.items {
		ageW = max(ageW, textw.StringWidth(e.Age))
	}
	ageW = min(ageW, max(1, innerW-4))
	lines := make([]string, 0, innerH)
	for _, e := range a.items {
		if len(lines) >= innerH {
			break
		}
		color := t.P.FGFade
		if e.Type == "Warning" {
			color = t.P.Red
		}
		message := strings.Join(strings.Fields(e.Message), " ")
		if e.Count > 1 {
			message = fmt.Sprintf("%s (×%d)", message, e.Count)
		}
		prefix := t.Meta.Render(lipgloss.NewStyle().Width(ageW).Align(lipgloss.Right).Render(ansi.Truncate(e.Age, ageW, "…"))) + " " + lipgloss.NewStyle().Foreground(color).Render("●") + " "
		text := t.NameStrong.Render(e.Reason) + " " + t.NameDim.Render(message)
		wrapped := strings.Split(ansi.Wrap(prefix+text, max(1, innerW), ""), "\n")
		if innerW >= 40 {
			// Continuations belong under event text, leaving the time gutter clear.
			prefixW := textw.StringWidth(prefix)
			wrapped = strings.Split(ansi.Wrap(text, innerW-prefixW, ""), "\n")
			for i := range wrapped {
				if i == 0 {
					wrapped[i] = prefix + wrapped[i]
				} else {
					wrapped[i] = strings.Repeat(" ", prefixW) + wrapped[i]
				}
			}
		}
		remaining := innerH - len(lines)
		if len(wrapped) > remaining {
			// Signal that the final visible event continues beyond the tile.
			wrapped = wrapped[:remaining]
			wrapped[remaining-1] = ansi.Truncate(wrapped[remaining-1], max(1, innerW-1), "") + "…"
		}
		lines = append(lines, wrapped...)
	}
	return strings.Join(lines, "\n")
}
