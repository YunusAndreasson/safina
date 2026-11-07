package sections

import (
	"context"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/yunus/safina/internal/k8s"
	"github.com/yunus/safina/internal/tui/components"
	"github.com/yunus/safina/internal/tui/theme"
	"github.com/yunus/safina/internal/tui/tuictx"
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
	ref   NodeRef
	snap  *connectedSnapshot
	cache map[string]*k8s.Connections
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
	c.snap = nil
	c.ref = NodeRef{}
	c.cache = make(map[string]*k8s.Connections)
}

// SetSelection rebuilds the base snapshot for the current tree cursor and
// returns a tea.Cmd to fetch connections when the cache misses. Returns
// nil for kinds that have no connections path.
func (c *Connected) SetSelection(ctx context.Context, mgr *k8s.Manager, contextName string, tr *Tree) tea.Cmd {
	ref := tr.SelectedRef()
	if ref.Kind == "" {
		c.snap = nil
		c.ref = ref
		return nil
	}
	sameRef := c.ref == ref
	c.ref = ref

	if !connectedSupportsKind(ref) {
		hint := "no connections to show"
		if isResourceGroup(ref.Kind) {
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
	c.snap = c.buildSnapshot(ref, cached)

	if sameRef || mgr == nil || contextName == "" {
		return nil
	}
	if cached != nil {
		return nil
	}
	c.snap.loading = true
	return LoadConnections(ctx, mgr, contextName, ref.Kind, ref.SubKind, ref.Namespace, ref.Name)
}

// Apply ingests a load result, caches it, and re-renders the snapshot if
// the result is still for the current selection.
func (c *Connected) Apply(msg ConnectedMsg) {
	key := connectedCacheKeyParts(msg.Kind, msg.SubKind, msg.Namespace, msg.Name)
	conns := msg.Connections
	c.cache[key] = &conns
	cur := c.ref
	if cur.Kind == msg.Kind && cur.Namespace == msg.Namespace && cur.Name == msg.Name {
		c.snap = c.buildSnapshot(cur, &conns)
		c.snap.err = msg.Err
	}
}

// View renders the tile.
func (c *Connected) View(ctx *tuictx.Ctx, w, h int) string {
	t := ctx.Theme
	if c.snap == nil {
		body := t.Idle.Render("nothing selected")
		return components.PanelWithAccent(t, "Connected", w, h, body, false, t.P.Dim, t.P.Dim)
	}
	body := c.renderBody(ctx, components.PanelContentWidth(w), h-2)
	title := "Connected"
	if c.snap.loading {
		title = "Connected · " + ctx.Spinner.View() + " loading…"
	}
	return components.PanelWithAccent(t, title, w, h, body, false, t.P.Pink, t.P.Pink)
}

func (c *Connected) renderBody(ctx *tuictx.Ctx, innerW, innerH int) string {
	t := ctx.Theme
	s := c.snap

	parts := []string{
		t.NameStrong.Render(truncate(s.title, innerW)),
	}
	if s.subtitle != "" {
		parts = append(parts, t.DimTxt.Render(truncate(s.subtitle, innerW)))
	}

	if !s.notApplicable {
		parts = append(parts, c.renderGroups(t, innerW)...)
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

func (c *Connected) renderGroups(t *theme.Theme, innerW int) []string {
	s := c.snap
	parts := []string{}

	if len(s.uses) > 0 {
		parts = append(parts, "")
		parts = append(parts, t.DimBold.Render("USES"))
		for _, r := range s.uses {
			parts = append(parts, renderConnectedRow(t, r, innerW))
		}
	}
	if len(s.usedBy) > 0 {
		parts = append(parts, "")
		parts = append(parts, t.DimBold.Render("USED BY"))
		for _, r := range s.usedBy {
			parts = append(parts, renderConnectedRow(t, r, innerW))
		}
	}
	if len(s.uses) == 0 && len(s.usedBy) == 0 && !s.loading && s.err == nil {
		parts = append(parts, "")
		parts = append(parts, t.Hint.Render(truncate("— no references found —", innerW)))
	}
	return parts
}

// renderConnectedRow paints one reference as `glyph · name  (reason)`.
// Glyph mirrors the tree's kindGlyph so the user sees the same visual
// shorthand on both sides of the screen.
func renderConnectedRow(t *theme.Theme, r k8s.ConnectedRef, innerW int) string {
	glyph := kindGlyph(r.Kind, r.SubKind)
	if glyph == "" {
		glyph = "·"
	}
	nameStyle := lipgloss.NewStyle().Foreground(t.P.FG)
	reasonStyle := t.Hint
	glyphStyle := lipgloss.NewStyle().Foreground(t.P.Dim)

	left := glyphStyle.Render(glyph) + " " + nameStyle.Render(r.Name)
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
