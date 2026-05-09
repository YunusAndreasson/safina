package config

import (
	"fmt"
	"os"
	"path/filepath"

	"sigs.k8s.io/yaml"
)

type AppConfig struct {
	LastCluster   string `json:"lastCluster,omitempty"`
	LastNamespace string `json:"lastNamespace,omitempty"`

	// UserNamespaces / SystemNamespaces override the built-in namespace
	// classifier (which uses prefix-matching against well-known control-plane
	// namespaces). When non-empty, UserNamespaces is treated as the exact
	// allowlist of "user" namespaces; everything else is "system".
	// SystemNamespaces takes precedence over the prefix list when set.
	UserNamespaces   []string `json:"userNamespaces,omitempty"`
	SystemNamespaces []string `json:"systemNamespaces,omitempty"`
}

func ConfigDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".safina"), nil
}

func ConfigPath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.yaml"), nil
}

func Load() (*AppConfig, error) {
	cfg := &AppConfig{}
	path, err := ConfigPath()
	if err != nil {
		return cfg, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, nil
	}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return cfg, nil
	}
	return cfg, nil
}

func Save(cfg *AppConfig) error {
	if cfg == nil {
		return fmt.Errorf("config is nil")
	}
	dir, err := ConfigDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	path, err := ConfigPath()
	if err != nil {
		return err
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}
