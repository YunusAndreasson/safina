package sections

import (
	"sort"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/sahilm/fuzzy"

	"github.com/yunus/safina/internal/config"
	"github.com/yunus/safina/internal/k8s"
	"github.com/yunus/safina/internal/tui/components"
	"github.com/yunus/safina/internal/tui/tuictx"
)

// Browser is the Resource Browser modal. It shows every resource kind
// the cluster exposes plus the special synthetic "Workloads" group, and
// lets the operator toggle which appear as folds in the tree.
//
// Modeled after Palette: same fuzzy-filter machinery, same modal styling,
// same keyboard shape — but the rows are checkboxes and the result is a
// new []config.ResourceRef instead of a navigation Path.
type Browser struct {
	open    bool
	query   string
	entries []browserEntry // full corpus, captured at Open()
	matches []int          // indexes into entries, ranked
	indices [][]int        // matched-char offsets per match — highlight
	cursor  int            // index into matches
}

// browserEntry is one toggleable row in the modal.
type browserEntry struct {
	Ref     config.ResourceRef
	Display string // plural label ("Workloads", "PrometheusRules", "ConfigMaps")
	Group   string // for the right-hand suffix ("monitoring.coreos.com/v1", "core/v1")
	Enabled bool
}

// NewBrowser builds an empty browser. Open() seeds it.
func NewBrowser() Browser { return Browser{} }

// IsOpen reports whether the modal is currently displayed. The view
// layer uses this to overlay the modal over the tree.
func (b *Browser) IsOpen() bool { return b.open }

// Open seeds the browser with the discovered kinds + the synthetic
// Workloads entry. enabled marks the user's current selection so the
// "already on" rows show as [x] from the start.
func (b *Browser) Open(kinds []k8s.ResourceKind, enabled []config.ResourceRef) {
	b.entries = buildBrowserEntries(kinds, enabled)
	b.query = ""
	b.cursor = 0
	b.open = true
	b.recomputeMatches()
}

// Close dismisses the modal without saving — the model handles the
// "cancel" path. Confirm() is the save path.
func (b *Browser) Close() { b.open = false }

// SetQuery replaces the query and reranks matches.
func (b *Browser) SetQuery(q string) {
	b.query = q
	b.cursor = 0
	b.recomputeMatches()
}

// Query returns the current query string for the input-line render.
func (b *Browser) Query() string { return b.query }

// CursorUp / CursorDown move within the result list.
func (b *Browser) CursorUp() {
	if b.cursor > 0 {
		b.cursor--
	}
}

func (b *Browser) CursorDown() {
	if b.cursor < len(b.matches)-1 {
		b.cursor++
	}
}

// Toggle flips the Enabled state of the highlighted entry. Space.
func (b *Browser) Toggle() {
	if b.cursor < 0 || b.cursor >= len(b.matches) {
		return
	}
	idx := b.matches[b.cursor]
	b.entries[idx].Enabled = !b.entries[idx].Enabled
}

// Confirm closes the modal and returns the chosen ResourceRefs in
// display order so folds render in a stable order across sessions.
func (b *Browser) Confirm() []config.ResourceRef {
	b.open = false
	out := make([]config.ResourceRef, 0, len(b.entries))
	for _, e := range b.entries {
		if e.Enabled {
			out = append(out, e.Ref)
		}
	}
	return out
}

