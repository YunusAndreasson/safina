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

// RenderModal frames a modal: panel with focused-cyan accent, a body of
// (status row + viewport block + keys row), centered over the standard
// backdrop. termW/termH are the outer terminal dims; modalW/modalH the
// modal's outer box (caller-computed via ModalFitSize so all modals match).
func RenderModal(t *theme.Theme, termW, termH, modalW, modalH int, title, status, body, keys string) string {
	stack := lipgloss.JoinVertical(lipgloss.Left, status, body, keys)
	panel := PanelWithAccent(t, title, modalW, modalH, stack, true, t.P.Cyan, t.P.Cyan)
	return lipgloss.Place(termW, termH, lipgloss.Center, lipgloss.Center, panel, ModalBackdrop(t)...)
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
		caption := defaultStr(st.Loading_, "loading")
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
		left = t.DimTxt.Italic(true).Render("press / to filter")
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
	return left + strings.Repeat(" ", pad) + right
}
