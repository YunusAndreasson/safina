package sections

import (
	"context"
	"errors"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/YunusAndreasson/safina/internal/k8s"
	"github.com/YunusAndreasson/safina/internal/tui/components"
	"github.com/YunusAndreasson/safina/internal/tui/theme"
	"github.com/YunusAndreasson/safina/internal/tui/tuictx"
)

// Connected is the right-stack tile that shows what the focused resource is
// wired up to — its forward references (uses) and reverse references (used
// by). Replaces the chat tile in the right stack; chat now lives behind a
// full-screen modal.
//
// The tile is kind-aware (drives off the tree selection) and caches per
// (kind, ns, name) so cursor movement up a list of pods stays free of
// repeated round-trips after the first visit.
type Connected struct {
	Focused                           bool
	PanelTop, PanelHeight, PanelWidth int
	cursor                            int
	rowOffset                         int
	renderedRefs                      []k8s.ConnectedRef
	ref                               NodeRef
	cancelFetch                       context.CancelFunc
	snap                              *connectedSnapshot
	cache                             map[string]*k8s.Connections
	cacheRecency                      cacheRecency
}

// connectedSnapshot is the kind-erased payload the render path consumes.
// title and subtitle frame the panel; rows are grouped by direction.
type connectedSnapshot struct {
	title    string
	subtitle string
	uses     []k8s.ConnectedRef
	usedBy   []k8s.ConnectedRef
	loading  bool
	// notApplicable is true for selections that don't have a useful
	// connection graph (cluster picker, folds, nodes, etc.) — the tile
	// renders a short "—" hint instead of empty groups.
	notApplicable bool
	err           error
}

// NewConnected constructs an empty Connected tile.
func NewConnected() Connected {
	return Connected{cache: make(map[string]*k8s.Connections)}
}

// Clear drops the snapshot AND the cache. Called on cluster switch / picker
// pop so stale data from another cluster doesn't bleed in.
func (c *Connected) Clear() {
	cancelSelectionFetch(&c.cancelFetch)
	c.snap = nil
	c.cursor = 0
	c.Hide()
	c.ref = NodeRef{}
	c.cache = make(map[string]*k8s.Connections)
	c.cacheRecency = cacheRecency{}
}

// SetSelection rebuilds the base snapshot for the current tree cursor and
// returns a tea.Cmd to fetch connections when the cache misses. Returns
// nil for kinds that have no connections path.
func (c *Connected) SetSelection(ctx context.Context, mgr *k8s.Manager, contextName string, tr *Tree) tea.Cmd {
	ref := tr.SelectedRef()
	if ref != c.ref {
		cancelSelectionFetch(&c.cancelFetch)
	}
	if ref.Kind == "" {
		c.snap = nil
		c.ref = ref
		return nil
	}
	sameRef := c.ref == ref
	if !sameRef {
		c.cursor = 0
	}
	c.ref = ref

	if !connectedSupportsKind(ref) {
		hint := "no connections to show"
		if IsResourceGroup(ref.Kind) {
			hint = "select a resource to see its connections"
		}
		c.snap = &connectedSnapshot{
			title:         connectedTitle(ref),
			subtitle:      hint,
			notApplicable: true,
		}
		return nil
	}

	key := connectedCacheKey(ref)
	cached := c.cache[key]
	if cached != nil {
		c.cacheRecency.touch(key)
	}
	c.snap = c.buildSnapshot(ref, cached)

	if sameRef || mgr == nil || contextName == "" {
		return nil
	}
	if cached != nil {
		return nil
	}
	c.snap.loading = true
	return selectionFetch(ctx, &c.cancelFetch, func(ctx context.Context) tea.Cmd {
		return LoadConnections(ctx, mgr, contextName, ref.Kind, ref.SubKind, ref.Namespace, ref.Name)
	})
}

