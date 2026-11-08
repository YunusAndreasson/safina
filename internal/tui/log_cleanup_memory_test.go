package tui

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/YunusAndreasson/safina/internal/config"
	"github.com/YunusAndreasson/safina/internal/history"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/YunusAndreasson/safina/internal/k8s"
	"github.com/YunusAndreasson/safina/internal/tui/sections"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

func TestDroppingLogChannelsDoesNotStartUnboundedDrainers(t *testing.T) {
	for _, stale := range []bool{false, true} {
		ignore := goleak.IgnoreCurrent()
		m := shiftFooterModel(t)
		events := make(chan k8s.PodLogEvent)
		// Deliberately leave this producer-less channel open until after checking.
		// No cancellation path should need another goroutine to read it forever.
		defer close(events)
		if stale {
			m.Update(sections.LogStreamStartedMsg{Request: m.logRequest + 1, Ch: events})
		} else {
			m.logCh = events
			m.stopLogStream()
		}
		require.Nil(t, m.logCh)
		goleak.VerifyNone(t, ignore)
	}
}

func TestRelationshipBackHistoryIsBounded(t *testing.T) {
	m := shiftFooterModel(t)
	for range maxConnectedHistory * 4 {
		m.rememberConnected(sections.PaletteEntry{Name: "source", Path: m.tree.Path()})
	}
	require.Len(t, m.connectedBack, maxConnectedHistory)
}

func TestCanceledLogStreamClosesAndRecordsOneTerminalOutcome(t *testing.T) {
	sent := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintln(w, "record")
		_ = http.NewResponseController(w).Flush()
		close(sent)
		<-r.Context().Done()
	}))
	defer server.Close()
	evidence := false
	store, err := history.Open(history.Options{Directory: t.TempDir(), Evidence: &evidence})
	require.NoError(t, err)
	defer func() { _ = store.Close() }()
	cs, err := kubernetes.NewForConfig(&rest.Config{Host: server.URL})
	require.NoError(t, err)
	m := NewModel(&k8s.Manager{History: store, Clients: map[string]*k8s.Client{"demo": {Clientset: cs, StreamClientset: cs}}}, &config.AppConfig{})
	t.Cleanup(m.ctx.Cancel)
	m.ctx.ActiveContext = "demo"
	ready, ok := m.startTargetLogs(logTarget{namespace: "shop", pod: "pod", container: "app"}, false)().(sections.LogStreamStartedMsg)
	require.True(t, ok)
	defer m.stopLogStream()
	select {
	case <-sent:
	case <-time.After(time.Second):
		t.Fatal("stream request did not start")
	}
	m.stopLogStream()
	closed := make(chan struct{})
	go func() {
		for range ready.Ch {
		}
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("canceled bridge did not close")
	}
	result, err := history.Query(store.Directory(), history.Filter{Actions: []string{"logs"}})
	require.NoError(t, err)
	terminal := 0
	for _, record := range result.Records {
		if record.Outcome == "cancelled" {
			terminal++
		}
	}
	require.Equal(t, 1, terminal)
}
