package config

import (
	"errors"
	"sort"

	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

type ContextInfo struct {
	Name    string
	Cluster string
	User    string
}

// LoadKubeConfig loads and merges kubeconfig files via kubectl's canonical
// loading rules: an explicit --kubeconfig path wins outright; otherwise each
// entry in $KUBECONFIG (list-separated) is merged with first-file-wins
// semantics per key, falling back to ~/.kube/config. Matching kubectl here
// means safina always sees exactly the config kubectl would use — including
// which file's current-context wins.
func LoadKubeConfig(explicitPath string) (*clientcmdapi.Config, error) {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	rules.ExplicitPath = explicitPath

	cfg, err := rules.Load()
	if err != nil {
		return nil, err
	}
	if len(cfg.Contexts) == 0 {
		return nil, errors.New("no contexts found in kubeconfig")
	}
	return cfg, nil
}

func ListContexts(raw *clientcmdapi.Config) []ContextInfo {
	if raw == nil {
		return nil
	}
	contexts := make([]ContextInfo, 0, len(raw.Contexts))
	for name, ctx := range raw.Contexts {
		contexts = append(contexts, ContextInfo{
			Name:    name,
			Cluster: ctx.Cluster,
			User:    ctx.AuthInfo,
		})
	}
	// Sort by name so the picker is stable across launches — Go's map
	// iteration is randomised and would otherwise reshuffle the cluster
	// list on every start, breaking the user's muscle memory for
	// "second from the top is staging".
	sort.Slice(contexts, func(i, j int) bool {
		return contexts[i].Name < contexts[j].Name
	})
	return contexts
}
