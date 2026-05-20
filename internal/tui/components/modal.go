package components

import (
	"strings"

	"charm.land/bubbles/v2/viewport"
	"charm.land/lipgloss/v2"

	"github.com/yunus/safina/internal/tui/theme"
)

// ModalChrome is the number of vertical lines a section's modal reserves for
// non-viewport content: top border + bottom border + status row + keys row.
// Sections subtract this from the modal's outer height to size the viewport.
const ModalChrome = 4

// ModalFitSize clamps the requested terminal dimensions to the consistent
// modal envelope used by every section. Identical math previously lived in
// each modal's SetSize, drifting independently — keeping it in one place
// guarantees logs, events, and describe always render at the same size.
func ModalFitSize(termW, termH int) (w, h int) {
	w = termW - 4
	if w > 160 {
		w = 160
	}
	if w < 40 {
		w = 40
	}
	h = termH - 2
	if h < 12 {
		h = 12
	}
	return w, h
}

// ModalFilterWidth returns the width to give a filter input inside a modal of
// outer width modalW. Reserves ~36 cells on the right edge of the status row
// for indicators (match counts, follow flag, scroll %).
func ModalFilterWidth(modalW int) int {
	fw := PanelContentWidth(modalW) - 36
	if fw < 8 {
		fw = 8
	}
	return fw
}

// ModalBackdrop returns the standard whitespace-fill options used for the dim
// dot pattern behind every floating modal. Centralising it ensures all modals
// share the same backdrop look — adding a new modal type can't drift.
func ModalBackdrop(t *theme.Theme) []lipgloss.WhitespaceOption {
	return []lipgloss.WhitespaceOption{
		lipgloss.WithWhitespaceChars("·"),
		lipgloss.WithWhitespaceStyle(lipgloss.NewStyle().Foreground(t.P.BG3)),
	}
}

// BlockState is a discriminated union of the four states the centered-text
// area of a modal can be in. Exactly one boolean / non-zero field should be
// set per call; the renderer picks the matching message.
type BlockState struct {
	Loading  bool   // initial fetch in flight
	Err      error  // load failed
	Empty    bool   // load succeeded but returned zero items
	NoMatch  bool   // filter is non-empty and matches zero items
	Loaded   bool   // none of the above — render vp.View()
	Loading_ string // override for the spinner suffix (e.g. "establishing stream")
	Empty_   string // override for the empty-state caption
	NoMatch_ string // override for the no-match caption
}

// CenteredViewportBlock returns either the viewport's view or a centered
// status caption (loading spinner, error, empty, no-match) sized to fill the
// viewport's footprint. Replaces the per-section `viewportBlock` helpers
// that all duplicated this exact branch ladder.
func CenteredViewportBlock(t *theme.Theme, spinner string, vp viewport.Model, st BlockState) string {
	if st.Loaded {
		return vp.View()
	}
	box := lipgloss.NewStyle().
		Width(vp.Width()).
		Height(vp.Height()).
		Align(lipgloss.Center, lipgloss.Center)
	switch {
	case st.Loading:
		// Unify caption form: every in-flight surface shows "<spinner> <verb>…"
		// so the modal/inline/title loaders all read the same tense regardless
		// of whether the caller passed an override.
		caption := defaultStr(st.Loading_, "loading")
		if !strings.HasSuffix(caption, "…") {
			caption += "…"
		}
		return box.Render(t.Idle.Render(spinner + " " + caption))
	case st.Err != nil:
		return box.Render(lipgloss.NewStyle().Foreground(t.P.Red).Render(st.Err.Error()))
	case st.Empty:
		return box.Render(t.Idle.Render(defaultStr(st.Empty_, "no items")))
	case st.NoMatch:
		return box.Render(t.Idle.Render(defaultStr(st.NoMatch_, "no matches")))
	}
	return vp.View()
}

func defaultStr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// KeyDesc is a single (key, description) entry for a modal keys row. Sections
// build a slice of these and pass it to RenderModalKeysRow — the rendering is
// identical everywhere so we lift it.
type KeyDesc struct{ Key, Desc string }

