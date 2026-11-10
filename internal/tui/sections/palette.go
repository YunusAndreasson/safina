package sections

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/sahilm/fuzzy"

	"github.com/YunusAndreasson/safina/internal/k8s"
	"github.com/YunusAndreasson/safina/internal/tui/components"
	"github.com/YunusAndreasson/safina/internal/tui/textw"
	"github.com/YunusAndreasson/safina/internal/tui/tuictx"
)

// Palette is the `:` Vim-style command palette. It scores everything the
// tree has loaded so far against a fuzzy query, surfaces ranked matches
// with a readable kind label and namespace, and shows the highlighted
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
	input   components.SearchInput
	entries []PaletteEntry // full corpus, captured at Open()
	matches []PaletteEntry // ranked matches (or alphabetical when query empty)
	indices [][]int        // matched character offsets per match — for highlight
	cursor  int            // index into matches
	verbHit string         // last-pressed verb letter (consumed by caller)
	scope   string
	actions bool // Tab explicitly enables action shortcuts
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
	p.scope = "Resources & tools"
	p.entries = entries
	p.input.SetValue("")
	p.cursor = 0
	p.verbHit = ""
	p.actions = false
	p.recomputeMatches()
}

// SetQuery replaces the query and reranks matches.
func (p *Palette) SetQuery(q string) {
	p.input.SetValue(q)
	p.cursor = 0
	p.recomputeMatches()
}

// UpdateQuery delegates editing to the shared Bubbles input and reranks only
// when its value changes; cursor movement does not reset result selection.
func (p *Palette) UpdateQuery(msg tea.Msg) tea.Cmd {
	cmd, changed := p.input.Update(msg)
	if changed {
		p.cursor = 0
		p.recomputeMatches()
	}
	return cmd
}

// Query returns the current query string (for the input-line render).
func (p *Palette) Query() string { return p.input.Value() }

func (p *Palette) ActionsActive() bool {
	selected, ok := p.Selected()
	return p.actions && ok && len(PaletteVerbsFor(selected)) > 0
}

func (p *Palette) ToggleActions() {
	if p.actions {
		p.actions = false
		return
	}
	selected, ok := p.Selected()
	p.actions = ok && len(PaletteVerbsFor(selected)) > 0
}

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
// an empty query we show the stable alphabetical index.
func (p *Palette) recomputeMatches() {
	if p.input.Value() == "" {
		p.matches = p.entries
		p.indices = make([][]int, len(p.matches))
		return
	}
	corpus := paletteCorpus{entries: p.entries}
	res := fuzzy.FindFrom(p.input.Value(), corpus)
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
	return lipgloss.Place(termW, termH, lipgloss.Center, lipgloss.Center, p.Card(c, termW, termH), components.ModalBackdrop(c.Theme)...)
}

// Card renders only the dialog; the application owns its layered backdrop.
func (p *Palette) Card(c *tuictx.Ctx, termW, termH int) string {
	t := c.Theme

	innerW := max(1, min(76, termW-8))
	input := p.input.View(t, innerW)
	if p.ActionsActive() {
		input = t.CyanBold.Render("Resource actions · ") + components.RenderModalKeysRow(t, []components.KeyDesc{{Key: "tab", Desc: "back to search"}})
	}
	input = ansi.Truncate(input, innerW, "…")
	preview, verbs := "", ""
	if sel, ok := p.Selected(); ok && p.ActionsActive() {
		if termH >= 18 {
			preview = ansi.Truncate(renderPalettePreview(c, sel), innerW, "…")
		}
		verbs = ansi.Wrap(renderPaletteVerbs(c, sel), innerW, "")
	}
	hints := []components.KeyDesc{{Key: "↵", Desc: "open"}}
	if selected, ok := p.Selected(); ok && len(PaletteVerbsFor(selected)) > 0 {
		desc := "actions"
		if p.ActionsActive() {
			desc = "search"
		}
		hints = append(hints, components.KeyDesc{Key: "tab", Desc: desc})
	}
	hints = append(hints, components.KeyDesc{Key: "ctrl+s", Desc: "search namespace"}, components.KeyDesc{Key: "esc", Desc: "close"})
	footer := components.RenderBoundedHints(t, hints, innerW, components.KeyDesc{Key: "esc", Desc: "close"})
	// Keep the namespace expansion discoverable at narrow widths too.
	searchHint := ""
	if innerW < 65 && !p.ActionsActive() {
		searchHint = components.RenderBoundedHints(t, []components.KeyDesc{{Key: "ctrl+s", Desc: "search namespace"}}, innerW, components.KeyDesc{})
	}
	extra := 0
	if p.ActionsActive() {
		extra = lipgloss.Height(verbs)
		if preview != "" {
			extra += lipgloss.Height(preview)
		}
	}
	if searchHint != "" {
		extra++
	}
	fixed := 9 + extra
	rows := max(1, min(8, termH-fixed))
	scope := p.scope
	if scope == "" {
		scope = "Resources & tools"
	}
	title := fmt.Sprintf("%s · %d matches", scope, len(p.matches))
	parts := []string{t.CyanBold.Render(ansi.Truncate(title, innerW, "…")), input, "", p.renderList(c, innerW, rows), ""}
	if p.ActionsActive() {
		if preview != "" {
			parts = append(parts, preview)
		}
		parts = append(parts, verbs)
	}
	if searchHint != "" {
		parts = append(parts, searchHint)
	}
	parts = append(parts, footer)
	body := textw.JoinVertical(lipgloss.Left, parts...)
	panel := t.Modal.Padding(1, 2).Width(innerW + 6).Render(body)
	return panel
}

