package sections

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"
)

// selectionFetchDelay is how long the cursor must rest on a row before its
// detail requests go out. Holding j across twenty rows then fetches one
// row's details instead of twenty, and the highlight's frame no longer
// competes with requests for rows already left behind. Cached details
// render without waiting (callers only fetch on a cache miss).
const selectionFetchDelay = 120 * time.Millisecond

func cancelSelectionFetch(cancel *context.CancelFunc) {
	if *cancel != nil {
		(*cancel)()
		*cancel = nil
	}
}

// A selection owns one request. Cancel queued/active work when it changes and
// unregister completed child contexts from the process-lifetime parent. The
// request waits out selectionFetchDelay first; a selection change during the
// wait cancels it before anything is sent.
func selectionFetch(parent context.Context, cancel *context.CancelFunc, build func(context.Context) tea.Cmd) tea.Cmd {
	return delayedSelectionFetch(parent, cancel, selectionFetchDelay, build)
}

func delayedSelectionFetch(parent context.Context, cancel *context.CancelFunc, delay time.Duration, build func(context.Context) tea.Cmd) tea.Cmd {
	cancelSelectionFetch(cancel)
	ctx, done := context.WithCancel(parent)
	*cancel = done
	cmd := build(ctx)
	return func() tea.Msg {
		defer done()
		if delay > 0 {
			timer := time.NewTimer(delay)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return nil // superseded while waiting: nothing was requested
			case <-timer.C:
			}
		}
		return cmd()
	}
}
