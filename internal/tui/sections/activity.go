package sections

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/yunus/safina/internal/debug"
	"github.com/yunus/safina/internal/k8s"
	"github.com/yunus/safina/internal/tui/components"
	"github.com/yunus/safina/internal/tui/theme"
	"github.com/yunus/safina/internal/tui/tuictx"
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
	curKey string

	// kind/name/namespace echo the load target so a result Msg can be
	// matched to the current selection (if the user navigated away while a
	// fetch was in flight, we discard the stale result rather than rendering
	// events for a row that's no longer selected).
	kind, name, namespace string

	items   []k8s.EventInfo
	loading bool
	err     error

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
	Kind, Name, Namespace string
	Events                []k8s.EventInfo
	Err                   error
}

// NewActivity constructs an empty tile.
func NewActivity() Activity { return Activity{} }

// Clear drops the tile's state — used when leaving a cluster or when the
// selection becomes a non-eventable kind.
func (a *Activity) Clear() {
	a.curKey = ""
	a.kind, a.name, a.namespace = "", "", ""
	a.items = nil
	a.loading = false
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
		return !a.loading
	}
	return now.Sub(a.lastFetch) >= activityRefreshInterval && !a.loading
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
	a.loading = true
	a.lastFetch = time.Time{}
	return loadActivity(ctx, mgr, contextName, kind, name, namespace)
}

// Refresh re-issues a load against the current selection. No-op when
// nothing is selected. Idempotent if a load is already in flight.
func (a *Activity) Refresh(ctx context.Context, mgr *k8s.Manager, contextName string) tea.Cmd {
	if a.kind == "" || a.loading {
		return nil
	}
	a.loading = true
	return loadActivity(ctx, mgr, contextName, a.kind, a.name, a.namespace)
}

// Apply ingests a load result. Stale results (for a different selection)
// are dropped — the caller already matched on kind/name/ns, this is just
// a defensive second gate.
func (a *Activity) Apply(msg ActivityLoadedMsg) {
	if msg.Kind != a.kind || msg.Name != a.name || msg.Namespace != a.namespace {
		return
	}
	a.loading = false
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
		out := ActivityLoadedMsg{Kind: kind, Name: name, Namespace: namespace}
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
	title := "Activity"
	if a.kind != "" {
		title = "Activity · " + stringsTitle(a.kind) + " · " + a.name
		title = truncate(title, innerW)
	}
	return components.PanelWithAccent(t, title, w, h, body, false, t.P.Cyan, t.P.Dim)
}

func (a *Activity) renderBody(c *tuictx.Ctx, innerW, innerH int) string {
	t := c.Theme
	switch {
	case a.kind == "":
		return t.Idle.Render("nothing selected")
	case a.loading && len(a.items) == 0:
		return t.Idle.Render(c.Spinner.View() + " loading events…")
	case a.err != nil:
		return lipgloss.NewStyle().Foreground(t.P.Red).Render("✗ " + a.err.Error())
	case len(a.items) == 0:
		return t.Idle.Render("no recent events")
	}

	// Layout per row: age (right, 5 cells) · type-dot (1) · reason (12)
	// · message (rest). Compact — three or four events should comfortably
	// fit in 5-6 rows. Type is reduced to a single colored dot because
	// "Normal" is overwhelmingly the most common value; the dot's color
	// alone tells the operator whether the row is a warning.
	const ageW, dotW, reasonW = 5, 1, 14
	msgW := innerW - ageW - dotW - reasonW - 3
	if msgW < 10 {
		msgW = 10
	}

	rowCount := innerH
	if rowCount > len(a.items) {
		rowCount = len(a.items)
	}

	lines := make([]string, 0, rowCount)
	for i := 0; i < rowCount; i++ {
		e := a.items[i]
		lines = append(lines, a.renderRow(t, e, ageW, dotW, reasonW, msgW))
	}
	return strings.Join(lines, "\n")
}

func (a *Activity) renderRow(t *theme.Theme, e k8s.EventInfo, ageW, dotW, reasonW, msgW int) string {
	age := lipgloss.NewStyle().Foreground(t.P.Dim).Width(ageW).AlignHorizontal(lipgloss.Right).
		Render(components.TruncPad(e.Age, ageW))

	dotColor := t.P.FGFade
	switch e.Type {
	case "Warning":
		dotColor = t.P.Red
	case "Normal":
		dotColor = t.P.Green
	}
	dot := lipgloss.NewStyle().Foreground(dotColor).Width(dotW).Render("●")

	reason := lipgloss.NewStyle().Foreground(t.P.FG).Bold(true).Width(reasonW).
		Render(components.TruncPad(e.Reason, reasonW))

	msgText := e.Message
	if e.Count > 1 {
		msgText = fmt.Sprintf("%s (×%d)", msgText, e.Count)
	}
	msg := lipgloss.NewStyle().Foreground(t.P.FGFade).Width(msgW).
		Render(components.TruncPad(msgText, msgW))

	return age + " " + dot + " " + reason + " " + msg
}