// Apply ingests a load result, caches it, and re-renders the snapshot if
// the result is still for the current selection.
func (c *Connected) Apply(msg ConnectedMsg) {
	if errors.Is(msg.Err, context.Canceled) || errors.Is(msg.Err, context.DeadlineExceeded) {
		return
	}
	key := connectedCacheKeyParts(msg.Kind, msg.SubKind, msg.Namespace, msg.Name)
	conns := msg.Connections
	c.cache[key] = &conns
	c.cacheRecency.touch(key)
	pruneCache(c.cache, &c.cacheRecency, maxDetailCacheEntries, func(key string) bool { return key == connectedCacheKey(c.ref) })
	cur := c.ref
	if cur.Kind == msg.Kind && cur.SubKind == msg.SubKind && cur.Namespace == msg.Namespace && cur.Name == msg.Name {
		selected, hadSelection := c.Selected()
		c.snap = c.buildSnapshot(cur, &conns)
		c.snap.err = msg.Err
		c.cursor = clamp(c.cursor, c.Count()-1)
		if hadSelection {
			for n, ref := range append(append([]k8s.ConnectedRef(nil), c.snap.uses...), c.snap.usedBy...) {
				if ref.Kind == selected.Kind && ref.SubKind == selected.SubKind && ref.Namespace == selected.Namespace && ref.Name == selected.Name {
					c.cursor = n
					break
				}
			}
		}
	}
}

// View renders the tile.
func (c *Connected) View(ctx *tuictx.Ctx, w, h int) string {
	t := ctx.Theme
	if c.snap == nil {
		body := t.Idle.Render("Select a resource to see connections.")
		return components.PanelWithAccent(t, "Connected", w, h, body, false, t.P.Dim, t.P.Dim)
	}
	body := c.renderBody(ctx, components.PanelContentWidth(w), h-2)
	c.PanelHeight, c.PanelWidth = h, w
	title := "Connected"
	if c.snap.loading {
		title = "Connected · " + ctx.Spinner.View() + " loading…"
	}
	return components.PanelWithAccent(t, title, w, h, body, false, t.P.Pink, t.P.Pink)
}

func (c *Connected) renderBody(ctx *tuictx.Ctx, innerW, innerH int) string {
	t := ctx.Theme
	s := c.snap

	c.renderedRefs = nil
	var parts []string
	if s.subtitle != "" {
		parts = append(parts, t.DimTxt.Render(truncate(s.subtitle, innerW)))
	}

	c.rowOffset = len(parts)
	if !s.notApplicable {
		parts = append(parts, c.renderGroups(t, innerW, max(0, innerH-len(parts)))...)
	}

	if s.err != nil {
		parts = append(parts, "")
		parts = append(parts, lipgloss.NewStyle().Foreground(t.P.Red).
			Render(truncate("✗ "+s.err.Error(), innerW)))
	}

	if len(parts) > innerH {
		parts = parts[:innerH]
	}
	return strings.Join(parts, "\n")
}

func (c *Connected) renderGroups(t *theme.Theme, innerW, rows int) []string {
	s := c.snap
	c.renderedRefs = nil
	if rows < 1 {
		return nil
	}
	if len(s.uses) == 0 && len(s.usedBy) == 0 {
		if !s.loading && s.err == nil {
			return []string{t.Hint.Render(truncate("no references found", innerW))}
		}
		return nil
	}
	if rows == 1 && !c.Focused && len(s.uses) > 0 && len(s.usedBy) > 0 {
		return []string{t.DimTxt.Render(truncate(fmt.Sprintf("Uses %d · Used by %d", len(s.uses), len(s.usedBy)), innerW))}
	}
	usesRows := min(len(s.uses), rows)
	if len(s.usedBy) > 0 {
		usesRows = min(usesRows, (rows+1)/2)
	}
	usedByRows := min(len(s.usedBy), rows-usesRows)
	usesRows = min(len(s.uses), rows-usedByRows)
	if rows == 1 && c.Focused && c.cursor >= len(s.uses) {
		usesRows, usedByRows = 0, min(1, len(s.usedBy))
	}
	var lines []string
	groupOffset := 0
	for _, group := range []struct {
		label string
		refs  []k8s.ConnectedRef
		limit int
	}{
		{"Uses", s.uses, usesRows}, {"Used by", s.usedBy, usedByRows},
	} {
		start := 0
		if c.Focused && c.cursor >= groupOffset && c.cursor < groupOffset+len(group.refs) {
			start = max(0, c.cursor-groupOffset-group.limit+1)
		}
		for n := range group.limit {
			label := group.label
			if hidden := len(group.refs) - group.limit; hidden > 0 {
				label += fmt.Sprintf(" (+%d)", hidden)
			}
			prefix := label + " "
			if n > 0 {
				prefix = strings.Repeat(" ", lipgloss.Width(prefix))
			}
			ref := group.refs[start+n]
			c.renderedRefs = append(c.renderedRefs, ref)
			marker := ""
			if c.Focused {
				marker = "  "
				if c.cursor == groupOffset+start+n {
					marker = "› "
				}
			}
			bg := lipgloss.NewStyle()
			if c.Focused && c.cursor == groupOffset+start+n {
				bg = t.Selected
			}
			row := bg.Foreground(t.P.Dim).Render(marker+prefix) + renderConnectedRow(t, ref, max(1, innerW-lipgloss.Width(marker+prefix)), bg)
			row += bg.Render(strings.Repeat(" ", max(0, innerW-lipgloss.Width(row))))
			lines = append(lines, truncate(row, innerW))
		}
		groupOffset += len(group.refs)
	}
	return lines
}

