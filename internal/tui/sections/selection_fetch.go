package sections

import (
	"context"

	tea "charm.land/bubbletea/v2"
)

func cancelSelectionFetch(cancel *context.CancelFunc) {
	if *cancel != nil {
		(*cancel)()
		*cancel = nil
	}
}

// A selection owns one request. Cancel queued/active work when it changes and
// unregister completed child contexts from the process-lifetime parent.
func selectionFetch(parent context.Context, cancel *context.CancelFunc, build func(context.Context) tea.Cmd) tea.Cmd {
	cancelSelectionFetch(cancel)
	ctx, done := context.WithCancel(parent)
	*cancel = done
	cmd := build(ctx)
	return func() tea.Msg { defer done(); return cmd() }
}