// RenderModalKeysRow renders the bottom-of-modal key hints in the same style
// the global help bar uses (interpunct-separated, dim). Sections previously
// hand-rolled this with hardcoded strings; centralising means future style
// changes (e.g. switching the separator) propagate everywhere.
func RenderModalKeysRow(t *theme.Theme, pairs []KeyDesc) string {
	if len(pairs) == 0 {
		return ""
	}
	pieces := make([]string, len(pairs))
	for i, p := range pairs {
		pieces[i] = t.HelpKey.Render(p.Key) + " " + t.HelpDesc.Render(p.Desc)
	}
	return strings.Join(pieces, t.HelpDesc.Render(" · "))
}

// RenderHintRow renders an overlay's footer hint row — the same key/desc
// pattern as RenderModalKeysRow, but with a roomier interpunct separator
// (` · ` flanked by extra padding) suited to centred-overlay footers
// (palette, browser, cluster switcher, scale / port-forward modals).
// Sections previously hand-rolled this five different ways; centralising
// makes every floating modal use one rhythm.
func RenderHintRow(t *theme.Theme, pairs []KeyDesc) string {
	if len(pairs) == 0 {
		return ""
	}
	pieces := make([]string, len(pairs))
	for i, p := range pairs {
		pieces[i] = t.HelpKey.Render(p.Key) + t.HelpDesc.Render(" "+p.Desc)
	}
	return strings.Join(pieces, t.HelpDesc.Render("  ·  "))
}

// SearchPromptRow renders the centred-modal search input — a bold cyan `›`
// prompt, the live query, and a single-cell reverse-video cursor. Shared by
// the command palette and the resource browser so both modal-search overlays
// type, paint, and align identically.
func SearchPromptRow(t *theme.Theme, query string) string {
	prompt := lipgloss.NewStyle().Foreground(t.P.Cyan).Bold(true).Render("›")
	queryStyle := lipgloss.NewStyle().Foreground(t.P.FG)
	cursor := lipgloss.NewStyle().Foreground(t.P.FG).Reverse(true).Render(" ")
	return prompt + " " + queryStyle.Render(query) + cursor
}

// RenderConfirmHint renders the y/n confirm row used by every destructive
// modal (delete, rollback, restart, cordon, drain, edit-apply). Pattern:
//
//	y <verb>  ·  n/esc cancel
//
// The "y" is painted green to signal "this is the go-ahead key"; the rest
// stays dim so the row reads as a quiet affordance — the heavy lifting
// (yellow verb / red subject) happens above it.
func RenderConfirmHint(t *theme.Theme, verb string) string {
	green := lipgloss.NewStyle().Foreground(t.P.Green).Bold(true)
	yes := green.Render("y") + t.HelpDesc.Render(" "+verb)
	no := t.HelpDesc.Render("n/esc cancel")
	return yes + t.HelpDesc.Render("  ·  ") + no
}

// Indicator is a single right-aligned status indicator (match counts, scroll
// %, follow/paused, etc.). Style is optional — when nil the renderer defaults
// to t.DimTxt so most call sites can pass a bare {Text: …}.
type Indicator struct {
	Text  string
	Style *lipgloss.Style
}

// RenderModalStatusBar renders the modal's top status row: the filter widget
// (or a "press / to filter" hint) on the left, indicators joined by 2-space
// gaps on the right, justified to the modal's inner width. Sections used to
// hand-roll this with subtly different padding logic — the unified version
// guarantees consistent alignment.
func RenderModalStatusBar(t *theme.Theme, modalW int, filter Filter, indicators []Indicator) string {
	inner := PanelContentWidth(modalW)

	var left string
	switch {
	case filter.Active():
		left = filter.View()
	case filter.Value() != "":
		left = t.FilterPrompt.Render("/") + t.FilterText.Render(filter.Value())
	default:
		left = t.Hint.Render("press / to filter")
	}

	parts := make([]string, 0, len(indicators))
	for _, ind := range indicators {
		st := t.DimTxt
		if ind.Style != nil {
			st = *ind.Style
		}
		parts = append(parts, st.Render(ind.Text))
	}
	right := strings.Join(parts, "  ")

	pad := inner - lipgloss.Width(left) - lipgloss.Width(right)
	if pad < 1 {
		pad = 1
	}
	// Bare-text gap (no surrounding bg style) — Width(pad).Render("") on
	// an empty style produces exactly the same characters as the literal
	// space repeat, but reads as "padding cell" instead of "manual chars."
	return left + lipgloss.NewStyle().Width(pad).Render("") + right
}
