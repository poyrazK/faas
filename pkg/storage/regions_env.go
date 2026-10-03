package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/region"
)

// RegionalStoreConfig contains connection metadata; OCI credentials are named
// environment variables rather than values in the routing file.
type RegionalStoreConfig struct {
	ID             string `json:"id"`
	Backend        string `json:"backend"`
	RegistryURL    string `json:"registry_url,omitempty"`
	RepoPrefix     string `json:"repo_prefix,omitempty"`
	UsernameEnv    string `json:"username_env,omitempty"`
	PasswordEnv    string `json:"password_env,omitempty"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
	Bucket         string `json:"bucket,omitempty"`
}

func regionalRoutesFromEnv(ctx context.Context) (map[string]StorageBackend, error) {
	path := os.Getenv("FAAS_STORAGE_REGIONS_FILE")
	if path == "" {
		return nil, nil
	}
	f, err := os.Open(path) //nolint:forbidigo // Operator-controlled routing path; provisioned configuration may be a symlink.
	if err != nil {
		return nil, fmt.Errorf("storage: regional routing file: %w", err)
	}
	defer func() { _ = f.Close() }()
	var config struct {
		Regions []RegionalStoreConfig `json:"regions"`
	}
	decoder := json.NewDecoder(f)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return nil, fmt.Errorf("storage: decode regional routing file: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("storage: regional routing file must contain one JSON object")
	}
	if len(config.Regions) == 0 {
		return nil, fmt.Errorf("storage: regional routing file has no regions")
	}
	compression, err := snapshotCompressionFromEnv()
	if err != nil {
		return nil, err
	}
	routes := make(map[string]StorageBackend, len(config.Regions))
	namespaces := make(map[string]bool, len(config.Regions))
	for _, entry := range config.Regions {
		if entry.ID == "" || !region.Valid(entry.ID) || routes[entry.ID] != nil {
			return nil, fmt.Errorf("storage: invalid or duplicate regional route %q", entry.ID)
		}
		backend, namespace, err := regionalStoreFromConfig(ctx, entry, compression)
		if err != nil {
			return nil, fmt.Errorf("storage: configure region %q: %w", entry.ID, err)
		}
		if namespaces[namespace] {
			return nil, fmt.Errorf("storage: regional routes must use distinct storage namespaces")
		}
		namespaces[namespace] = true
		routes[entry.ID] = backend
	}
	return routes, nil
}

func regionalStoreFromConfig(ctx context.Context, config RegionalStoreConfig, compression string) (StorageBackend, string, error) {
	switch config.Backend {
	case "oci":
		if config.RegistryURL == "" || config.Bucket != "" || config.TimeoutSeconds < 0 {
			return nil, "", fmt.Errorf("OCI route requires registry_url, no bucket, and nonnegative timeout_seconds")
		}
		username, err := regionalCredential(config.UsernameEnv)
		if err != nil {
			return nil, "", err
		}
		password, err := regionalCredential(config.PasswordEnv)
		if err != nil {
			return nil, "", err
		}
		if (config.UsernameEnv == "") != (config.PasswordEnv == "") {
			return nil, "", fmt.Errorf("OCI credential references must be provided together")
		}
		prefix := config.RepoPrefix
		if prefix == "" {
			prefix = "faas"
		}
		if err := validateKey(prefix); err != nil {
			return nil, "", fmt.Errorf("invalid regional OCI repository prefix: %w", err)
		}
		endpoint, err := url.Parse(config.RegistryURL)
		if err != nil || (endpoint.Scheme != "https" && endpoint.Scheme != "http") || endpoint.Host == "" ||
			endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" || strings.Trim(endpoint.Path, "/") != "" {
			return nil, "", fmt.Errorf("regional registry_url must be an HTTP(S) origin without credentials, query, or path")
		}
		endpoint.Host = strings.ToLower(endpoint.Host)
		endpoint.Path = ""
		timeout := config.TimeoutSeconds
		if timeout == 0 {
			timeout = 60
		}
		backend, err := NewOCIRegistryStorageBackend(
			WithRegistry(endpoint.String()), WithRepoPrefix(prefix),
			WithCredentials(username, password), WithTimeout(time.Duration(timeout)*time.Second),
			WithSnapshotCompression(compression),
		)
		return backend, "oci:" + endpoint.String() + "/" + prefix, err
	case "gcs":
		if config.Bucket == "" || config.RegistryURL != "" || config.RepoPrefix != "" ||
			config.UsernameEnv != "" || config.PasswordEnv != "" || config.TimeoutSeconds != 0 {
			return nil, "", fmt.Errorf("GCS route requires only bucket and ADC authentication")
		}
		backend, err := NewGCSStorageBackend(ctx, config.Bucket, compression)
		return backend, "gcs:" + config.Bucket, err
	default:
		return nil, "", fmt.Errorf("regional backend must be oci or gcs")
	}
}

func regionalCredential(name string) (string, error) {
	if name == "" {
		return "", nil
	}
	for i, c := range name {
		if (c < 'A' || c > 'Z') && c != '_' && (i == 0 || c < '0' || c > '9') {
			return "", fmt.Errorf("invalid regional credential environment variable name")
		}
	}
	value, ok := os.LookupEnv(name)
	if !ok || value == "" {
		return "", fmt.Errorf("regional credential environment variable %s is unset or empty", name)
	}
	return value, nil
}
