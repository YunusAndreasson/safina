package keys

import "charm.land/bubbles/v2/key"

// Map is the global keymap. It also implements bubbles/help.KeyMap so it can be
// passed straight to help.View / help.ShortHelpView.
type Map struct {
	Up       key.Binding
	Down     key.Binding
	Enter    key.Binding
	Back     key.Binding
	Quit     key.Binding
	Delete   key.Binding
	Describe key.Binding
	Scale    key.Binding
	Rollback key.Binding
	Restart  key.Binding
	Logs     key.Binding
	Events   key.Binding
	Shell    key.Binding
	Help     key.Binding
	Refresh  key.Binding
	Filter   key.Binding
	// Tree-navigator additions:
	FirstFail key.Binding
	// Ask focuses the chat panel (vim-style "insert mode" jump from tree
	// to typing). Tab also works; Ask is the discoverable alias.
	Ask key.Binding
	// Yank copies the section's payload (resource path, log buffer, YAML,
	// last assistant turn) to the system clipboard via OSC52 — works over
	// SSH and through tmux/zellij with passthrough.
	Yank key.Binding
	// Reveal decodes a Secret's data and shows it in the describe modal.
	// Scoped to Secret rows; harmless elsewhere. Separate from Describe
	// because `d` should keep kubectl's (base64) semantics.
	Reveal key.Binding
	// Edit opens the resource's YAML in $EDITOR. After the editor exits,
	// the diff is gated behind a y/n confirm before any write hits the
	// cluster. Uppercase E because lowercase e is taken by Events.
	Edit key.Binding
}

// Default builds the standard keybindings.
func Default() Map {
	return Map{
		// "up" next to ↑ is restating the obvious — same for ↓, ↵, esc.
		// The arrow glyphs already carry the meaning; drop the labels so the
		// help modal's description column reserves space for keys that aren't
		// universally known.
		Up:       key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "")),
		Down:     key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "")),
		Enter:    key.NewBinding(key.WithKeys("enter"), key.WithHelp("↵", "")),
		Back:     key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "")),
		Quit:     key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
		Delete:   key.NewBinding(key.WithKeys("ctrl+d"), key.WithHelp("ctrl+d", "delete pod")),
		Describe: key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "describe")),
		Scale:    key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "scale")),
		Rollback: key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "revert")),
		Restart:  key.NewBinding(key.WithKeys("x"), key.WithHelp("x", "restart")),
		Logs:     key.NewBinding(key.WithKeys("l"), key.WithHelp("l", "logs")),
		Events:   key.NewBinding(key.WithKeys("e"), key.WithHelp("e", "events")),
		Shell:    key.NewBinding(key.WithKeys("S"), key.WithHelp("S", "shell")),
		Help:     key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
		Refresh:  key.NewBinding(key.WithKeys("R"), key.WithHelp("R", "refresh")),
		Filter:   key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "filter")),

		FirstFail: key.NewBinding(key.WithKeys("F"), key.WithHelp("F", "next fail")),
		Ask:       key.NewBinding(key.WithKeys("i"), key.WithHelp("i", "ask (tab also)")),
		Yank:      key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "copy")),
		Reveal:    key.NewBinding(key.WithKeys("v"), key.WithHelp("v", "reveal secret")),
		Edit:      key.NewBinding(key.WithKeys("E"), key.WithHelp("E", "edit yaml")),
	}
}

func (k Map) ShortHelp() []key.Binding {
	return []key.Binding{k.Up, k.Down, k.Enter, k.Back, k.Filter, k.FirstFail, k.Ask, k.Yank, k.Logs, k.Events, k.Help, k.Quit}
}

func (k Map) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.Enter, k.Back},
		{k.Filter, k.FirstFail, k.Logs, k.Events, k.Describe, k.Reveal, k.Edit, k.Shell},
		{k.Scale, k.Rollback, k.Restart, k.Delete, k.Refresh},
		{k.Ask, k.Yank, k.Help, k.Quit},
	}
}

// Group is one labeled column in the `?` help modal — keys clustered by
// what the operator is *trying to do* rather than by code structure.
type Group struct {
	Title string
	// Bindings render as `key  desc` rows. Use these for keys that already
	// have meaningful Help() text on the binding itself.
	Bindings []key.Binding
	// Pairs render the same way as Bindings but skip the binding lookup —
	// useful for universal nav (↑↓/↵/esc) whose binding helps are blank by
	// design (the short footer omits them; the help modal teaches them).
	Pairs []KeyPair
}

// KeyPair is a (key-glyph, description) tuple for keys that don't carry a
// description on the binding itself — universal nav, mostly.
type KeyPair struct {
	Key  string
	Desc string
}

// Groups returns the help-modal layout. The intent: an operator scanning
// the modal sees three columns (inspect / change / meta) and lands on the
// verb they want by category, not by letter-hunting through 20 entries.
func (k Map) Groups() []Group {
	return []Group{
		{
			Title:    "inspect",
			Bindings: []key.Binding{k.Describe, k.Logs, k.Events, k.Reveal, k.Shell, k.FirstFail},
		},
		{
			Title:    "change",
			Bindings: []key.Binding{k.Restart, k.Scale, k.Rollback, k.Edit, k.Delete},
		},
		{
			Title: "meta",
			Pairs: []KeyPair{
				{Key: "↑↓", Desc: "navigate"},
				{Key: "↵", Desc: "drill / confirm"},
				{Key: "esc", Desc: "back / close"},
			},
			Bindings: []key.Binding{k.Filter, k.Refresh, k.Ask, k.Yank, k.Help, k.Quit},
		},
	}
}
