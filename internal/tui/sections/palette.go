package sections

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/sahilm/fuzzy"

	"github.com/yunus/safina/internal/k8s"
	"github.com/yunus/safina/internal/tui/theme"
	"github.com/yunus/safina/internal/tui/tuictx"
)

// Palette is the `:` Vim-style command palette. It scores everything the
// tree has loaded so far against a fuzzy query, surfaces ranked matches
// with an inline kind glyph and namespace, and shows the highlighted
// match's quick-info panel + verb chips at the bottom so the operator
// can jump straight to a verb (logs, edit, reveal, …) without first
// drilling.
//
// Search corpus comes from the tree's level cache; it grows as the
// session progresses. We deliberately don't pre-fetch the entire cluster
// on every `:` press — that would be slow on large clusters and would
// surface stale data on long-running sessions. Operators tend to
// navigate to the area they care about first; the palette becomes
// progressively smarter as that happens.
type Palette struct {
	query   string
	entries []PaletteEntry // full corpus, captured at Open()
	matches []PaletteEntry // ranked matches (or recents when query empty)
	indices [][]int        // matched character offsets per match — for highlight
	cursor  int            // index into matches
	verbHit string         // last-pressed verb letter (consumed by caller)
}

// PaletteVerb is one action chip below the preview pane. Letter is the
// hotkey shown after Tab; Label is the short verb name.
type PaletteVerb struct {
	Letter string
	Label  string
	// EntryKind is what the chip dispatches to — set on creation in
	// PaletteVerbsFor and consumed by the caller's verb dispatcher.
	EntryKind string
}

// NewPalette builds an empty palette. Open() seeds it for use.
func NewPalette() Palette {
	return Palette{}
}

// Open seeds the palette with the search corpus and resets the query.
// Called whenever the user presses `:` so the corpus is always fresh
// against the latest cache.
func (p *Palette) Open(entries []PaletteEntry) {
	p.entries = entries
	p.query = ""
	p.cursor = 0
	p.verbHit = ""
	p.recomputeMatches()
}

// SetQuery replaces the query and reranks matches.
func (p *Palette) SetQuery(q string) {
	p.query = q
	p.cursor = 0
	p.recomputeMatches()
}

// Query returns the current query string (for the input-line render).
func (p *Palette) Query() string { return p.query }

// CursorUp / CursorDown move within the result list.
func (p *Palette) CursorUp() {
	if len(p.matches) == 0 {
		return
	}
	if p.cursor > 0 {
		p.cursor--
	}
}

func (p *Palette) CursorDown() {
	if len(p.matches) == 0 {
		return
	}
	if p.cursor < len(p.matches)-1 {
		p.cursor++
	}
}

// Selected returns the highlighted entry, if any.
func (p *Palette) Selected() (PaletteEntry, bool) {
	if p.cursor < 0 || p.cursor >= len(p.matches) {
		return PaletteEntry{}, false
	}
	return p.matches[p.cursor], true
}

// VerbHit consumes the most recent verb letter the user pressed (after
// Tab). Returns "" when none. Caller dispatches the verb on the
// currently-selected entry, then closes the palette.
func (p *Palette) VerbHit() string {
	v := p.verbHit
	p.verbHit = ""
	return v
}

// SetVerbHit records a verb letter the user pressed. Caller calls
// VerbHit() to consume it on the next tick.
func (p *Palette) SetVerbHit(letter string) { p.verbHit = letter }

// recomputeMatches reranks the entries against the current query. With
// an empty query we show the entries as-is (most recently indexed first
// — BuildPaletteIndex preserves cache iteration order which is roughly
// chronological).
func (p *Palette) recomputeMatches() {
	if p.query == "" {
		// Cap to a sane number; the user can still type to surface more.
		const head = 50
		if len(p.entries) > head {
			p.matches = p.entries[:head]
		} else {
			p.matches = p.entries
		}
		p.indices = make([][]int, len(p.matches))
		return
	}
	corpus := paletteCorpus{entries: p.entries}
	res := fuzzy.FindFrom(p.query, corpus)
	p.matches = make([]PaletteEntry, 0, len(res))
	p.indices = make([][]int, 0, len(res))
	for _, r := range res {
		p.matches = append(p.matches, p.entries[r.Index])
		p.indices = append(p.indices, append([]int(nil), r.MatchedIndexes...))
	}
}

// paletteCorpus adapts []PaletteEntry to fuzzy.Source.
type paletteCorpus struct{ entries []PaletteEntry }

