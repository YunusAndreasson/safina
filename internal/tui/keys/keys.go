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
	// Wrap toggles soft line-wrap inside the long-content workspace
	// viewports (logs, events, describe). Default off — lines
	// truncate at panel width, matching every other log viewer the
	// on-caller has seen — and a single keystroke flips it for the
	// times a long panic / wide JSON line needs to read in one
	// glance. Lowercase per the read-action convention.
	Wrap key.Binding
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
	// PortForward opens the port-forward input modal for a Pod or Service.
	// Lower-case `f` is free.
	PortForward key.Binding
	// Forwards opens the active-forwards list from anywhere in the app —
	// users need a hotkey that works while focused on chat, logs, etc.,
	// not just on the originating row.
	Forwards key.Binding
	// Cordon toggles a node's schedulable state (one verb in the UI,
	// cordon-or-uncordon under the hood — kubectl's split is leaked
	// plumbing the on-caller doesn't need).
	Cordon key.Binding
	// Drain evicts a node's pods. Uppercase D to keep `d` (Describe) intact.
	Drain key.Binding
	// Hot opens the noisy-neighbours modal — top-N pods cluster-wide
	// sorted by CPU usage. Read-only and cluster-global; lowercase per
	// the convention (uppercase is reserved for mutators).
	Hot key.Binding
	// Configure opens the Resource Browser modal — the user picks which
	// kubernetes resource kinds appear as folds in the tree. Bound to
	// `ctrl+b` (Browse): a chord keeps it out of the alphabet collision
	// space and pairs naturally with `ctrl+f` (forwards list) and
	// `ctrl+k` (cluster switch) as the meta-config chord cluster.
	Configure key.Binding
	// ClusterSwitch (Ctrl+K) opens the mid-session cluster-switcher
	// overlay. Memory rule "no default cluster" governs the cold start;
	// this is the discoverable escape hatch for "I want to look at a
	// different cluster right now" without popping back to the root.
	ClusterSwitch key.Binding
}

// Default builds the standard keybindings.
func Default() Map {
	return Map{
		// "up" next to ↑ is restating the obvious — same for ↓, ↵, esc.
		// The arrow glyphs already carry the meaning; drop the labels so the
		// help modal's description column reserves space for keys that aren't
		// universally known.
		Up:    key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "")),
		Down:  key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "")),
		Enter: key.NewBinding(key.WithKeys("enter"), key.WithHelp("↵", "")),
		Back:  key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),
		Quit:  key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
		// Delete uses K (kill, unix convention) rather than ctrl+d — the
		// chord collides with EOF muscle memory in every shell/REPL, and a
		// shifted single letter is faster from the home row anyway. K
		// shares its "loud verb" feel with the rest of the mutator block.
		Delete:   key.NewBinding(key.WithKeys("K"), key.WithHelp("K", "delete")),
		Describe: key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "describe")),
		// Convention: lowercase = read-only; uppercase = mutates cluster
		// state. Reading is safe to fat-finger; mutating requires the shift
		// key. Same hierarchy as vi's d/D. The uppercase letters are picked
		// to be mnemonic, not to spell the user-facing verb — operators
		// recognise "rollback" instantly, so the help text uses that across
		// footer/modal/banner even though the key is `U` (for "undo").
		Scale:    key.NewBinding(key.WithKeys("S"), key.WithHelp("S", "scale")),
		Rollback: key.NewBinding(key.WithKeys("U"), key.WithHelp("U", "rollback")),
		Restart:  key.NewBinding(key.WithKeys("R"), key.WithHelp("R", "restart")),
		Logs:     key.NewBinding(key.WithKeys("l"), key.WithHelp("l", "logs")),
		Events:   key.NewBinding(key.WithKeys("e"), key.WithHelp("e", "events")),
		Shell:    key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "shell")),
		Help:     key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
		Refresh:  key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh")),
		Filter:   key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "filter")),

		// FirstFail uses `n` to match vim's next-search-match convention.
		// Read-only navigation jump → lowercase.
		FirstFail: key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "next fail")),
		Wrap:      key.NewBinding(key.WithKeys("w"), key.WithHelp("w", "wrap")),
		Ask:       key.NewBinding(key.WithKeys("i"), key.WithHelp("i", "chat")),
		Yank:      key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "copy")),
		Reveal:    key.NewBinding(key.WithKeys("v"), key.WithHelp("v", "reveal secret")),
		Edit:      key.NewBinding(key.WithKeys("E"), key.WithHelp("E", "edit yaml")),

		PortForward: key.NewBinding(key.WithKeys("f"), key.WithHelp("f", "forward")),
		Forwards:    key.NewBinding(key.WithKeys("ctrl+f"), key.WithHelp("ctrl+f", "forwards")),

		Cordon: key.NewBinding(key.WithKeys("C"), key.WithHelp("C", "cordon")),
		Drain:  key.NewBinding(key.WithKeys("D"), key.WithHelp("D", "drain")),

		Hot: key.NewBinding(key.WithKeys("h"), key.WithHelp("h", "hot pods")),

		Configure: key.NewBinding(key.WithKeys("ctrl+b"), key.WithHelp("ctrl+b", "browse kinds")),

		// Ctrl+K is the de-facto "command palette / quick-switcher" chord
		// in every modern editor (VS Code, Linear, Notion). We claim it
		// for the cluster switcher since `:` is already the entry-resource
		// palette — distinct concept, distinct chord.
		ClusterSwitch: key.NewBinding(key.WithKeys("ctrl+k"), key.WithHelp("ctrl+k", "switch cluster")),
	}
}

