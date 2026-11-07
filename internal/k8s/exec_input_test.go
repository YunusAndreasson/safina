package k8s

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"k8s.io/client-go/tools/remotecommand"
)

type inputExecutor struct {
	run func(context.Context, remotecommand.StreamOptions) error
}

func (e inputExecutor) Stream(options remotecommand.StreamOptions) error {
	return e.run(context.Background(), options)
}

func (e inputExecutor) StreamWithContext(ctx context.Context, options remotecommand.StreamOptions) error {
	return e.run(ctx, options)
}

func TestSessionInputOrderAndOwnership(t *testing.T) {
	ready := make(chan struct{})
	got := make(chan string, 1)
	session := startSession(t.Context(), inputExecutor{func(_ context.Context, opts remotecommand.StreamOptions) error {
		<-ready
		data := make([]byte, 6)
		_, err := io.ReadFull(opts.Stdin, data)
		got <- string(data)
		return err
	}})
	t.Cleanup(session.Close)
	data := []byte("abc")
	require.NoError(t, session.SendInput(data))
	copy(data, "xxx")
	require.NoError(t, session.SendInput([]byte("def")))
	close(ready)
	select {
	case value := <-got:
		require.Equal(t, "abcdef", value)
	case <-time.After(5 * time.Second):
		t.Fatal("input stalled")
	}
	for event := range session.Events {
		require.NoError(t, event.Err)
	}
	require.Eventually(t, func() bool { return session.SendInput([]byte("x")) != nil }, time.Second, time.Millisecond)
}

func TestSessionInputBackpressureAndClose(t *testing.T) {
	session := startSession(t.Context(), inputExecutor{func(ctx context.Context, _ remotecommand.StreamOptions) error {
		<-ctx.Done()
		return ctx.Err()
	}})
	t.Cleanup(session.Close)
	require.ErrorContains(t, session.SendInput(make([]byte, 65537)), "64 KiB")
	// The executor never reads stdin: one write may block, but the UI must not.
	finished := make(chan error, 1)
	go func() {
		for range 18 {
			if err := session.SendInput([]byte("x")); err != nil {
				finished <- err
				return
			}
		}
		finished <- nil
	}()
	select {
	case err := <-finished:
		require.ErrorContains(t, err, "queue is full")
	case <-time.After(5 * time.Second):
		t.Fatal("UI blocked on stdin")
	}
	session.Close()
	session.Close()
	select {
	case <-session.done:
	case <-time.After(5 * time.Second):
		t.Fatal("session did not close")
	}
	require.Error(t, session.SendInput([]byte("x")))
	for range session.Events {
	}
}
