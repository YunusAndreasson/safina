package k8s

import (
	"encoding/json"
	"os"
	"slices"

	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/YunusAndreasson/safina/internal/debug"
)

// A kubeconfig changes while safina runs: a fresh login rewrites a token or
// certificate, get-credentials adds a context. Clients used to be built once
// at launch, so a cluster kept its old credentials until safina restarted.
// The kubeconfig is now re-read when a cluster is opened or retried, and only
// a client whose entries changed is rebuilt.

// SetKubeconfigLoader gives the manager the way to re-read the kubeconfig its
// clients were built from. Without one, Reload does nothing.
func (m *Manager) SetKubeconfigLoader(load func() (*clientcmdapi.Config, error)) {
	m.load = load
}

// SetNamespace scopes every client to namespace (the --namespace flag),
// including the ones Reload builds later.
func (m *Manager) SetNamespace(namespace string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.namespace = namespace
	for _, c := range m.Clients {
		c.SetNamespace(namespace)
	}
}

// Reload re-reads the kubeconfig and rebuilds the named context's client if
// its entries changed since that client was built, keeping its informers
// running if they were. An empty name only reads. It returns the kubeconfig
// as read, for the caller's context list, or nil without a loader.
func (m *Manager) Reload(name string) (*clientcmdapi.Config, error) {
	if m == nil || m.load == nil {
		return nil, nil
	}
	raw, err := m.load()
	if err != nil {
		return nil, err
	}
	entries := kubeconfigEntries(raw, name)
	if entries == "" {
		return raw, nil
	}
	m.mu.RLock()
	old := m.Clients[name]
	unchanged := old != nil && m.built[name] == entries
	m.mu.RUnlock()
	if unchanged {
		return raw, nil
	}
	c, err := NewClient(raw, name)
	if err != nil {
		return raw, err
	}
	m.mu.Lock()
	if m.namespace != "" {
		c.SetNamespace(m.namespace)
	}
	if m.Clients == nil {
		m.Clients = make(map[string]*Client)
	}
	if m.built == nil {
		m.built = make(map[string]string)
	}
	m.Clients[name] = c
	m.built[name] = entries
	m.Skipped = slices.DeleteFunc(m.Skipped, func(s SkippedContext) bool { return s.Name == name })
	m.mu.Unlock()
	if old != nil {
		watching := old.Informers() != nil
		old.Close()
		if watching {
			c.StartInformers()
		}
	}
	debug.Log("kubeconfig.reloaded", "context", name)
	return raw, nil
}

// kubeconfigEntries is what a context's client is built from: its context,
// cluster and user entries, and when each file they name last changed, since
// a renewed certificate is often rewritten in place. Empty when the
// kubeconfig has no such context.
func kubeconfigEntries(raw *clientcmdapi.Config, name string) string {
	if raw == nil || raw.Contexts[name] == nil {
		return ""
	}
	kc := raw.Contexts[name]
	cluster, user := raw.Clusters[kc.Cluster], raw.AuthInfos[kc.AuthInfo]
	var files []string
	if cluster != nil {
		files = append(files, cluster.CertificateAuthority)
	}
	if user != nil {
		files = append(files, user.ClientCertificate, user.ClientKey, user.TokenFile)
	}
	stamps := make([]string, 0, len(files))
	for _, f := range files {
		if st, err := os.Stat(f); f != "" && err == nil {
			stamps = append(stamps, f+"@"+st.ModTime().String())
		}
	}
	b, err := json.Marshal(struct {
		Context *clientcmdapi.Context  `json:"context"`
		Cluster *clientcmdapi.Cluster  `json:"cluster"`
		User    *clientcmdapi.AuthInfo `json:"user"`
		Files   []string               `json:"files"`
	}{kc, cluster, user, stamps})
	if err != nil {
		return "unencodable: " + err.Error()
	}
	return string(b)
}