// buildBrowserEntries composes the corpus from the synthetic Workloads
// row plus every GVR discovery reports for the cluster. This is the
// "only what actually exists on the cluster" rule — we don't show kinds
// we can't list. Order: Workloads first; then core; then non-core
// built-in groups (apps/batch/networking/…); then CRDs alphabetised by
// group. The user's already-enabled selections light up as [x] via the
// enabledSet lookup.
//
// When discovery hasn't landed yet (modal opened before the API
// returned), `kinds` is empty and only the Workloads row appears. The
// DiscoveryLoadedMsg handler re-opens the modal once results arrive so
// the corpus expands without the operator having to re-trigger.
func buildBrowserEntries(kinds []k8s.ResourceKind, enabled []config.ResourceRef) []browserEntry {
	enabledSet := make(map[string]bool, len(enabled))
	for _, e := range enabled {
		enabledSet[refKey(e)] = true
	}

	out := make([]browserEntry, 0, len(kinds)+1)

	// Workloads — safina-defined synthetic. Not a real GVR, so it
	// doesn't depend on discovery. Always offered.
	out = append(out, browserEntry{
		Ref:     config.ResourceRef{Synthetic: "Workloads"},
		Display: "Workloads",
		Group:   "deployments · statefulsets · daemonsets · cronjobs · jobs",
		Enabled: enabledSet[refKey(config.ResourceRef{Synthetic: "Workloads"})],
	})

	// Sort: core first, then non-CRD built-in groups, then CRDs. Within
	// each tier alphabetise by (group, kind) so the same cluster always
	// renders the same order across sessions.
	sorted := append([]k8s.ResourceKind(nil), kinds...)
	sort.Slice(sorted, func(i, j int) bool {
		ti, tj := tier(sorted[i]), tier(sorted[j])
		if ti != tj {
			return ti < tj
		}
		if sorted[i].Group != sorted[j].Group {
			return sorted[i].Group < sorted[j].Group
		}
		return sorted[i].Kind < sorted[j].Kind
	})
	for _, k := range sorted {
		ref := config.ResourceRef{Group: k.Group, Version: k.Version, Resource: k.Resource}
		out = append(out, browserEntry{
			Ref:     ref,
			Display: kindDisplayName(k),
			Group:   groupVersionLabel(k.Group, k.Version),
			Enabled: enabledSet[refKey(ref)],
		})
	}
	return out
}

// tier groups discovered kinds into three visual bands: core (0),
// built-in non-core (1), and CRD (2). Used as the primary sort key in
// buildBrowserEntries so the modal reads "familiar at the top, exotic
// at the bottom" on every cluster.
func tier(k k8s.ResourceKind) int {
	switch {
	case k.Group == "":
		return 0
	case !k.IsCRD:
		return 1
	default:
		return 2
	}
}

// kindDisplayName picks the operator-friendly label: the singular Kind
// from discovery if known, otherwise a title-cased resource name. The
// row already shows the GVR underneath, so we don't need to disambiguate
// further.
func kindDisplayName(k k8s.ResourceKind) string {
	if k.Kind != "" {
		return k.Kind
	}
	return titleCasePlural(k.Resource)
}

// groupVersionLabel renders the right-hand suffix on a row. Core (empty
// group) shows as "core/v1" so the column always has a value.
func groupVersionLabel(group, version string) string {
	if group == "" {
		return "core/" + version
	}
	return group + "/" + version
}

// refKey canonicalises a ResourceRef so two refs that identify the
// same resource collide in the enabledSet map. Synthetic entries
// (Workloads) use the Synthetic field directly.
func refKey(r config.ResourceRef) string {
	if r.Synthetic != "" {
		return "synthetic:" + r.Synthetic
	}
	return gvrKey(r.Group, r.Version, r.Resource)
}

// recomputeMatches reranks entries against the current query. Empty
// query → entries in their natural order (defaults first).
func (b *Browser) recomputeMatches() {
	if b.query == "" {
		b.matches = make([]int, len(b.entries))
		for i := range b.entries {
			b.matches[i] = i
		}
		b.indices = make([][]int, len(b.entries))
		return
	}
	corpus := browserCorpus{entries: b.entries}
	res := fuzzy.FindFrom(b.query, corpus)
	b.matches = make([]int, 0, len(res))
	b.indices = make([][]int, 0, len(res))
	for _, r := range res {
		b.matches = append(b.matches, r.Index)
		b.indices = append(b.indices, append([]int(nil), r.MatchedIndexes...))
	}
}

type browserCorpus struct{ entries []browserEntry }

