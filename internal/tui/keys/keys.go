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

		FirstFail: key.NewBinding(key.WithKeys("F"), key.WithHelp("F", "first fail")),
		Ask:       key.NewBinding(key.WithKeys("i"), key.WithHelp("i", "ask")),
		Yank:      key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "copy")),
	}
}

func (k Map) ShortHelp() []key.Binding {
	return []key.Binding{k.Up, k.Down, k.Enter, k.Back, k.Filter, k.FirstFail, k.Ask, k.Yank, k.Logs, k.Events, k.Help, k.Quit}
}

func (k Map) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.Enter, k.Back},
		{k.Filter, k.FirstFail, k.Logs, k.Events, k.Describe, k.Shell},
		{k.Scale, k.Rollback, k.Restart, k.Delete, k.Refresh},
		{k.Ask, k.Yank, k.Help, k.Quit},
	}
}