func (c paletteCorpus) String(i int) string { return c.entries[i].SearchString() }
func (c paletteCorpus) Len() int            { return len(c.entries) }

// View renders the palette as a centered modal. width and height come
// from the host model and are clamped so the modal stays bounded on
// huge terminals.
func (p *Palette) View(c *tuictx.Ctx, termW, termH int) string {
	t := c.Theme

	// Modal sized to roughly 80% of terminal width, capped so very wide
	// terminals don't make the lines unreadable.
	w := termW * 80 / 100
	if w > 96 {
		w = 96
	}
	if w < 60 {
		w = 60
	}
	innerW := w - 4 // border + padding

	// Vertical budget: input row + match list (variable) + preview block
	// + verb chips + footer + 1 spacer per gap. Cap matches displayed.
	maxRows := termH * 70 / 100
	if maxRows > 24 {
		maxRows = 24
	}
	listRows := maxRows - 8 // leaves room for input + preview + verbs + footer
	if listRows < 4 {
		listRows = 4
	}

	// Input row.
	prompt := lipgloss.NewStyle().Foreground(t.P.Cyan).Bold(true).Render("›")
	queryStyle := lipgloss.NewStyle().Foreground(t.P.FG)
	cursor := lipgloss.NewStyle().Foreground(t.P.FG).Reverse(true).Render(" ")
	input := prompt + " " + queryStyle.Render(p.query) + cursor

	// Result list.
	list := p.renderList(c, innerW, listRows)

	// Preview + verbs.
	var preview, verbs string
	if sel, ok := p.Selected(); ok {
		preview = renderPalettePreview(c, sel, innerW)
		verbs = renderPaletteVerbs(c, sel, innerW)
	} else {
		preview = lipgloss.NewStyle().Foreground(t.P.Dim).Italic(true).
			Render("type to search · drill what you've already loaded")
		verbs = ""
	}

	footer := lipgloss.NewStyle().Foreground(t.P.Dim).Render(
		t.HelpKey.Render("↵") + t.HelpDesc.Render(" open  ") +
			t.HelpKey.Render("letter") + t.HelpDesc.Render(" verb  ") +
			t.HelpKey.Render("esc") + t.HelpDesc.Render(" close"),
	)

	body := lipgloss.JoinVertical(lipgloss.Left,
		input,
		"",
		list,
		"",
		preview,
		verbs,
		"",
		footer,
	)

	panel := t.Modal.Padding(1, 2).Width(w).Render(body)
	return lipgloss.Place(termW, termH, lipgloss.Center, lipgloss.Center, panel,
		lipgloss.WithWhitespaceChars("·"),
		lipgloss.WithWhitespaceStyle(lipgloss.NewStyle().Foreground(t.P.BG3)),
	)
}

// renderList renders the matches as a compact list with kind-glyph,
// highlighted name, namespace tag, and hint. Selected row is reversed.
func (p *Palette) renderList(c *tuictx.Ctx, w, rows int) string {
	t := c.Theme
	if len(p.matches) == 0 {
		return lipgloss.NewStyle().Foreground(t.P.Dim).Italic(true).
			Render("  no matches — drill into a cluster to populate the index")
	}
	start := 0
	end := len(p.matches)
	// Ensure cursor stays in view.
	if end > rows {
		// Window of size rows centered on cursor.
		half := rows / 2
		start = p.cursor - half
		if start < 0 {
			start = 0
		}
		end = start + rows
		if end > len(p.matches) {
			end = len(p.matches)
			start = end - rows
		}
	}

	out := make([]string, 0, end-start)
	for i := start; i < end; i++ {
		entry := p.matches[i]
		var hits []int
		if i < len(p.indices) {
			hits = p.indices[i]
		}
		row := renderPaletteRow(c, entry, hits, w, i == p.cursor)
		out = append(out, row)
	}
	return strings.Join(out, "\n")
}

