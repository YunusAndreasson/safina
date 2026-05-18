package k8s

import (
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"

	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"github.com/yunus/safina/internal/debug"
)

type Client struct {
	Clientset  kubernetes.Interface
	RESTConfig *rest.Config
	// Discovery enumerates the GVRs the API server exposes — used by the
	// Resource Browser to populate "every kind in this cluster, including
	// CRDs". Reads through the same wrapped transport so debug auditing
	// still applies.
	Discovery discovery.DiscoveryInterface
	// Dynamic lists / watches arbitrary GVRs without compile-time types.
	// Used for any resource the tree doesn't have a first-class typed
	// renderer for (CRDs, anything the user enabled beyond the defaults).
	Dynamic dynamic.Interface
	// informers backs the cache-aware Client methods (HotPods, ClusterHealth).
	// nil when initial sync failed (e.g. RBAC denies Watch); methods detect
	// this and fall back to direct API reads.
	informers *informerSet
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

	disco, err := discovery.NewDiscoveryClientForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create discovery client for %q: %w", ctxName, err)
	}

	dyn, err := dynamic.NewForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create dynamic client for %q: %w", ctxName, err)
	}

	// Eagerly start the pod + node informers and block until initial sync.
	// On a typical cluster this is ~100-500ms; NewManager parallelises across
	// contexts so total launch cost is bounded by the slowest cluster, not
	// the sum. Failure (timeout or RBAC-denied Watch) leaves informers=nil,
	// and the cache-aware methods fall back to direct API reads.
	inf := startInformers(cs, 5*time.Second)

	return &Client{
		Clientset:  cs,
		RESTConfig: restCfg,
		Discovery:  disco,
		Dynamic:    dyn,
		informers:  inf,
	}, nil
}

// Close releases the informer watch streams. Idempotent; safe on a Client
// whose informer sync failed (informers will be nil).
func (c *Client) Close() {
	if c == nil {
		return
	}
	c.informers.close()
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

	// Build clients in parallel. Each NewClient blocks on informer sync
	// (~100-500ms per cluster); doing them serially across N contexts would
	// stack the wait, but in parallel total launch latency collapses to the
	// slowest single cluster.
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
		return nil, fmt.Errorf("no reachable contexts found")
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

func (m *Manager) GetClient(contextName string) (*Client, bool) {
	c, ok := m.Clients[contextName]
	return c, ok
}
