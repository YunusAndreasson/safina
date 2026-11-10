package main

import (
	"context"
	"io"
	"log"
	"maps"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// Traffic counts what a tool asked of the API server.
type Traffic struct {
	Requests int64 `json:"requests"`
	Lists    int64 `json:"lists"`
	Watches  int64 `json:"watches"`
	Bytes    int64 `json:"bytes"`
}

func (t Traffic) Sub(o Traffic) Traffic {
	return Traffic{t.Requests - o.Requests, t.Lists - o.Lists, t.Watches - o.Watches, t.Bytes - o.Bytes}
}

// Proxy sits between both tools and the cluster. It holds the real
// credentials, so the tools see the same unauthenticated local endpoint, and
// every request pays the same added round trip.
type Proxy struct {
	URL      string
	RTT      time.Duration
	srv      *http.Server
	requests atomic.Int64
	lists    atomic.Int64
	watches  atomic.Int64
	bytes    atomic.Int64

	mu        sync.Mutex
	endpoints map[string]int64
}

func StartProxy(kubeconfig, kubeContext string, rtt time.Duration) (*Proxy, error) {
	loading := &clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeconfig}
	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loading, &clientcmd.ConfigOverrides{CurrentContext: kubeContext}).ClientConfig()
	if err != nil {
		return nil, err
	}
	upstream, err := url.Parse(cfg.Host)
	if err != nil {
		return nil, err
	}
	transport, err := rest.TransportFor(cfg)
	if err != nil {
		return nil, err
	}
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	p := &Proxy{URL: "http://" + ln.Addr().String(), RTT: rtt, endpoints: map[string]int64{}}
	rp := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(upstream)
			r.Out.Host = upstream.Host
		},
		Transport:     transport,
		FlushInterval: -1,
		// Tools cancel watches on every view change; the proxy logs nothing.
		ErrorLog: log.New(io.Discard, "", 0),
	}
	p.srv = &http.Server{ReadHeaderTimeout: 10 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.requests.Add(1)
		verb := endpointVerb(r)
		switch verb {
		case "WATCH":
			p.watches.Add(1)
		case "LIST":
			p.lists.Add(1)
		}
		p.mu.Lock()
		p.endpoints[endpointKey(verb, r)]++
		p.mu.Unlock()
		if p.RTT > 0 {
			time.Sleep(p.RTT)
		}
		rp.ServeHTTP(&countingWriter{ResponseWriter: w, n: &p.bytes}, r)
	})}
	go func() { _ = p.srv.Serve(ln) }()
	return p, nil
}

// isCollection reports whether an API path names a collection rather than one
// object: /api/v1/pods, /api/v1/namespaces/x/pods, /apis/g/v/namespaces/x/r.
func isCollection(path string) bool {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	var rest []string
	switch {
	case len(parts) >= 2 && parts[0] == "api":
		rest = parts[2:]
	case len(parts) >= 3 && parts[0] == "apis":
		rest = parts[3:]
	default:
		return false
	}
	if len(rest) >= 2 && rest[0] == "namespaces" {
		rest = rest[2:]
	}
	return len(rest) == 1
}

func endpointVerb(r *http.Request) string {
	q := r.URL.Query()
	switch {
	case q.Get("watch") == "true" || q.Get("watch") == "1" || strings.Contains(r.URL.Path, "/watch/"):
		return "WATCH"
	case r.Method == http.MethodGet && isCollection(r.URL.Path):
		return "LIST"
	default:
		return r.Method
	}
}

// endpointKey names a request without the names in it, so requests for
// different pods or namespaces group together: "GET /api/v1/namespaces/{ns}/pods/{name}".
func endpointKey(verb string, r *http.Request) string {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	resource := -1 // index of the resource segment
	switch {
	case len(parts) >= 2 && parts[0] == "api":
		resource = 2
	case len(parts) >= 3 && parts[0] == "apis":
		resource = 3
	}
	if resource >= 0 {
		if resource+1 < len(parts) && parts[resource] == "watch" {
			resource++
		}
		if resource+2 < len(parts) && parts[resource] == "namespaces" {
			parts[resource+1] = "{ns}"
			resource += 2
		}
		if resource+1 < len(parts) {
			parts[resource+1] = "{name}"
		}
	}
	key := verb + " /" + strings.Join(parts, "/")
	q := r.URL.Query()
	if q.Get("labelSelector") != "" {
		key += " ?labelSelector"
	}
	if q.Get("fieldSelector") != "" {
		key += " ?fieldSelector"
	}
	return key
}

// Endpoints returns request counts by endpoint so far.
func (p *Proxy) Endpoints() map[string]int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return maps.Clone(p.endpoints)
}

// SubEndpoints is now minus before, without zero entries.
func SubEndpoints(now, before map[string]int64) map[string]int64 {
	out := map[string]int64{}
	for k, v := range now {
		if d := v - before[k]; d > 0 {
			out[k] = d
		}
	}
	return out
}

func (p *Proxy) Traffic() Traffic {
	return Traffic{p.requests.Load(), p.lists.Load(), p.watches.Load(), p.bytes.Load()}
}

func (p *Proxy) Close() { _ = p.srv.Close() }

// WriteKubeconfig writes a kubeconfig whose only context, named like the
// real one, points at the proxy.
func (p *Proxy) WriteKubeconfig(dir, kubeContext string) (string, error) {
	cfg := clientcmdapi.NewConfig()
	cfg.Clusters[kubeContext] = &clientcmdapi.Cluster{Server: p.URL}
	cfg.AuthInfos[kubeContext] = &clientcmdapi.AuthInfo{}
	cfg.Contexts[kubeContext] = &clientcmdapi.Context{Cluster: kubeContext, AuthInfo: kubeContext, Namespace: "default"}
	cfg.CurrentContext = kubeContext
	path := filepath.Join(dir, "kubeconfig")
	if err := clientcmd.WriteToFile(*cfg, path); err != nil {
		return "", err
	}
	return path, os.Chmod(path, 0o600)
}

type countingWriter struct {
	http.ResponseWriter
	n *atomic.Int64
}

func (c *countingWriter) Write(b []byte) (int, error) {
	n, err := c.ResponseWriter.Write(b)
	c.n.Add(int64(n))
	return n, err
}

func (c *countingWriter) Flush() {
	if f, ok := c.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (c *countingWriter) Unwrap() http.ResponseWriter { return c.ResponseWriter }