// renderPaletteRow renders one match line: cursor mark, kind glyph,
// fuzzy-highlighted name, namespace, hint. The kind glyphs come from the
// existing tree palette so the palette feels like a continuation of the
// same visual language — operators see the same ◇ for Deployments,
// ⌧ for Secrets, etc., they already learned.
func renderPaletteRow(c *tuictx.Ctx, e PaletteEntry, hits []int, w int, selected bool) string {
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

	glyph := paletteGlyph(e)
	glyphCell := bg.Render(" ") + lipgloss.NewStyle().Inherit(bg).Foreground(paletteGlyphColor(t, e)).Render(glyph) + bg.Render(" ")

	// Highlight matched chars in the search string. Hits are positions
	// within SearchString(), which begins with Name — so the prefix of
	// the highlight set lands on the name body.
	name := e.Name
	highlighted := highlightMatches(name, hits, bg, lipgloss.NewStyle().Inherit(bg).Foreground(t.P.Yellow).Bold(true))

	// Suffix: namespace + sub-kind.
	suffix := ""
	if e.Namespace != "" {
		suffix += "  " + bg.Foreground(t.P.Dim).Render("ns:"+e.Namespace)
	}
	kindLabel := e.SubKind
	if kindLabel == "" {
		kindLabel = e.Kind
	}
	suffix += "  " + bg.Foreground(t.P.FGFade).Render(kindLabel)
	if e.Hint != "" {
		suffix += "  " + bg.Foreground(t.P.Dim).Render(e.Hint)
	}

	line := cursor + glyphCell + highlighted + suffix
	// Pad to width so the row's background fills the modal column.
	pad := w - lipgloss.Width(line)
	if pad > 0 {
		line += bg.Render(strings.Repeat(" ", pad))
	}
	return line
}

// highlightMatches renders s with characters at hit positions in the
// match style and others in base. Hits beyond len(s) are ignored — they
// belong to the suffix tokens of the search string and aren't part of
// the visible name.
func highlightMatches(s string, hits []int, base, match lipgloss.Style) string {
	if len(hits) == 0 {
		return base.Render(s)
	}
	hitset := make(map[int]struct{}, len(hits))
	for _, h := range hits {
		if h >= 0 && h < len(s) {
			hitset[h] = struct{}{}
		}
	}
	if len(hitset) == 0 {
		return base.Render(s)
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		ch := string(s[i])
		if _, ok := hitset[i]; ok {
			b.WriteString(match.Render(ch))
		} else {
			b.WriteString(base.Render(ch))
		}
	}
	return b.String()
}

// renderPalettePreview renders the highlighted match's quick-info row.
// Compact one-liner — the operator hits ↵ to drill into the full view if
// they want depth.
func renderPalettePreview(c *tuictx.Ctx, e PaletteEntry, w int) string {
	t := c.Theme
	bar := lipgloss.NewStyle().Foreground(t.P.Cyan).Render("▌ ")
	name := lipgloss.NewStyle().Foreground(t.P.FG).Bold(true).Render(e.Name)
	parts := []string{bar + name}
	if e.Namespace != "" {
		parts = append(parts, lipgloss.NewStyle().Foreground(t.P.Dim).Render("ns: "+e.Namespace))
	}
	kind := e.SubKind
	if kind == "" {
		kind = e.Kind
	}
	parts = append(parts, lipgloss.NewStyle().Foreground(t.P.FGFade).Render(kind))
	if e.Hint != "" {
		parts = append(parts, lipgloss.NewStyle().Foreground(t.P.Dim).Render(e.Hint))
	}
	return strings.Join(parts, "  ")
}

// renderPaletteVerbs renders the action chips below the preview. Each
// chip shows `letter label` so the operator can press Tab + letter to
// dispatch directly. Verbs come from PaletteVerbsFor — the per-kind
// affordance table.
func renderPaletteVerbs(c *tuictx.Ctx, e PaletteEntry, w int) string {
	t := c.Theme
	verbs := PaletteVerbsFor(e)
	if len(verbs) == 0 {
		return ""
	}
	chips := make([]string, 0, len(verbs))
	for _, v := range verbs {
		k := lipgloss.NewStyle().Foreground(t.P.Blue).Bold(true).Render(v.Letter)
		l := lipgloss.NewStyle().Foreground(t.P.FGFade).Render(v.Label)
		chips = append(chips, k+" "+l)
	}
	prefix := lipgloss.NewStyle().Foreground(t.P.Cyan).Render("▌ ")
	return prefix + strings.Join(chips, "  ")
}

