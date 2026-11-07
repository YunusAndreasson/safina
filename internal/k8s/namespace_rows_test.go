package k8s

import (
	"context"
	"errors"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListNamespaceRows_BoundsConcurrency(t *testing.T) {
	namespaces := make([]string, 40)
	for i := range namespaces {
		namespaces[i] = strconv.Itoa(i)
	}
	started := make(chan struct{}, len(namespaces))
	release := make(chan struct{})
	defer close(release)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var active, peak atomic.Int32
	done := make(chan error, 1)
	go func() {
		rows, err := listNamespaceRows(ctx, namespaces, "test", func(ctx context.Context, ns string) ([]string, error) {
			n := active.Add(1)
			defer active.Add(-1)
			for old := peak.Load(); n > old; old = peak.Load() {
				if peak.CompareAndSwap(old, n) {
					break
				}
			}
			started <- struct{}{}
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			return []string{ns}, nil
		}, func(s string) string { return s })
		if err == nil && len(rows) != len(namespaces) {
			err = errors.New("missing rows")
		}
		done <- err
	}()
	for range namespaceListConcurrency {
		select {
		case <-started:
		case <-ctx.Done():
			t.Fatal("workers did not start")
		}
	}
	assert.Equal(t, int32(namespaceListConcurrency), active.Load())
	// Release all workers without closing the channel twice on assertion failure.
	for range namespaces {
		select {
		case release <- struct{}{}:
		case <-ctx.Done():
			t.Fatal("workers did not finish")
		}
	}
	require.NoError(t, <-done)
	assert.LessOrEqual(t, peak.Load(), int32(namespaceListConcurrency))
}

func TestListNamespaceRows_Failures(t *testing.T) {
	denied := errors.New("denied")
	convert := func(s string) string { return s }
	list := func(_ context.Context, ns string) ([]string, error) {
		if ns == "denied" {
			return nil, denied
		}
		return []string{ns}, nil
	}
	rows, err := listNamespaceRows(t.Context(), []string{"denied", "allowed"}, "test", list, convert)
	require.NoError(t, err)
	assert.Equal(t, []string{"allowed"}, rows)
	_, err = listNamespaceRows(t.Context(), []string{"denied"}, "test", list, convert)
	require.ErrorIs(t, err, denied)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = listNamespaceRows(ctx, []string{"allowed"}, "test", list, convert)
	require.ErrorIs(t, err, context.Canceled)
	rows, err = listNamespaceRows(t.Context(), nil, "test", list, convert)
	require.NoError(t, err)
	assert.Empty(t, rows)
}