func (k Map) ShortHelp() []key.Binding {
	return []key.Binding{k.Up, k.Down, k.Enter, k.Back, k.Filter, k.FirstFail, k.Ask, k.Yank, k.Logs, k.Events, k.Configure, k.Help, k.Quit}
}

func (k Map) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.Enter, k.Back},
		// Row 2: reads (lowercase).
		{k.Describe, k.Logs, k.Events, k.Shell, k.Reveal, k.PortForward, k.FirstFail, k.Filter},
		// Row 3: mutators (uppercase / ctrl-chord).
		{k.Scale, k.Rollback, k.Restart, k.Edit, k.Cordon, k.Drain, k.Delete},
		// Row 4: chrome / global affordances.
		{k.Refresh, k.Ask, k.Yank, k.Forwards, k.Configure, k.Help, k.Quit},
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
			// All lowercase: read-only verbs. Safe to fat-finger.
			Title:    "inspect",
			Bindings: []key.Binding{k.Describe, k.Logs, k.Events, k.Shell, k.Reveal, k.PortForward, k.FirstFail},
		},
		{
			// All uppercase (or ctrl-chord): each mutates cluster state.
			// Ordered roughly safest-to-most-destructive so the eye walks
			// down with rising stakes.
			Title:    "change",
			Bindings: []key.Binding{k.Scale, k.Rollback, k.Restart, k.Edit, k.Cordon, k.Drain, k.Delete},
		},
		{
			Title: "meta",
			Pairs: []KeyPair{
				{Key: "↑↓", Desc: "navigate"},
				{Key: "tab", Desc: "switch pane"},
				{Key: "z", Desc: "expand reading pane"},
				{Key: "←→", Desc: "workspace tabs"},
				{Key: ":", Desc: "search loaded resources"},
				{Key: "ctrl+s", Desc: "expand palette to namespace"},
				{Key: "ctrl+n", Desc: "notification history"},
				{Key: "ctrl+d", Desc: "dismiss notification"},
				{Key: "c / p", Desc: "log targets / previous logs"},
				{Key: "↵", Desc: "drill / confirm"},
			},
			// k.Back pulls its own "esc → back" label from the binding now,
			// so we don't carry a hardcoded Pair for it. Anything that renders
			// Back.Help() elsewhere gets the same description.
			Bindings: []key.Binding{k.Back, k.Filter, k.Refresh, k.Ask, k.Yank, k.Hot, k.Forwards, k.Configure, k.ClusterSwitch, k.Help, k.Quit},
		},
	}
}
