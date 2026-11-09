package k8s

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"k8s.io/client-go/rest"
)

func TestLogStreamOutlivesOrdinaryRequestTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = fmt.Fprintln(w, "first")
		_ = http.NewResponseController(w).Flush()
		select {
		case <-r.Context().Done():
			return
		case <-time.After(100 * time.Millisecond):
		}
		_, _ = fmt.Fprintln(w, "still streaming")
		_ = http.NewResponseController(w).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	cfg := &rest.Config{Host: server.URL, Timeout: 20 * time.Millisecond}
	client, err := newStreamingClient(cfg, nil)
	require.NoError(t, err)
	require.Equal(t, 20*time.Millisecond, cfg.Timeout, "one-shot client keeps its timeout")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	events := make(chan PodLogEvent, 8)
	go StreamPodContainerLogs(ctx, client, "ns", "pod", "", false, events)
	var output strings.Builder
	for {
		select {
		case event, ok := <-events:
			require.True(t, ok, "stream ended before second record")
			require.NoError(t, event.Err)
			output.WriteString(event.Line)
			if strings.Contains(output.String(), "still streaming") {
				return
			}
		case <-ctx.Done():
			t.Fatal("did not receive the late stream record")
		}
	}
}
