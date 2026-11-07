package components

import (
	"iter"
	"slices"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/sahilm/fuzzy"

	"github.com/yunus/safina/internal/tui/theme"
)

// Filter is a textinput-based fuzzy-filter widget with snapshot semantics.
//
// Lifecycle:
//   - Open()   — focuses the input and snapshots the current value so a later
//     Cancel() can revert to it.
//   - Close()  — commits: blurs the input, value is preserved.
//   - Cancel() — reverts the value to the snapshot taken at Open(), then
//     Closes. Use this for "esc inside the input".
//   - Clear()  — empties the value entirely (regardless of focus state). Use
//     for "esc outside the input" or an explicit ctrl+l.
//
// While Active, keystrokes feed the input via Update; the section reads
// Value() and applies it (commonly with FuzzyMatch) to its data set.
type Filter struct {
	input    textinput.Model
	active   bool
	snapshot string
}

// NewFilter builds a themed filter input.
func NewFilter(t *theme.Theme) Filter {
	in := textinput.New()
	in.Placeholder = "filter…"
	in.Prompt = "/"
	styles := in.Styles()
	// Italic placeholder reads as a separate typographic register from real
	// query text — the operator can tell at a glance whether they're seeing
	// a hint or their own input.
	placeholder := t.Hint
	styles.Focused.Prompt = t.FilterPrompt
	styles.Focused.Text = t.FilterText
	styles.Focused.Placeholder = placeholder
	styles.Blurred.Prompt = t.FilterPrompt
	styles.Blurred.Text = t.FilterText
	styles.Blurred.Placeholder = placeholder
	in.SetStyles(styles)
	return Filter{input: in}
}

// Open focuses the input and captures the current value as the cancel
// snapshot. The query itself is NOT cleared — the operator can refine an
// existing filter rather than starting over.
func (f *Filter) Open() {
	f.snapshot = f.input.Value()
	f.active = true
	f.input.Focus()
}

// Close commits the current value and blurs the input. Value is preserved.
func (f *Filter) Close() {
	f.active = false
	f.input.Blur()
}

// Cancel reverts the input value to whatever it was when Open() was last
// called, then closes. Use for "esc while typing".
func (f *Filter) Cancel() {
	f.input.SetValue(f.snapshot)
	f.Close()
}

// Clear empties the value. Use for "esc when input is closed but a query
// is still applied" or for explicit clear keys (e.g. ctrl+l).
func (f *Filter) Clear() {
	f.input.SetValue("")
	f.snapshot = ""
}

func (f *Filter) Active() bool   { return f.active }
func (f *Filter) Value() string  { return strings.TrimSpace(f.input.Value()) }
func (f *Filter) View() string   { return f.input.View() }
func (f *Filter) SetWidth(w int) { f.input.SetWidth(w) }

// Update forwards a key message to the underlying textinput.
func (f *Filter) Update(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	f.input, cmd = f.input.Update(msg)
	return cmd
}

// FilterKeyAction is the outcome of HandleKey: which lifecycle transition
// (if any) the section should react to. The Edit case carries the text-input
// command produced by typing into the filter.
type FilterKeyAction int

const (
	// FilterEdit means the keystroke modified the filter value; the section
	// should re-run its match pass and refresh its viewport.
	FilterEdit FilterKeyAction = iota
	// FilterCommit means the user pressed Enter — keep the value, close the
	// input. The section should re-size its viewport (the filter is no
	// longer visible so the row layout may change).
	FilterCommit
	// FilterCancel means the user pressed Esc — revert to the snapshot taken
	// at Open() and close. The section should re-run match pass + refresh +
	// resize so the rendered content reflects the reverted query.
	FilterCancel
)

// HandleKey runs the section's "filter is active" key dispatch in one place.
// Returns the textinput's Cmd (only meaningful on Edit) and which action the
// section should react to. Previously each modal hand-rolled the same
// esc/enter/typed branch with subtle differences — centralising here makes
// the lifecycle uniform and lets the dispatcher evolve once.
func (f *Filter) HandleKey(msg tea.KeyPressMsg) (tea.Cmd, FilterKeyAction) {
	switch msg.String() {
	case "esc":
		f.Cancel()
		return nil, FilterCancel
	case "enter":
		f.Close()
		return nil, FilterCommit
	}
	return f.Update(msg), FilterEdit
}

// FuzzyMatch returns the indices of items whose strings best match query, in
// rank order. Empty query returns nil so callers can short-circuit.
func FuzzyMatch(query string, items []string) []int {
	return FuzzyMatchIter(query, slices.Values(items))
}

// FuzzyMatchIter matches names in iteration order without an intermediate name
// slice. Returned indices refer to that order; equal scores keep their order.
func FuzzyMatchIter(query string, items iter.Seq[string]) []int {
	q := strings.TrimSpace(query)
	if q == "" {
		return nil
	}
	matches := fuzzy.FindFromIter(q, items)
	out := make([]int, 0, len(matches))
	for _, m := range matches {
		out = append(out, m.Index)
	}
	return out
}
