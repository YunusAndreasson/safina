package k8s

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"k8s.io/client-go/rest"
)

func TestLogProducerExitsWhenCanceledWithoutAnyReceiver(t *testing.T) {
	for _, alreadyCanceled := range []bool{false, true} {
		t.Run(strconv.FormatBool(alreadyCanceled), func(t *testing.T) {
			sent := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = fmt.Fprintln(w, "record")
				_ = http.NewResponseController(w).Flush()
				close(sent)
				<-r.Context().Done()
			}))
			defer server.Close()
			cs, err := newStreamingClient(&rest.Config{Host: server.URL})
			require.NoError(t, err)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if alreadyCanceled {
				cancel()
			}
			events := make(chan PodLogEvent)
			done := make(chan struct{})
			go func() { defer close(done); StreamPodContainerLogs(ctx, cs, "shop", "pod", "", false, events) }()
			if !alreadyCanceled {
				select {
				case <-sent:
				case <-time.After(time.Second):
					t.Fatal("request never reached log server")
				}
				cancel()
			}
			select {
			case <-done:
			case <-time.After(250 * time.Millisecond):
				// Release the old buggy producer before failing, so reproduction itself
				// does not leave a goroutine behind for suite-wide leak detection.
				go func() {
					for range events {
					}
				}()
				select {
				case <-done:
				case <-time.After(time.Second):
					t.Fatal("producer did not exit even with cleanup receiver")
				}
				t.Fatal("canceled log producer remained blocked without a receiver")
			}
		})
	}
}
