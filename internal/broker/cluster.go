package broker

import (
	"fmt"
	"net/url"
	"os"

	"github.com/YunusAndreasson/safina/internal/config"
	"github.com/YunusAndreasson/safina/internal/history"
	inv "github.com/YunusAndreasson/safina/internal/investigation"
)

// Read configuration only: no auth plugins, API calls, or mutation clients.
func resolveCluster(path, alias string) (string, error) {
	raw, err := config.LoadKubeConfig(path)
	if err != nil {
		return "", err
	}
	selected, ok := raw.Contexts[alias]
	if !ok || selected == nil {
		return "", inv.ErrNotFound
	}
	cluster, ok := raw.Clusters[selected.Cluster]
	if !ok || cluster == nil {
		return "", inv.ErrNotFound
	}
	endpoint, err := url.Parse(cluster.Server)
	if err != nil || endpoint.Host == "" || (endpoint.Scheme != "https" && endpoint.Scheme != "http") || endpoint.User != nil {
		return "", fmt.Errorf("%w: unusable cluster endpoint", inv.ErrInvalid)
	}
	ca := cluster.CertificateAuthorityData
	if len(ca) == 0 && cluster.CertificateAuthority != "" {
		ca, err = os.ReadFile(cluster.CertificateAuthority)
		if err != nil {
			return "", err
		}
	}
	return clusterFingerprint(cluster.Server, ca), nil
}

func clusterFingerprint(server string, ca []byte) string { return history.ClusterID(server, ca) }
