package config

import (
	"errors"
	"os"
	"path/filepath"

	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/YunusAndreasson/safina/internal/history"
	"sigs.k8s.io/yaml"
)

type AppConfig struct {
	History       history.Options `json:"history,omitempty"`
	LastCluster   string          `json:"lastCluster,omitempty"`
	LastNamespace string          `json:"lastNamespace,omitempty"`

	// UserNamespaces / SystemNamespaces override the built-in namespace
	// classifier (which uses prefix-matching against well-known control-plane
	// namespaces). When non-empty, UserNamespaces is treated as the exact
	// allowlist of "user" namespaces; everything else is "system".
	// SystemNamespaces takes precedence over the prefix list when set.
	UserNamespaces   []string `json:"userNamespaces,omitempty"`
	SystemNamespaces []string `json:"systemNamespaces,omitempty"`

	// EnabledResources is the source of truth for which resource kinds the
	// tree renders as folds at the namespace/All level. Order is preserved
	// — the tree renders folds in the order they appear here. nil (absent
	// from yaml) means "fresh install" and the loader seeds the default
	// set so existing users see no change. An explicit empty slice is
	// respected: the user has opted out of every fold.
	EnabledResources []ResourceRef `json:"enabledResources"`
}

// ResourceRef identifies one Kubernetes resource by GVR. Group is empty for
// core ("" maps to the core API group in client-go). Resource is the
// lowercase plural name as the API server reports it (e.g. "configmaps",
// "ingresses"). The Synthetic field is non-empty for the special
// "Workloads" fold which unions five workload GVRs into one row.
type ResourceRef struct {
	Group     string `json:"group,omitempty"`
	Version   string `json:"version,omitempty"`
	Resource  string `json:"resource,omitempty"`
	Synthetic string `json:"synthetic,omitempty"`
}

// DefaultEnabledResources returns the seed list used when the user has not
// configured EnabledResources. Existing explicit pin lists are preserved.
func DefaultEnabledResources() []ResourceRef {
	return []ResourceRef{
		{Synthetic: "Workloads"},
		{Version: "v1", Resource: "pods"},
		{Group: "", Version: "v1", Resource: "configmaps"},
		{Group: "", Version: "v1", Resource: "secrets"},
		{Group: "", Version: "v1", Resource: "services"},
		{Group: "networking.k8s.io", Version: "v1", Resource: "ingresses"},
		{Group: "", Version: "v1", Resource: "persistentvolumeclaims"},
	}
}

// Equal reports whether two ResourceRefs identify the same resource.
func (r ResourceRef) Equal(o ResourceRef) bool {
	return r.GVR() == o.GVR() && r.Synthetic == o.Synthetic
}

// GVR converts the persisted pin fields to the SDK's API resource identity.
func (r ResourceRef) GVR() schema.GroupVersionResource {
	return schema.GroupVersion{Group: r.Group, Version: r.Version}.WithResource(r.Resource)
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
		seedDefaults(cfg)
		return cfg, nil //nolint:nilerr // return empty config when home dir unavailable
	}
	return loadFile(path)
}

func loadFile(path string) (*AppConfig, error) {
	cfg := &AppConfig{}
	data, err := os.ReadFile(path) // #nosec G304 -- Reads the application config path resolved from the current user home directory.
	if err != nil {
		seedDefaults(cfg)
		return cfg, nil //nolint:nilerr // return empty config when file missing
	}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		seedDefaults(cfg)
		return cfg, nil //nolint:nilerr // return empty config when YAML corrupt
	}
	seedDefaults(cfg)
	return cfg, nil
}

// seedDefaults populates fields that the user has not yet customised. Only
// EnabledResources is seeded today; we treat a nil slice as "fresh install,
// the user has never picked" and inject the historical default set. An
// explicit empty slice means "the user picked nothing" and is left intact.
func seedDefaults(cfg *AppConfig) {
	if cfg.EnabledResources == nil {
		cfg.EnabledResources = DefaultEnabledResources()
	}
}

func Save(cfg *AppConfig) error {
	if cfg == nil {
		return errors.New("config is nil")
	}
	path, err := ConfigPath()
	if err != nil {
		return err
	}
	return saveFile(cfg, path)
}

func saveFile(cfg *AppConfig, path string) error {
	if cfg == nil {
		return errors.New("config is nil")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	// Write beside the destination so rename atomically replaces it. Readers
	// see either the old config or the complete new one, never a partial file.
	f, err := os.CreateTemp(filepath.Dir(path), ".config-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
