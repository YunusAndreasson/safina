package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/YunusAndreasson/safina/internal/tui/components"
	"github.com/YunusAndreasson/safina/internal/tui/textw"
	"github.com/charmbracelet/x/ansi"
)

// Shift only reveals hints. It never arms commands or changes their bindings.
func (m *Model) canRevealChanges() bool {
	return m.focused && m.choices == nil && !m.tree.FilterActive() && !m.sectionHasFilterFocus() &&
		(m.state == stateMain || m.isImmersiveState())
}

func (m *Model) resetShift() {
	m.shiftHeld = false
	m.shiftKeys = 0
}

func (m *Model) trackShift(k tea.Key, release bool) {
	if !m.shiftHintsSupported || !m.canRevealChanges() {
		m.resetShift()
		return
	}
	var side uint8
	switch k.Code {
	case tea.KeyLeftShift:
		side = 1
	case tea.KeyRightShift:
		side = 2
	}
	if side != 0 {
		// A modifier event can carry the mask from before its transition.
		// The event itself is authoritative; track both physical Shift keys
		// so releasing one doesn't hide hints while the other remains held.
		if release {
			m.shiftKeys &^= side
		} else {
			m.shiftKeys |= side
		}
		m.shiftHeld = m.shiftKeys != 0
		return
	}
	// Other key releases must not re-arm the footer with a stale mask.
	// Modifier-only events likewise cannot change the physical Shift state.
	if release || modifierOnly(k.Code) {
		return
	}
	m.shiftHeld = k.Mod&tea.ModShift != 0
	if !m.shiftHeld {
		m.shiftKeys = 0
	}
}

// Enhanced reporting includes standalone modifier presses. They are not
// commands and must not be forwarded into text inputs or confirmation dialogs.
func modifierOnly(code rune) bool {
	switch code {
	case tea.KeyLeftShift, tea.KeyRightShift, tea.KeyLeftCtrl, tea.KeyRightCtrl,
		tea.KeyLeftAlt, tea.KeyRightAlt, tea.KeyLeftSuper, tea.KeyRightSuper,
		tea.KeyLeftHyper, tea.KeyRightHyper, tea.KeyLeftMeta, tea.KeyRightMeta,
		tea.KeyIsoLevel3Shift, tea.KeyIsoLevel5Shift:
		return true
	}
	return false
}

func (m *Model) renderTreeFooter() string {
	t := m.ctx.Theme
	width := max(0, m.ctx.Width-2)
	reads, changes := []components.KeyDesc{}, []components.KeyDesc{}
	for _, binding := range m.contextualShortHelp() {
		h := binding.Help()
		if h.Key == m.ctx.Keys.Help.Help().Key || h.Key == m.ctx.Keys.Search.Help().Key {
			continue
		} // fixed right-hand escape hatch
		hint := components.KeyDesc{Key: h.Key, Desc: h.Desc, Write: m.ctx.Keys.IsWrite(h.Key)}
		if hint.Write {
			changes = append(changes, hint)
		} else {
			reads = append(reads, hint)
		}
	}
	showChanges := m.shiftHeld && m.canRevealChanges() && len(changes) > 0
	rightHints := []components.KeyDesc{{Key: "?", Desc: "help"}}
	if !showChanges {
		rightHints = append([]components.KeyDesc{{Key: "ctrl+p", Desc: "search"}}, rightHints...)
	}
	if m.shiftHintsSupported && len(changes) > 0 && m.canRevealChanges() {
		desc := "changes"
		if showChanges {
			desc = "release"
		}
		rightHints = append([]components.KeyDesc{{Key: "shift", Desc: desc}}, rightHints...)
	}
	right := components.RenderModalKeysRow(t, rightHints)
	if m.shiftHintsSupported && len(changes) > 0 && !showChanges && m.canRevealChanges() {
		right = t.HelpDesc.Render("hold ") + right
	}
	// Keep the discoverability hint visible even when commands overflow.
	// While Shift reveals changes, the footer is about the selected row's
	// changes alone; the back hint returns with the normal hints.
	back := ""
	if !showChanges {
		back = m.backHint()
	}
	leftWidth := max(0, width-textw.Width(right)-2-textw.Width(back))
	var left string
	if showChanges {
		prefix := t.Danger.Render("CHANGE")
		if width >= 90 {
			ref := m.tree.SelectedRef()
			kind := ref.Kind
			if ref.SubKind != "" {
				kind = ref.SubKind
			}
			target := ansi.Truncate(kind+"/"+ref.Name, min(28, leftWidth/3), "…")
			prefix += " · " + t.Hint.Render(target)
		}
		budget := max(0, leftWidth-textw.Width(prefix)-2)
		commands := components.RenderBoundedHints(t, changes, budget, components.KeyDesc{})
		left = prefix + "  " + commands
	} else if !m.fullHeader() {
		// The full header lists the reads; without it the footer does.
		left = components.RenderBoundedHints(t, reads, leftWidth, components.KeyDesc{})
	}
	left = back + ansi.Truncate(left, leftWidth, "")
	return left + strings.Repeat(" ", max(0, width-textw.Width(left)-textw.Width(right))) + right
}