// Hide invalidates the mouse geometry when this section is not rendered.
func (c *Connected) Hide() { c.PanelHeight = 0; c.renderedRefs = nil }

func (c *Connected) Count() int {
	if c.snap == nil {
		return 0
	}
	return len(c.snap.uses) + len(c.snap.usedBy)
}
func (c *Connected) MoveCursor(delta int) { c.cursor = clamp(c.cursor+delta, c.Count()-1) }
func (c *Connected) Selected() (k8s.ConnectedRef, bool) {
	if c.snap == nil || c.Count() == 0 {
		return k8s.ConnectedRef{}, false
	}
	c.cursor = clamp(c.cursor, c.Count()-1)
	if c.cursor < len(c.snap.uses) {
		return c.snap.uses[c.cursor], true
	}
	return c.snap.usedBy[c.cursor-len(c.snap.uses)], true
}

// Hit resolves an interior body row, excluding the panel border.
func (c *Connected) Hit(row int) (k8s.ConnectedRef, bool) {
	row -= c.rowOffset
	if row < 0 || row >= len(c.renderedRefs) {
		return k8s.ConnectedRef{}, false
	}
	return c.renderedRefs[row], true
}

// renderConnectedRow names the resource kind explicitly in this mixed list.
func renderConnectedRow(t *theme.Theme, r k8s.ConnectedRef, innerW int, backgrounds ...lipgloss.Style) string {
	bg := lipgloss.NewStyle()
	if len(backgrounds) > 0 {
		bg = backgrounds[0]
	}
	kind := r.Kind
	if r.SubKind != "" {
		kind = r.SubKind
	}
	nameStyle := bg.Foreground(t.P.FG)
	reasonStyle := bg.Foreground(t.P.FGFade)
	kindStyle := bg.Foreground(t.ResourceKindColor(kind))

	left := kindStyle.Render(kind) + " " + nameStyle.Render(r.Name)
	if r.Reason != "" {
		left += "  " + reasonStyle.Render("("+r.Reason+")")
	}
	return truncate(left, innerW)
}

// ── builders / helpers ──────────────────────────────────────────────

// connectedSupportsKind reports whether the row's kind has a connection
// graph worth fetching. Folds, contexts, namespaces themselves, etc.
// return false — the tile shows "no connections" instead of an empty fetch.
func connectedSupportsKind(ref NodeRef) bool {
	if ref.Kind == k8s.KindGenericResource {
		return ref.SubKind == "ServiceAccount" || ref.SubKind == "HorizontalPodAutoscaler"
	}
	switch ref.Kind {
	case k8s.KindApp, k8s.KindPod, k8s.KindService, k8s.KindIngress,
		k8s.KindConfigMap, k8s.KindSecret, k8s.KindHPA, k8s.KindServiceAccount:
		return true
	}
	return false
}

// connectedTitle is the panel headline for kinds without a fetched payload —
// the same "Kind · Name" pattern the inspector uses.
func connectedTitle(ref NodeRef) string {
	return selectionTitle(ref)
}

func (c *Connected) buildSnapshot(ref NodeRef, conns *k8s.Connections) *connectedSnapshot {
	s := &connectedSnapshot{title: connectedTitle(ref)}
	if conns != nil {
		s.uses = conns.Uses
		s.usedBy = conns.UsedBy
	}
	switch ref.Kind {
	case k8s.KindConfigMap, k8s.KindSecret, k8s.KindServiceAccount:
		// These three are only meaningful via reverse refs.
		s.subtitle = "who consumes me"
	case k8s.KindHPA:
		s.subtitle = "what I scale"
	case k8s.KindIngress:
		s.subtitle = "backend services"
	default:
		s.subtitle = ""
	}
	return s
}

func connectedCacheKey(ref NodeRef) string {
	return connectedCacheKeyParts(ref.Kind, ref.SubKind, ref.Namespace, ref.Name)
}

func connectedCacheKeyParts(kind, subKind, namespace, name string) string {
	return kind + "/" + subKind + "/" + namespace + "/" + name
}
