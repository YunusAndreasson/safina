package k8s

import (
	"errors"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"github.com/yunus/safina/internal/debug"
	"github.com/yunus/safina/internal/version"
)

type Client struct {
	Clientset kubernetes.Interface
	// StreamClientset has no whole-request timeout; its caller owns cancellation.
	StreamClientset kubernetes.Interface
	RESTConfig      *rest.Config
	// Discovery enumerates the GVRs the API server exposes — used by the
	// Resource Browser to populate "every kind in this cluster, including
	// CRDs". Reads through the same wrapped transport so debug auditing
	// still applies.
	Discovery discovery.DiscoveryInterface
	// Dynamic lists / watches arbitrary GVRs without compile-time types.
	// Used for any resource the tree doesn't have a first-class typed
	// renderer for (CRDs, anything the user enabled beyond the defaults).
	Dynamic dynamic.Interface
	// informers backs the cache-aware Client methods. Lazily started when
	// this context becomes active (Manager.ActivateContext) and stopped when
	// the user leaves it, so watch streams only ever target the cluster on
	// screen. Atomic because tea.Cmd goroutines read it while the lifecycle
	// swaps it. nil (or per-kind unsynced) means "fall back to direct reads".
	informers atomic.Pointer[informerSet]
	// informerCS is the clientset the informer factory consumes: same
	// transport chain as Clientset but negotiating protobuf (bench-validated
	// as the biggest List/Watch bytes-on-wire win — see bench/variants.go).
	// ONLY the factory uses it: protobuf is safe for built-in types but the
	// main clientset's raw metrics path (metrics.go DoRaw → json.Unmarshal)
	// and the dynamic/CRD client must stay JSON. Falls back to Clientset
	// when construction failed.
	informerCS kubernetes.Interface
}

// Informers returns the running informer set for this context, or nil when
// the context isn't active (or the Client is nil). The set's per-kind
// accessors are themselves nil until that kind's initial sync lands, so
// callers can chain without checking: c.Informers().Pods() == nil ⇒ direct.
func (c *Client) Informers() *informerSet {
	if c == nil {
		return nil
	}
	return c.informers.Load()
}

// StartInformers lazily starts this context's informer set. Idempotent and
// non-blocking: watch streams open and sync in the background while callers
// keep rendering off direct API reads.
func (c *Client) StartInformers() {
	if c == nil || c.informers.Load() != nil {
		return
	}
	cs := c.informerCS
	if cs == nil {
		cs = c.Clientset
	}
	set := startInformers(cs)
	// A concurrent activation may have won the race — keep theirs, drop ours.
	if !c.informers.CompareAndSwap(nil, set) {
		set.close()
	}
}

// StopInformers tears down this context's watch streams. Idempotent.
func (c *Client) StopInformers() {
	if c == nil {
		return
	}
	if set := c.informers.Swap(nil); set != nil {
		set.close()
	}
}

// SkippedContext records a kubeconfig context whose Client construction
// failed at startup. We surface these to the user as a one-shot banner so
// "my context is missing from the picker" doesn't become a silent mystery.
type SkippedContext struct {
	Name string
	Err  error
}

type Manager struct {
	Clients map[string]*Client
	// Skipped lists contexts whose REST config or clientset failed to build.
	// The reachability of each cluster is NOT verified at construction (no
	// network call happens in NewClient), so this only catches kubeconfig
	// issues — not unreachable servers.
	Skipped []SkippedContext
	// Forwards owns every active port-forward this process opened. Lives
	// here (rather than on the TUI) so a forward keeps running when the
	// user drills into a different cluster — `↯ N fwd` in the topbar is
	// process-global, not view-local.
	Forwards *ForwardManager
}

