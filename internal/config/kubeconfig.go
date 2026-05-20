package config

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

type ContextInfo struct {
	Name    string
	Cluster string
	User    string
}

func resolveKubeconfigPath(explicitPath string) string {
	if explicitPath != "" {
		return explicitPath
	}
	if env := os.Getenv("KUBECONFIG"); env != "" {
		return env
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".kube", "config")
}

func LoadKubeConfig(explicitPath string) (*clientcmdapi.Config, error) {
	path := resolveKubeconfigPath(explicitPath)
	if path == "" {
		return nil, errors.New("no kubeconfig path resolved")
	}

	paths := strings.Split(path, string(os.PathListSeparator))
	merged := clientcmdapi.NewConfig()

	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if _, err := os.Stat(p); os.IsNotExist(err) {
			continue
		}
		cfg, err := clientcmd.LoadFromFile(p)
		if err != nil {
			continue
		}
		for k, v := range cfg.Clusters {
			merged.Clusters[k] = v
		}
		for k, v := range cfg.AuthInfos {
			merged.AuthInfos[k] = v
		}
		for k, v := range cfg.Contexts {
			merged.Contexts[k] = v
		}
		if cfg.CurrentContext != "" {
			merged.CurrentContext = cfg.CurrentContext
		}
	}

	if len(merged.Contexts) == 0 {
		return nil, errors.New("no contexts found in kubeconfig")
	}

	return merged, nil
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
