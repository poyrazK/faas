package managedpostgres

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
)

// Config contains only non-secret placement settings and names of environment
// variables containing secrets. Backend IDs are immutable and must remain in
// config for as long as a database references them.
type Config struct {
	DefaultRegion          string            `json:"default_region"`
	Defaults               map[string]string `json:"defaults"`
	MaxDatabasesPerAccount int               `json:"max_databases_per_account"`
	Backends               []BackendConfig   `json:"backends"`
}

type BackendConfig struct {
	ID        string            `json:"id"`
	Driver    string            `json:"driver"`
	Region    string            `json:"region"`
	Namespace string            `json:"namespace"`
	Settings  map[string]string `json:"settings,omitempty"`
	SecretEnv map[string]string `json:"secret_env,omitempty"`
}

type Backend struct {
	ID           string
	Driver       string
	Region       string
	Fingerprint  string
	Capabilities Capabilities
	Provider     Provider
}

type Factory func(BackendConfig, func(string) string) (Provider, error)

type Registry struct {
	DefaultRegion          string
	MaxDatabasesPerAccount int
	backends               map[string]Backend
	defaults               map[string]string
}

func NewRegistry(config Config, getenv func(string) string, factories map[string]Factory) (*Registry, error) {
	if config.MaxDatabasesPerAccount == 0 {
		config.MaxDatabasesPerAccount = 3
	}
	if config.MaxDatabasesPerAccount < 1 || config.MaxDatabasesPerAccount > 100 {
		return nil, errors.New("managed postgres: invalid per-account database limit")
	}
	registry := &Registry{
		DefaultRegion:          config.DefaultRegion,
		MaxDatabasesPerAccount: config.MaxDatabasesPerAccount,
		backends:               make(map[string]Backend, len(config.Backends)),
		defaults:               make(map[string]string, len(config.Defaults)),
	}
	validID := regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
	validEnv := regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	for _, backendConfig := range config.Backends {
		if !validID.MatchString(backendConfig.ID) || !validID.MatchString(backendConfig.Driver) || !validID.MatchString(backendConfig.Region) || backendConfig.Namespace == "" || len(backendConfig.Namespace) > 255 {
			return nil, errors.New("managed postgres: invalid backend identity")
		}
		if _, exists := registry.backends[backendConfig.ID]; exists {
			return nil, errors.New("managed postgres: duplicate backend ID")
		}
		for key, envName := range backendConfig.SecretEnv {
			if !validID.MatchString(key) || !validEnv.MatchString(envName) {
				return nil, errors.New("managed postgres: invalid secret environment mapping")
			}
		}
		factory, ok := factories[backendConfig.Driver]
		if !ok {
			return nil, fmt.Errorf("managed postgres: unknown driver %s", backendConfig.Driver)
		}
		provider, err := factory(backendConfig, getenv)
		if err != nil {
			return nil, fmt.Errorf("managed postgres: backend %s configuration failed: %w", backendConfig.ID, err)
		}
		capabilities := provider.Capabilities()
		if err := capabilities.Validate(); err != nil {
			return nil, fmt.Errorf("managed postgres: backend %s has invalid capabilities", backendConfig.ID)
		}
		registry.backends[backendConfig.ID] = Backend{
			ID:           backendConfig.ID,
			Driver:       backendConfig.Driver,
			Region:       backendConfig.Region,
			Fingerprint:  fingerprint(backendConfig),
			Capabilities: capabilities,
			Provider:     provider,
		}
	}
	for region, backendID := range config.Defaults {
		backend, ok := registry.backends[backendID]
		if !ok || backend.Region != region {
			return nil, errors.New("managed postgres: default references missing backend or mismatched region")
		}
		registry.defaults[region] = backendID
	}
	if _, ok := registry.defaults[registry.DefaultRegion]; !ok {
		return nil, errors.New("managed postgres: default_region is not configured")
	}
	return registry, nil
}

func Load(getenv func(string) string, factories map[string]Factory) (*Registry, error) {
	path := getenv("FAAS_MANAGED_POSTGRES_CONFIG")
	if path == "" {
		return nil, nil
	}
	// The path is operator-controlled daemon configuration, not a customer
	// path. Size and JSON shape are bounded immediately below.
	file, err := os.Open(path) //nolint:forbidigo
	if err != nil {
		return nil, errors.New("managed postgres: cannot open configuration")
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || info.Size() > 1<<20 {
		return nil, errors.New("managed postgres: configuration exceeds size limit or cannot be read")
	}
	decoder := json.NewDecoder(io.LimitReader(file, 1<<20))
	decoder.DisallowUnknownFields()
	var config Config
	if err := decoder.Decode(&config); err != nil {
		return nil, errors.New("managed postgres: invalid configuration JSON")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return nil, errors.New("managed postgres: trailing configuration data")
	}
	return NewRegistry(config, getenv, factories)
}

func (r *Registry) Default(region string) (Backend, error) {
	if r == nil {
		return Backend{}, ErrUnavailable
	}
	backend, ok := r.backends[r.defaults[region]]
	if !ok {
		return Backend{}, ErrUnsupported
	}
	return backend, nil
}

func (r *Registry) Resolve(id, placementFingerprint string) (Backend, error) {
	if r == nil {
		return Backend{}, ErrUnavailable
	}
	backend, ok := r.backends[id]
	if !ok || backend.Fingerprint != placementFingerprint {
		return Backend{}, ErrUnavailable
	}
	return backend, nil
}

func (r *Registry) Regions() []string {
	regions := make([]string, 0, len(r.defaults))
	for region := range r.defaults {
		regions = append(regions, region)
	}
	sort.Strings(regions)
	return regions
}

func fingerprint(config BackendConfig) string {
	// SecretEnv is intentionally excluded: credential rotation or renaming an
	// environment variable must not change placement. Namespace and all
	// non-secret settings fail closed if a backend is accidentally repurposed.
	payload := struct {
		Driver    string            `json:"driver"`
		Region    string            `json:"region"`
		Namespace string            `json:"namespace"`
		Settings  map[string]string `json:"settings"`
	}{config.Driver, config.Region, config.Namespace, config.Settings}
	encoded, _ := json.Marshal(payload)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}
