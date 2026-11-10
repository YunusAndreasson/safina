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
		((m.state == stateMain && !m.peekWorkspace) || (m.treeFocusInImmersive && m.isImmersiveState()))
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
	if m.ctx.Width < minSplitWidth || m.treeFocusInImmersive {
		reads = append(reads, components.KeyDesc{Key: "→/tab", Desc: "pane"})
	}
	for _, binding := range m.contextualShortHelp() {
		h := binding.Help()
		if h.Key == m.ctx.Keys.Help.Help().Key || h.Key == m.ctx.Keys.Search.Help().Key {
			continue
		} // fixed right-hand escape hatch
		if m.treeFocusInImmersive && h.Key == m.ctx.Keys.Quit.Help().Key {
			h.Desc = "close view"
		}
		hint := components.KeyDesc{Key: h.Key, Desc: h.Desc, Write: m.ctx.Keys.IsWrite(h.Key)}
		if hint.Write {
			changes = append(changes, hint)
		} else {
			reads = append(reads, hint)
		}
	}
	showChanges := m.shiftHeld && m.canRevealChanges() && len(changes) > 0
	rightHints := []components.KeyDesc{{Key: "?", Desc: "commands"}}
	if !showChanges {
		rightHints = append([]components.KeyDesc{{Key: "⌃p", Desc: "search"}}, rightHints...)
	}
	if m.shiftHintsSupported && len(changes) > 0 && m.canRevealChanges() {
		desc := "changes"
		if showChanges {
			desc = "release"
		}
		rightHints = append([]components.KeyDesc{{Key: "⇧", Desc: desc}}, rightHints...)
	}
	right := components.RenderModalKeysRow(t, rightHints)
	if m.shiftHintsSupported && len(changes) > 0 && !showChanges && m.canRevealChanges() {
		right = t.HelpDesc.Render("hold ") + right
	}
	// Keep the discoverability hint visible even when commands overflow.
	leftWidth := max(0, width-textw.Width(right)-2)
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
	} else {
		left = components.RenderBoundedHints(t, reads, leftWidth, components.KeyDesc{})
	}
	left = ansi.Truncate(left, leftWidth, "")
	return left + strings.Repeat(" ", max(0, width-textw.Width(left)-textw.Width(right))) + right
}