func (c browserCorpus) String(i int) string {
	return c.entries[i].Display + " " + c.entries[i].Group
}
func (c browserCorpus) Len() int { return len(c.entries) }

// View renders the browser as a centered modal. Same envelope as the
// palette so the two modals feel like one family.
func (b *Browser) View(c *tuictx.Ctx, termW, termH int) string {
	t := c.Theme

	w := termW * 80 / 100
	if w > 96 {
		w = 96
	}
	if w < 60 {
		w = 60
	}
	innerW := w - 4

	maxRows := termH * 75 / 100
	if maxRows > 28 {
		maxRows = 28
	}
	listRows := maxRows - 6
	if listRows < 6 {
		listRows = 6
	}

	prompt := lipgloss.NewStyle().Foreground(t.P.Cyan).Bold(true).Render("›")
	queryStyle := lipgloss.NewStyle().Foreground(t.P.FG)
	cursor := lipgloss.NewStyle().Foreground(t.P.FG).Reverse(true).Render(" ")
	input := prompt + " " + queryStyle.Render(b.query) + cursor

	header := lipgloss.NewStyle().Foreground(t.P.Dim).Italic(true).Render(
		"every resource the cluster exposes — toggle which appear as folds in your tree",
	)

	list := b.renderList(c, innerW, listRows)

	footer := components.RenderHintRow(t, []components.KeyDesc{
		{Key: "space", Desc: "toggle"},
		{Key: "↵", Desc: "save"},
		{Key: "esc", Desc: "cancel"},
	})

	body := lipgloss.JoinVertical(lipgloss.Left,
		input,
		header,
		"",
		list,
		"",
		footer,
	)

	panel := t.Modal.Padding(1, 2).Width(w).Render(body)
	return lipgloss.Place(termW, termH, lipgloss.Center, lipgloss.Center, panel,
		components.ModalBackdrop(t)...)
}

// renderList renders the matches as a window into the corpus, centred
// around the cursor. Each row is "[x] DisplayName            group/v".
func (b *Browser) renderList(c *tuictx.Ctx, w, rows int) string {
	t := c.Theme
	if len(b.matches) == 0 {
		return lipgloss.NewStyle().Foreground(t.P.Dim).Italic(true).
			Render("  no matches")
	}
	start := 0
	end := len(b.matches)
	if end > rows {
		half := rows / 2
		start = b.cursor - half
		if start < 0 {
			start = 0
		}
		end = start + rows
		if end > len(b.matches) {
			end = len(b.matches)
			start = end - rows
		}
	}
	out := make([]string, 0, end-start)
	for i := start; i < end; i++ {
		entry := b.entries[b.matches[i]]
		var hits []int
		if i < len(b.indices) {
			hits = b.indices[i]
		}
		out = append(out, renderBrowserRow(c, entry, hits, w, i == b.cursor))
	}
	return strings.Join(out, "\n")
}

func renderBrowserRow(c *tuictx.Ctx, e browserEntry, hits []int, w int, selected bool) string {
	t := c.Theme
	var bg lipgloss.Style
	if selected {
		bg = lipgloss.NewStyle().Background(t.P.BlueDark).Foreground(t.P.FG)
	} else {
		bg = lipgloss.NewStyle()
	}
	cursor := "  "
	if selected {
		cursor = bg.Foreground(t.P.Cyan).Bold(true).Render("▌ ")
	}
	var check string
	if e.Enabled {
		check = bg.Foreground(t.P.Green).Bold(true).Render("[x] ")
	} else {
		check = bg.Foreground(t.P.Dim).Render("[ ] ")
	}
	highlighted := highlightMatches(e.Display, hits, bg, lipgloss.NewStyle().Inherit(bg).Foreground(t.P.Yellow).Bold(true))
	suffix := "  " + bg.Foreground(t.P.Dim).Render(e.Group)
	line := cursor + check + highlighted + suffix
	pad := w - lipgloss.Width(line)
	if pad > 0 {
		line += bg.Width(pad).Render("")
	}
	return line
}
