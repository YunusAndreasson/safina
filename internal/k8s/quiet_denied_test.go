package k8s

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

// An informer's retries for a kind the account may not list are answered
// from memory; other paths, and anything but a 403 or 404, still go out.
func TestQuietDeniedReplaysRefusals(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		switch r.URL.Path {
		case "/apis/apps/v1/namespaces/tenant/daemonsets":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, `{"kind":"Status","reason":"Forbidden","code":403}`)
		default:
			_, _ = io.WriteString(w, `{"kind":"List","items":[]}`)
		}
	}))
	t.Cleanup(server.Close)
	client := &http.Client{Transport: &quietDenied{next: http.DefaultTransport}}
	get := func(path string) (int, string) {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+path, nil)
		require.NoError(t, err)
		resp, err := client.Do(req)
		require.NoError(t, err)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		return resp.StatusCode, string(body)
	}

	for range 3 {
		status, body := get("/apis/apps/v1/namespaces/tenant/daemonsets?watch=true")
		require.Equal(t, http.StatusForbidden, status)
		require.Contains(t, body, "Forbidden", "the replay carries the apiserver's own Status")
	}
	require.Equal(t, int32(1), hits.Load(), "one refusal reaches the server, the retries don't")

	for range 2 {
		status, _ := get("/apis/apps/v1/namespaces/tenant/deployments")
		require.Equal(t, http.StatusOK, status)
	}
	require.Equal(t, int32(3), hits.Load(), "answers that aren't refusals are never replayed")
}
