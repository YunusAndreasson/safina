package k8s

import (
	"fmt"
	"net/http"
	"sort"
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
	restCfg.QPS = 50
	restCfg.Burst = 100

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

	return &Client{
		Clientset:  cs,
		RESTConfig: restCfg,
		Discovery:  disco,
		Dynamic:    dyn,
	}, nil
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

	for _, name := range names {
		client, err := NewClient(rawCfg, name)
		if err != nil {
			m.Skipped = append(m.Skipped, SkippedContext{Name: name, Err: err})
			continue
		}
		m.Clients[name] = client
	}

	if len(m.Clients) == 0 {
		return nil, fmt.Errorf("no reachable contexts found")
	}

	return m, nil
}

func (m *Manager) GetClient(contextName string) (*Client, bool) {
	c, ok := m.Clients[contextName]
	return c, ok
}