// PaletteVerbsFor returns the action verb chips for an entry. Mirrors the
// tree's contextual-footer verb set so palette + footer teach the same
// vocabulary; the operator never has to learn two key alphabets.
// PaletteVerbsFor: lowercase letters = read verbs (left), uppercase
// letters = mutators (right). Matches the keymap convention.
func PaletteVerbsFor(e PaletteEntry) []PaletteVerb {
	del := PaletteVerb{Letter: "K", Label: "kill"}
	switch e.Kind {
	case k8s.KindApp:
		// Reads (lowercase, left) first.
		base := []PaletteVerb{
			{Letter: "d", Label: "describe"},
			{Letter: "l", Label: "logs"},
			{Letter: "e", Label: "events"},
		}
		// Mutators (uppercase, right) layered on per SubKind.
		switch e.SubKind {
		case k8s.AppKindDeployment:
			return append(base,
				PaletteVerb{Letter: "S", Label: "scale"},
				PaletteVerb{Letter: "U", Label: "undo"},
				PaletteVerb{Letter: "R", Label: "restart"},
				PaletteVerb{Letter: "E", Label: "edit"},
				del,
			)
		case k8s.AppKindStatefulSet:
			return append(base,
				PaletteVerb{Letter: "S", Label: "scale"},
				PaletteVerb{Letter: "R", Label: "restart"},
				PaletteVerb{Letter: "E", Label: "edit"},
				del,
			)
		case k8s.AppKindDaemonSet:
			return append(base,
				PaletteVerb{Letter: "R", Label: "restart"},
				PaletteVerb{Letter: "E", Label: "edit"},
				del,
			)
		case k8s.AppKindJob, k8s.AppKindCronJob:
			return append(base, del)
		}
		return base
	case k8s.KindPod:
		return []PaletteVerb{
			{Letter: "d", Label: "describe"},
			{Letter: "l", Label: "logs"},
			{Letter: "e", Label: "events"},
			{Letter: "s", Label: "shell"},
			{Letter: "f", Label: "forward"},
			{Letter: "R", Label: "restart"},
			del,
		}
	case k8s.KindSecret:
		return []PaletteVerb{
			{Letter: "d", Label: "describe"},
			{Letter: "v", Label: "reveal"},
			{Letter: "E", Label: "edit"},
			del,
		}
	case k8s.KindConfigMap:
		return []PaletteVerb{
			{Letter: "d", Label: "describe"},
			{Letter: "E", Label: "edit"},
			del,
		}
	case k8s.KindService:
		return []PaletteVerb{
			{Letter: "d", Label: "describe"},
			{Letter: "e", Label: "events"},
			{Letter: "E", Label: "edit"},
			{Letter: "f", Label: "forward"},
			del,
		}
	case k8s.KindPVC:
		return []PaletteVerb{
			{Letter: "d", Label: "describe"},
			{Letter: "e", Label: "events"},
			{Letter: "E", Label: "edit"},
			del,
		}
	case k8s.KindIngress:
		return []PaletteVerb{
			{Letter: "d", Label: "describe"},
			{Letter: "E", Label: "edit"},
			del,
		}
	case k8s.KindNode:
		return []PaletteVerb{
			{Letter: "d", Label: "describe"},
			{Letter: "e", Label: "events"},
			{Letter: "C", Label: "cordon"},
			{Letter: "D", Label: "drain"},
		}
	case k8s.KindNamespace, k8s.KindNamespaceFolder:
		return []PaletteVerb{
			{Letter: "d", Label: "describe"},
			{Letter: "e", Label: "events"},
		}
	}
	return nil
}

// paletteGlyph returns the same glyph the tree uses for this kind. We
// re-use the existing kindGlyph and appKindGlyph helpers so the palette
// reads as a continuation of the tree's visual language.
func paletteGlyph(e PaletteEntry) string {
	if e.Kind == k8s.KindApp && e.SubKind != "" {
		return appKindGlyph(e.SubKind)
	}
	return kindGlyph(e.Kind, "")
}

// paletteGlyphColor picks an accent for the kind glyph. Without per-row
// health data we color by kind family: workloads in blue, infra/cluster
// kinds in cyan, network/storage in yellow as a non-saturated accent,
// secrets/config dim. Cheap heuristic — the goal is just visual variety
// in the result list, not full health signaling.
func paletteGlyphColor(t *theme.Theme, e PaletteEntry) color.Color {
	switch e.Kind {
	case k8s.KindApp, k8s.KindPod, k8s.KindContainer:
		return t.P.Blue
	case k8s.KindContext, k8s.KindNode, k8s.KindNodePool, k8s.KindNamespace, k8s.KindNamespaceFolder:
		return t.P.Cyan
	case k8s.KindService, k8s.KindIngress, k8s.KindPVC, k8s.KindPV:
		return t.P.Yellow
	case k8s.KindConfigMap, k8s.KindSecret:
		return t.P.FGFade
	}
	return t.P.FG
}
