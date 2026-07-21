package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// BackendFromEnv is the shared seaming point for imaged / vmmd / any
// future daemon: read FAAS_STORAGE_BACKEND (default "local"), pick the
// matching driver, and configure it from env. Centralising the
// branching here keeps the env-var contract in one place — daemons
// only have to decide which "default root" to pass for the local
// case; the OCI case has its own required variables.
//
// Returned errors are stable so cmd/{imaged,vmmd}/main.go can wrap
// them with %w and surface a single ops-friendly message at startup.
//
// Env contract:
//
//	FAAS_STORAGE_BACKEND      "local" (default) | "oci"
//	FAAS_STORAGE_ROOT         local-only — root dir (e.g. /srv/fc)
//	FAAS_APPS_ROOT            local-only — apps prefix (may equal ROOT)
//	FAAS_OCI_REGISTRY         oci-only — full URL incl. scheme (e.g. https://ghcr.io/org)
//	FAAS_OCI_REPO_PREFIX      oci-only — repo namespace (default "faas")
//	FAAS_OCI_USERNAME         oci-only — optional Basic-Auth user for token endpoint
//	FAAS_OCI_PASSWORD         oci-only — optional Basic-Auth password
//	FAAS_OCI_TIMEOUT_SECONDS  oci-only — per-request timeout (default 60)
//
// The "apps-root can differ from fc-root" composition only makes sense
// for the local backend (an OCI backend namespaces all prefixes under
// one registry). When FAAS_STORAGE_BACKEND=oci we ignore
// FAAS_APPS_ROOT.
func BackendFromEnv() (StorageBackend, error) {
	kind := envOr("FAAS_STORAGE_BACKEND", "local")
	switch kind {
	case "local":
		return localBackendFromEnv()
	case "oci":
		return ociBackendFromEnv()
	default:
		return nil, fmt.Errorf("storage: unknown FAAS_STORAGE_BACKEND=%q (want \"local\" or \"oci\")", kind)
	}
}

// localBackendFromEnv builds a PrefixRouter over FAAS_STORAGE_ROOT +
// (optional) FAAS_APPS_ROOT, collapsing to a single backend when the
// two roots coincide.
func localBackendFromEnv() (StorageBackend, error) {
	storageRoot := envOr("FAAS_STORAGE_ROOT", "/srv/fc")
	appsRoot := envOr("FAAS_APPS_ROOT", "/var/lib/faas/apps")
	fcBackend, err := NewLocalStorageBackend(storageRoot)
	if err != nil {
		return nil, fmt.Errorf("storage: FAAS_STORAGE_ROOT=%q: %w", storageRoot, err)
	}
	if filepath.Clean(appsRoot) == filepath.Clean(storageRoot) {
		return fcBackend, nil
	}
	appsBackend, err := NewLocalStorageBackend(appsRoot)
	if err != nil {
		return nil, fmt.Errorf("storage: FAAS_APPS_ROOT=%q: %w", appsRoot, err)
	}
	router, err := NewPrefixRouter(map[string]StorageBackend{
		"apps/": appsBackend,
	}, fcBackend)
	if err != nil {
		return nil, fmt.Errorf("storage: prefix router: %w", err)
	}
	return router, nil
}

// ociBackendFromEnv wires the OCIRegistryStorageBackend. The driver is
// the only artifact-manipulation backend when FAAS_STORAGE_BACKEND=oci
// — there is no local fallback because the OCI driver handles all
// prefixes (apps/, snap/, base/, layers/, kernel/) by namespacing
// under FAAS_OCI_REPO_PREFIX.
func ociBackendFromEnv() (StorageBackend, error) {
	registry := os.Getenv("FAAS_OCI_REGISTRY")
	if registry == "" {
		return nil, fmt.Errorf("storage: FAAS_STORAGE_BACKEND=oci requires FAAS_OCI_REGISTRY (e.g. https://ghcr.io/onebox-faas)")
	}
	opts := []Option{
		WithRegistry(registry),
		WithCredentials(os.Getenv("FAAS_OCI_USERNAME"), os.Getenv("FAAS_OCI_PASSWORD")),
	}
	if p := os.Getenv("FAAS_OCI_REPO_PREFIX"); p != "" {
		opts = append(opts, WithRepoPrefix(p))
	}
	if v := os.Getenv("FAAS_OCI_TIMEOUT_SECONDS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("storage: FAAS_OCI_TIMEOUT_SECONDS=%q: must be a positive integer", v)
		}
		opts = append(opts, WithTimeout(time.Duration(n)*time.Second))
	}
	be, err := NewOCIRegistryStorageBackend(opts...)
	if err != nil {
		return nil, fmt.Errorf("storage: oci backend: %w", err)
	}
	return be, nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
