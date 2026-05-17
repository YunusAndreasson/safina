package debug

import (
	"net/http"
	"time"
)

// AuditTransport wraps an http.RoundTripper and emits one slog record per
// Kubernetes API request (verb, path, status, latency) when the debug sink
// is enabled. When disabled the wrapper is a straight pass-through with no
// per-call work — Enabled() is checked before any allocation.
//
// Authorization is never read or echoed; the wrapper only inspects URL.Path,
// Method, and the response status. The audit log is the kubectl `-v=8`
// equivalent an SRE reaches for when something silently fails, scoped to
// metadata that's already plumbed through the http.Request.
type AuditTransport struct {
	Base http.RoundTripper
}

// WrapTransport is the rest.Config.WrapTransport-compatible factory. Pass it
// as cfg.WrapTransport = debug.WrapTransport and every request from the
// resulting client is audited when SAFINA_DEBUG is set.
func WrapTransport(rt http.RoundTripper) http.RoundTripper {
	return &AuditTransport{Base: rt}
}

func (a *AuditTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if !Enabled() {
		return a.Base.RoundTrip(req)
	}
	start := time.Now()
	resp, err := a.Base.RoundTrip(req)
	dur := time.Since(start).Milliseconds()
	if err != nil {
		Errf("kube.api",
			"verb", req.Method,
			"path", req.URL.Path,
			"dur_ms", dur,
			"err", err.Error())
		return resp, err
	}
	Log("kube.api",
		"verb", req.Method,
		"path", req.URL.Path,
		"status", resp.StatusCode,
		"dur_ms", dur)
	return resp, nil
}
