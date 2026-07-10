package k8s

import (
	"fmt"
	"net/url"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/streaming/pkg/httpstream"
)

// streamFallback decides when a WebSocket-first streaming attempt (exec,
// attach, port-forward) should be retried over legacy SPDY: the apiserver or
// an intermediary rejected the protocol upgrade, or an HTTPS proxy broke the
// websocket dial. Same predicate kubectl ships with — anything else (auth
// failure, pod gone, network down) would fail identically on SPDY, so
// retrying would only mask the real error.
func streamFallback(err error) bool {
	return httpstream.IsUpgradeFailure(err) || httpstream.IsHTTPSProxyError(err)
}

// newStreamExecutor builds the kubectl-standard executor for exec/attach:
// WebSocket (V5 — faithful exit-code propagation, plays well with HTTP/2-only
// load balancers) first, transparent SPDY retry when the upgrade path is what
// failed. Every cluster safina touches is production, so a session must come
// up whichever of the two protocols the cluster's ingress path lets through.
func newStreamExecutor(restCfg *rest.Config, reqURL *url.URL) (remotecommand.Executor, error) {
	ws, err := remotecommand.NewWebSocketExecutor(restCfg, "GET", reqURL.String())
	if err != nil {
		return nil, fmt.Errorf("create websocket executor: %w", err)
	}
	spdyExec, err := remotecommand.NewSPDYExecutor(restCfg, "POST", reqURL)
	if err != nil {
		return nil, fmt.Errorf("create spdy executor: %w", err)
	}
	return remotecommand.NewFallbackExecutor(ws, spdyExec, streamFallback)
}
