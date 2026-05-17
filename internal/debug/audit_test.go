package debug

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// roundTripFunc lets a test stand in for http.RoundTripper without spinning
// up a server when we don't need network behavior.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestAuditTransport_PassthroughWhenDisabled(t *testing.T) {
	// Ensure the package starts clean — another test in the suite may have
	// flipped Init() on.
	Close()
	called := false
	base := roundTripFunc(func(*http.Request) (*http.Response, error) {
		called = true
		return &http.Response{StatusCode: 200, Body: http.NoBody}, nil
	})
	wrapped := WrapTransport(base)
	req := &http.Request{Method: "GET", URL: &url.URL{Path: "/api/v1/pods"}}
	resp, err := wrapped.RoundTrip(req)
	if err != nil {
		t.Fatalf("roundtrip: %v", err)
	}
	if resp.Body != nil {
		_ = resp.Body.Close()
	}
	if resp.StatusCode != 200 || !called {
		t.Fatalf("base transport was not invoked")
	}
}

func TestAuditTransport_LogsWhenEnabled(t *testing.T) {
	// Arrange a fresh sink in a tempfile so we can read what was written.
	Close()
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")
	t.Setenv("SAFINA_DEBUG", "1")
	t.Setenv("SAFINA_DEBUG_FILE", path)
	if got := Init(); got != path {
		t.Fatalf("Init path = %q, want %q", got, path)
	}
	t.Cleanup(Close)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(404)
	}))
	t.Cleanup(srv.Close)

	wrapped := WrapTransport(http.DefaultTransport)
	req, _ := http.NewRequestWithContext(context.Background(), "GET", srv.URL+"/api/v1/pods", nil)
	resp, err := wrapped.RoundTrip(req)
	if err != nil {
		t.Fatalf("roundtrip: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	// Force a flush by closing the sink, then re-read the file.
	Close()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	contents := string(b)
	if !strings.Contains(contents, `"msg":"kube.api"`) {
		t.Fatalf("kube.api event missing from log: %s", contents)
	}
	if !strings.Contains(contents, `"verb":"GET"`) {
		t.Fatalf("verb missing from log: %s", contents)
	}
	if !strings.Contains(contents, `"status":404`) {
		t.Fatalf("status missing from log: %s", contents)
	}
	// Authorization must never appear — we only inspect method/path/status.
	if strings.Contains(strings.ToLower(contents), "authorization") {
		t.Fatalf("authorization leaked into log: %s", contents)
	}
}