// renderList renders the matches as a compact list with kind-glyph,
// highlighted name, namespace tag, and hint. Selected row is reversed.
func (p *Palette) renderList(c *tuictx.Ctx, w, rows int) string {
	t := c.Theme
	if len(p.matches) == 0 {
		if len(p.entries) == 0 {
			return t.Hint.Render("No resources loaded. ") + components.RenderBoundedHints(t, []components.KeyDesc{{Key: "esc", Desc: "back to clusters"}}, max(0, w-21), components.KeyDesc{})
		}
		return t.Hint.Render(ansi.Truncate("No matches. Try a shorter query or browse more resources.", w, "…"))
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

// renderPaletteRow shows the selected name, namespace, and readable resource kind.
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

	// Highlight matched chars in the search string. Hits are positions
	// within SearchString(), which begins with Name — so the prefix of
	// the highlight set lands on the name body.
	name := e.Name
	highlighted := highlightMatches(name, hits, bg, lipgloss.NewStyle().Inherit(bg).Foreground(t.P.Yellow).Bold(true))

	// Suffix: namespace + sub-kind.
	suffix := ""
	if e.Namespace != "" {
		suffix += bg.Render("  ") + bg.Foreground(t.P.Dim).Render("ns:"+e.Namespace)
	}
	kindLabel := e.kindLabel()
	suffix += bg.Render("  ") + bg.Foreground(t.ResourceKindColor(kindLabel)).Render(kindLabel)
	if e.Hint != "" {
		suffix += bg.Render("  ") + bg.Foreground(t.P.Dim).Render(e.Hint)
	}

	line := cursor + highlighted + suffix
	// Pad to width so the row's background fills the modal column.
	pad := w - textw.Width(line)
	if pad > 0 {
		line += bg.Width(pad).Render("")
	}
	return ansi.Truncate(line, w, "…")
}

// highlightMatches adapts fuzzy's UTF-8 byte offsets to Lip Gloss rune
// indices. The library owns grouping and styling; suffix hits are ignored.
func highlightMatches(s string, hits []int, base, match lipgloss.Style) string {
	indices := make([]int, 0, len(hits))
	for _, hit := range hits {
		if hit >= 0 && hit < len(s) {
			indices = append(indices, utf8.RuneCountInString(s[:hit]))
		}
	}
	return lipgloss.StyleRunes(s, indices, match, base)
}

// renderPalettePreview renders the highlighted match's quick-info row.
// Compact one-liner — the operator hits ↵ to drill into the full view if
// they want depth.
func renderPalettePreview(c *tuictx.Ctx, e PaletteEntry) string {
	t := c.Theme
	bar := lipgloss.NewStyle().Foreground(t.P.Cyan).Render("▌ ")
	name := lipgloss.NewStyle().Foreground(t.P.FG).Bold(true).Render(e.Name)
	parts := []string{bar + name}
	if e.Namespace != "" {
		parts = append(parts, lipgloss.NewStyle().Foreground(t.P.Dim).Render("ns: "+e.Namespace))
	}
	kind := e.kindLabel()
	parts = append(parts, lipgloss.NewStyle().Foreground(t.ResourceKindColor(kind)).Render(kind))
	if e.Hint != "" {
		parts = append(parts, lipgloss.NewStyle().Foreground(t.P.Dim).Render(e.Hint))
	}
	return strings.Join(parts, "  ")
}

// renderPaletteVerbs renders the action chips below the preview. Each
// chip shows `letter label` so the operator can press Tab + letter to
// dispatch directly. Verbs come from PaletteVerbsFor — the per-kind
// affordance table.
func renderPaletteVerbs(c *tuictx.Ctx, e PaletteEntry) string {
	t := c.Theme
	verbs := PaletteVerbsFor(e)
	if len(verbs) == 0 {
		return ""
	}
	pairs := make([]components.KeyDesc, 0, len(verbs))
	for _, v := range verbs {
		pairs = append(pairs, components.KeyDesc{Key: v.Letter, Desc: v.Label, Write: c.Keys.IsWrite(v.Letter)})
	}
	prefix := lipgloss.NewStyle().Foreground(t.P.Cyan).Render("▌ ")
	return prefix + components.RenderModalKeysRow(t, pairs)
}

// PaletteVerbsFor returns the action verb chips for an entry. Mirrors the
// tree's contextual-footer verb set so palette + footer teach the same
// vocabulary; the operator never has to learn two key alphabets.
// PaletteVerbsFor: lowercase letters = read verbs (left), uppercase
// letters = mutators (right). Matches the keymap convention.
func PaletteVerbsFor(e PaletteEntry) []PaletteVerb {
	del := PaletteVerb{Letter: "K", Label: "delete"}
	switch e.Kind {
	case k8s.KindApp:
		// Reads (lowercase, left) first.
		base := []PaletteVerb{
			{Letter: "d", Label: "YAML"},
			{Letter: "l", Label: "logs"},
			{Letter: "e", Label: "events"},
		}
		// Mutators (uppercase, right) layered on per SubKind.
		switch e.SubKind {
		case k8s.AppKindDeployment:
			return append(base,
				PaletteVerb{Letter: "S", Label: "scale"},
				PaletteVerb{Letter: "U", Label: "rollback"},
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
			{Letter: "d", Label: "YAML"},
			{Letter: "l", Label: "logs"},
			{Letter: "e", Label: "events"},
			{Letter: "s", Label: "shell"},
			{Letter: "f", Label: "forward"},
			{Letter: "R", Label: "restart"},
			del,
		}
	case k8s.KindSecret:
		return []PaletteVerb{
			{Letter: "d", Label: "YAML"},
			{Letter: "v", Label: "reveal"},
			{Letter: "E", Label: "edit"},
			del,
		}
	case k8s.KindConfigMap:
		return []PaletteVerb{
			{Letter: "d", Label: "YAML"},
			{Letter: "E", Label: "edit"},
			del,
		}
	case k8s.KindService:
		return []PaletteVerb{
			{Letter: "d", Label: "YAML"},
			{Letter: "e", Label: "events"},
			{Letter: "E", Label: "edit"},
			{Letter: "f", Label: "forward"},
			del,
		}
	case k8s.KindPVC:
		return []PaletteVerb{
			{Letter: "d", Label: "YAML"},
			{Letter: "e", Label: "events"},
			{Letter: "E", Label: "edit"},
			del,
		}
	case k8s.KindIngress:
		return []PaletteVerb{
			{Letter: "d", Label: "YAML"},
			{Letter: "E", Label: "edit"},
			del,
		}
	case k8s.KindNode:
		return []PaletteVerb{
			{Letter: "d", Label: "YAML"},
			{Letter: "e", Label: "events"},
			{Letter: "C", Label: "cordon"},
			{Letter: "D", Label: "drain"},
		}
	case k8s.KindNamespace, k8s.KindNamespaceFolder:
		return []PaletteVerb{
			{Letter: "d", Label: "YAML"},
			{Letter: "e", Label: "events"},
		}
	}
	return nil
}
