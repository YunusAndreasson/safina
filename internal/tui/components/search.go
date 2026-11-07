package components

import (
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/yunus/safina/internal/tui/theme"
)

// SearchInput shares Bubbles' editing, Unicode handling, and horizontal
// scrolling across search dialogs. Result navigation stays with each dialog.
type SearchInput struct {
	input       textinput.Model
	initialized bool
}

func (s *SearchInput) init() {
	if s.initialized {
		return
	}
	s.input = textinput.New()
	s.input.Prompt = "› "
	s.input.CharLimit = 0
	styles := s.input.Styles()
	styles.Cursor.Blink = false
	s.input.SetStyles(styles)
	s.input.Focus()
	s.initialized = true
}

func (s *SearchInput) SetValue(value string) {
	s.init()
	s.input.SetValue(value)
	s.input.CursorEnd()
}

func (s *SearchInput) Value() string { return s.input.Value() }

func (s *SearchInput) Update(msg tea.Msg) (tea.Cmd, bool) {
	s.init()
	before := s.input.Value()
	var cmd tea.Cmd
	s.input, cmd = s.input.Update(msg)
	return cmd, before != s.input.Value()
}

func (s *SearchInput) View(t *theme.Theme, width int) string {
	s.init()
	styles := s.input.Styles()
	styles.Focused.Prompt = t.CyanBold
	styles.Focused.Text = t.FilterText
	styles.Focused.Placeholder = t.Hint
	styles.Cursor.Color = t.P.Cyan
	s.input.SetStyles(styles)
	// Bubbles' width excludes the two-cell prompt and end-of-input cursor.
	if w := max(1, width-3); w != s.input.Width() {
		s.input.SetWidth(w)
		// SetWidth stores the size; SetCursor asks Bubbles to recompute its
		// scroll window without moving the editing position after a resize.
		s.input.SetCursor(s.input.Position())
	}
	return s.input.View()
}