func NewClient(rawCfg *clientcmdapi.Config, ctxName string) (*Client, error) {
	clientCfg := clientcmd.NewNonInteractiveClientConfig(
		*rawCfg,
		ctxName,
		&clientcmd.ConfigOverrides{},
		clientcmd.NewDefaultClientConfigLoadingRules(),
	)

	restCfg, err := clientCfg.ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to create rest config for %q: %w", ctxName, err)
	}

	restCfg.Timeout = 30 * time.Second
	// QPS/Burst sized for safina's parallel fan-out call patterns (cluster-
	// health tick, resource-browser per-namespace scan, connection graph).
	// Bench validated this directly: at the client-go default of 50/100, S5
	// (50 parallel List calls across 10 namespaces) sat at a flat 1000ms p95
	// — pure client-side rate-limit floor, identical across every API knob
	// (protobuf, field selector, fan-out cap, informer cache). Raising to
	// 200/400 dropped S5 p95 to ~250ms (-75%) and S3 to ~32ms (-69%) with
	// bytes-on-wire and call counts unchanged. Sits well under the apiserver
	// APF default flow limits, so safe on real clusters.
	restCfg.QPS = 200
	restCfg.Burst = 400

	// Identify safina (with its exact build) in apiserver audit logs and APF
	// metrics — every cluster this client touches is production, so operators
	// reviewing audit trails should see more than client-go's generic default.
	restCfg.UserAgent = "safina/" + version.Version + " " + rest.DefaultKubernetesUserAgent()

	// Wrap the transport so every API request is audited when SAFINA_DEBUG
	// is set. Compose with any existing WrapTransport (auth/exec plugins
	// like gke-gcloud-auth-plugin can set their own) so we don't clobber
	// the auth chain. The wrapper is a no-op when the debug sink is off.
	existing := restCfg.WrapTransport
	restCfg.WrapTransport = func(rt http.RoundTripper) http.RoundTripper {
		if existing != nil {
			rt = existing(rt)
		}
		return debug.WrapTransport(rt)
	}

	cs, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create clientset for %q: %w", ctxName, err)
	}

	streamCS, err := newStreamingClient(restCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create streaming client: %w", err)
	}

	disco, err := discovery.NewDiscoveryClientForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create discovery client for %q: %w", ctxName, err)
	}

	dyn, err := dynamic.NewForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create dynamic client for %q: %w", ctxName, err)
	}

	// Second clientset for the informer factory, negotiating protobuf.
	// Post-informerization the factory owns virtually all List+Watch bytes,
	// so this captures the protobuf win (bench: ~40-60% fewer bytes on the
	// wire for pod-heavy lists) while structurally confining it to built-in
	// types — dynamic/CRD content and the metrics DoRaw path stay JSON. The
	// JSON entry in AcceptContentTypes covers any aggregated apiserver that
	// can't serve protobuf. Best-effort: on error, informers reuse the JSON
	// clientset.
	protoCfg := rest.CopyConfig(restCfg)
	protoCfg.ContentType = "application/vnd.kubernetes.protobuf"
	protoCfg.AcceptContentTypes = "application/vnd.kubernetes.protobuf,application/json"
	// The 30s client timeout above is right for one-shot reads but FATAL for
	// informers: it severs every watch stream at 30s, forcing all kinds to
	// re-establish twice a minute — measured live as 15 kinds × 2 req/min of
	// pure churn. Reflectors set their own server-side per-watch timeout
	// (~5-10 min randomized), so the informer client runs without one.
	protoCfg.Timeout = 0
	protoCS, err := kubernetes.NewForConfig(protoCfg)
	if err != nil {
		debug.Errf("client.proto_clientset_failed", "context", ctxName, "err", err.Error())
		protoCS = nil
	}

	// Informers are NOT started here: construction happens for every
	// kubeconfig context at launch, and opening watch streams to N-1
	// production clusters the operator never enters this session would be
	// pure apiserver load. Manager.ActivateContext starts them when the
	// user actually drills into this context.
	return &Client{
		Clientset:       cs,
		StreamClientset: streamCS,
		RESTConfig:      restCfg,
		Discovery:       disco,
		Dynamic:         dyn,
		informerCS:      protoCS,
	}, nil
}

// Close releases the informer watch streams. Idempotent; safe on a Client
// whose informers were never started.
func (c *Client) Close() {
	c.StopInformers()
}

func NewManager(rawCfg *clientcmdapi.Config) (*Manager, error) {
	m := &Manager{
		Clients:  make(map[string]*Client),
		Forwards: NewForwardManager(),
	}

	// Iterate contexts in sorted order so the Skipped slice (and any banner
	// derived from it) is stable across launches — Go's map iteration is
	// randomised, which previously shuffled the "skipped X and N others"
	// banner's chosen example on every start.
	names := make([]string, 0, len(rawCfg.Contexts))
	for name := range rawCfg.Contexts {
		names = append(names, name)
	}
	sort.Strings(names)

	// Build clients in parallel. Construction is pure config assembly (no
	// network calls, no informer sync), but exec-auth plugins can still
	// block on I/O — parallelism keeps launch latency bounded by the
	// slowest single context rather than the sum.
	type result struct {
		name   string
		client *Client
		err    error
	}
	results := make([]result, len(names))
	var wg sync.WaitGroup
	for i, name := range names {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, err := NewClient(rawCfg, name)
			results[i] = result{name: name, client: c, err: err}
		}()
	}
	wg.Wait()

	for _, r := range results {
		if r.err != nil {
			m.Skipped = append(m.Skipped, SkippedContext{Name: r.name, Err: r.err})
			continue
		}
		m.Clients[r.name] = r.client
	}

	if len(m.Clients) == 0 {
		return nil, errors.New("no reachable contexts found")
	}

	return m, nil
}

// Close shuts down every per-cluster informer in the manager. Called from
// the TUI Quit path so informer watch streams don't outlive the process
// intent. Idempotent.
func (m *Manager) Close() {
	if m == nil {
		return
	}
	for _, c := range m.Clients {
		c.Close()
	}
}

// ActivateContext starts the informer set for the named context and stops
// every other context's set. Called when the user drills into a cluster —
// safina's contract is "watch streams open only to the cluster on screen".
// Idempotent and non-blocking (sync happens in the background), so calling
// it on every context-focus event is safe.
func (m *Manager) ActivateContext(name string) {
	if m == nil {
		return
	}
	for n, c := range m.Clients {
		if n == name {
			c.StartInformers()
		} else {
			c.StopInformers()
		}
	}
}

// DeactivateAll stops every context's informers. Called when the user pops
// back to the context picker — parked there, no cluster deserves open
// watches. Idempotent.
func (m *Manager) DeactivateAll() {
	if m == nil {
		return
	}
	for _, c := range m.Clients {
		c.StopInformers()
	}
}

func (m *Manager) GetClient(contextName string) (*Client, bool) {
	c, ok := m.Clients[contextName]
	return c, ok
}

func newStreamingClient(cfg *rest.Config) (kubernetes.Interface, error) {
	streamCfg := rest.CopyConfig(cfg)
	streamCfg.Timeout = 0
	return kubernetes.NewForConfig(streamCfg)
}
