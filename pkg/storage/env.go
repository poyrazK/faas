package storage

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// BackendFromEnv is the shared seaming point for imaged / vmmd / any
// future daemon: read FAAS_STORAGE_BACKEND (default "local"), pick the
// matching driver, and configure it from env. Centralising the
// branching here keeps the env-var contract in one place — daemons
// only have to decide which "default root" to pass for the local
// case; the OCI case has its own required variables.
//
// If FAAS_STORAGE_CACHE_DIR is set, the resulting backend is wrapped
// in a LocalCacheBackend (read-through LRU on disk). The cache is the
// load-bearing piece that lets a registry outage degrade gracefully —
// without it, every cold boot on every compute node depends on a
// healthy registry. ADR-054 §2. For oci mode (multi-box), the cache
// defaults to on at /var/lib/faas/cache when the env var is unset
// (see wrapWithCache for the exact contract).
//
// Returned errors are stable so cmd/{imaged,vmmd}/main.go can wrap
// them with %w and surface a single ops-friendly message at startup.
//
// Env contract:
//
//	FAAS_STORAGE_BACKEND          "local" (default) | "oci"
//	FAAS_STORAGE_ROOT             local-only — root dir (e.g. /srv/fc)
//	FAAS_APPS_ROOT                local-only — apps prefix (may equal ROOT)
//	FAAS_STORAGE_LOCAL_PREFIXES   oci-only — comma-separated prefix list
//	                                routed to the local backend (default
//	                                "snap/,base/,kernel/,layers/"). The
//	                                literal "none" disables all local
//	                                routes; this is required with
//	                                FAAS_REQUIRE_SHARED_ARTIFACTS=1.
//	                                local-only backend IGNORES this — every
//	                                key falls through to FAAS_STORAGE_ROOT
//	                                with the full prefix preserved (the
//	                                prefix list exists to tell the OCI
//	                                backend what NOT to ship to the
//	                                registry; in a local-only deployment
//	                                there's nothing to keep apart, and
//	                                honouring it as routes strips prefixes
//	                                and crashes imaged — see env.go:155
//	                                and the 2026-07-31 incident).
//	FAAS_STORAGE_CACHE_DIR        local+oci — optional. When set, wrap
//	                                the resulting backend in a read-through
//	                                LocalCacheBackend rooted at this dir.
//	                                Solves the "registry outage → cold boot
//	                                fails" gap (ADR-054 §2). On oci mode
//	                                (FAAS_STORAGE_BACKEND=oci) the cache
//	                                defaults to on at /var/lib/faas/cache
//	                                when the env var is unset; set to ""
//	                                to explicitly disable.
//	FAAS_STORAGE_CACHE_MAX_BYTES  local+oci — optional cache byte budget
//	                                (default 1 GiB).
//	FAAS_OCI_REGISTRY             oci-only — full URL incl. scheme (e.g. https://ghcr.io/org)
//	FAAS_OCI_REPO_PREFIX          oci-only — repo namespace (default "faas")
//	FAAS_OCI_USERNAME             oci-only — optional Basic-Auth user for token endpoint
//	FAAS_OCI_PASSWORD             oci-only — optional Basic-Auth password
//	FAAS_OCI_TIMEOUT_SECONDS      oci-only — per-request timeout (default 60)
//	FAAS_REQUIRE_SHARED_ARTIFACTS  when "1"/"true", require the OCI backend
//	                                with FAAS_STORAGE_LOCAL_PREFIXES=none.
//	                                This is the production split-node gate:
//	                                snapshots, bases, kernels, and layers
//	                                must resolve from one shared store.
//
// The "apps-root can differ from fc-root" composition only makes sense
// for the local backend (an OCI backend namespaces all prefixes under
// one registry). When FAAS_STORAGE_BACKEND=oci we ignore
// FAAS_APPS_ROOT but still honor FAAS_STORAGE_LOCAL_PREFIXES so a
// compute node can keep canonical content-addressed blobs on local
// disk while routing per-app layers to the registry. ADR-054.
func BackendFromEnv() (StorageBackend, error) {
	kind := envOr("FAAS_STORAGE_BACKEND", "local")
	if err := validateSharedArtifactMode(kind); err != nil {
		return nil, err
	}
	var be StorageBackend
	var err error
	switch kind {
	case "local":
		be, err = localBackendFromEnv()
	case "oci":
		be, err = ociBackendFromEnv()
	default:
		return nil, fmt.Errorf("storage: unknown FAAS_STORAGE_BACKEND=%q (want \"local\" or \"oci\")", kind)
	}
	if err != nil {
		return nil, err
	}
	return wrapWithCache(be, kind)
}

// DefaultOCICacheDir is the canonical cache directory when
// FAAS_STORAGE_BACKEND=oci and FAAS_STORAGE_CACHE_DIR is unset. ADR-054
// §2 + the acceptance amendment pin this default for multi-box fleets.
// Exported so tests and operator tooling can assert the contract.
const DefaultOCICacheDir = "/var/lib/faas/cache"

// resolveCacheDir applies the cache-dir env contract without performing
// any I/O. Pure function — no MkdirAll, no parent construction. Lets
// unit tests assert the resolution matrix hermetically (without creating
// /var/lib/faas/cache on the dev machine).
//
//   - FAAS_STORAGE_CACHE_DIR set to a non-empty path → ("path", true).
//   - FAAS_STORAGE_CACHE_DIR unset AND kind=="oci" →
//     (DefaultOCICacheDir, true). Multi-box default-on.
//   - FAAS_STORAGE_CACHE_DIR unset AND kind!="oci" → ("", false). Single-
//     box opt-in.
//   - FAAS_STORAGE_CACHE_DIR explicitly empty ("") → ("", false). The
//     os.LookupEnv distinction is load-bearing: unset vs explicit empty.
//
// Returns the dir and a bool indicating whether the caller should wrap.
func resolveCacheDir(kind string) (string, bool) {
	dir, isSet := os.LookupEnv("FAAS_STORAGE_CACHE_DIR")
	if isSet {
		if dir == "" {
			return "", false
		}
		return dir, true
	}
	if kind == "oci" {
		return DefaultOCICacheDir, true
	}
	return "", false
}

// wrapWithCache wraps parent in a LocalCacheBackend according to the
// cache env contract. See resolveCacheDir for the resolution matrix.
//
// Returns the parent unchanged when no cache is wanted. A misconfigured
// cache dir blocks startup — better to fail loud than silently disable
// the multi-box safety net.
func wrapWithCache(parent StorageBackend, kind string) (StorageBackend, error) {
	dir, ok := resolveCacheDir(kind)
	if !ok {
		return parent, nil
	}
	maxBytes := DefaultCacheMaxBytes
	if raw := os.Getenv("FAAS_STORAGE_CACHE_MAX_BYTES"); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("storage: FAAS_STORAGE_CACHE_MAX_BYTES=%q: must be a positive integer", raw)
		}
		maxBytes = n
	}
	cache, err := NewLocalCacheBackend(parent, dir, maxBytes)
	if err != nil {
		return nil, fmt.Errorf("storage: cache backend: %w", err)
	}
	return cache, nil
}

// defaultLocalPrefixes is the canonical local-prefix set for OCI mode
// (ADR-054 §1). Snapshot blobs are intentionally absent: issue #1054's
// node-local fan-out worker needs every compute box to pull the same
// canonical snapshot from the shared OCI backend. The read-through cache
// still keeps the blob local after the first pull, so a prepositioned wake
// does not pay a registry round trip. Operators can opt back into legacy
// local snapshots with FAAS_STORAGE_LOCAL_PREFIXES=snap/,... during a
// staged migration, but that disables cross-box snapshot availability.
var defaultLocalPrefixes = []string{
	"base/", "kernel/", "layers/", "scans/",
}

// parseLocalPrefixes splits a FAAS_STORAGE_LOCAL_PREFIXES value
// into the canonical prefix slice. Empty entries and whitespace
// around prefixes are tolerated; an empty string returns the
// default. A value that lists zero non-empty prefixes is
// rejected (the router would lose its fallback).
func parseLocalPrefixes(raw string) ([]string, error) {
	if raw == "" {
		return defaultLocalPrefixes, nil
	}
	// An explicit remote-only value is intentionally different from an
	// empty environment variable. Empty preserves the historical default
	// for single-box and mixed OCI deployments; "none" is the fail-closed
	// production declaration that no artifact namespace may be served from
	// a node-local filesystem.
	if strings.EqualFold(strings.TrimSpace(raw), "none") {
		return []string{}, nil
	}
	seen := make(map[string]struct{})
	out := make([]string, 0, 4)
	for _, p := range strings.Split(raw, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if !strings.HasSuffix(p, "/") {
			// Match the PrefixRouter contract: a route
			// must end in '/' so dispatch never splits a
			// key on a non-boundary. ADR-054 keeps the
			// canonical form here for symmetry with the
			// constructor.
			p += "/"
		}
		if _, dup := seen[p]; dup {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	if len(out) == 0 {
		return nil, errors.New("storage: FAAS_STORAGE_LOCAL_PREFIXES is empty after parsing")
	}
	return out, nil
}

// validateSharedArtifactMode enforces the split-node storage contract before
// constructing any backend. A PrefixRouter with a local fallback is otherwise
// perfectly valid from the storage package's perspective, but it is unsafe for
// snapshot restore: identical logical keys can resolve to different bytes on
// different compute nodes. Keep the gate in the shared env seam so vmmd,
// imaged, and any future artifact consumer cannot accidentally diverge.
func validateSharedArtifactMode(kind string) error {
	raw, ok := os.LookupEnv("FAAS_REQUIRE_SHARED_ARTIFACTS")
	if !ok || strings.TrimSpace(raw) == "" || raw == "0" || strings.EqualFold(strings.TrimSpace(raw), "false") {
		return nil
	}
	if !strings.EqualFold(strings.TrimSpace(raw), "1") && !strings.EqualFold(strings.TrimSpace(raw), "true") {
		return fmt.Errorf("storage: FAAS_REQUIRE_SHARED_ARTIFACTS=%q must be 0, 1, false, or true", raw)
	}
	if kind != "oci" {
		return fmt.Errorf("storage: FAAS_REQUIRE_SHARED_ARTIFACTS requires FAAS_STORAGE_BACKEND=oci; got %q", kind)
	}
	rawPrefixes, set := os.LookupEnv("FAAS_STORAGE_LOCAL_PREFIXES")
	if !set || !strings.EqualFold(strings.TrimSpace(rawPrefixes), "none") {
		return errors.New("storage: shared artifact mode requires explicit FAAS_STORAGE_LOCAL_PREFIXES=none; refusing node-local artifact routes")
	}
	registry := strings.TrimSpace(os.Getenv("FAAS_OCI_REGISTRY"))
	parsedRegistry, err := url.Parse(registry)
	if err != nil || !strings.EqualFold(parsedRegistry.Scheme, "https") || parsedRegistry.Host == "" {
		return errors.New("storage: shared artifact mode requires FAAS_OCI_REGISTRY to use an HTTPS URL")
	}
	if stale := strings.TrimSpace(os.Getenv("FAAS_STORAGE_CACHE_SERVE_STALE")); stale != "" {
		on, err := strconv.ParseBool(stale)
		if err != nil || on {
			return errors.New("storage: shared artifact mode forbids FAAS_STORAGE_CACHE_SERVE_STALE; stale local blobs cannot be trusted")
		}
	}
	return nil
}

// localBackendFromEnv builds a PrefixRouter over FAAS_STORAGE_ROOT +
// (optional) FAAS_APPS_ROOT, with each configured local prefix
// routing to the canonical fc backend. The router collapses to a
// single LocalStorageBackend when the two roots coincide.
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
	// Local-only backend: register ONLY the apps/ prefix as a route.
	// The local-prefix set (snap/, base/, kernel/, layers/) is a list
	// of *subdirs of fcBackend's root* — registering them as routes
	// would strip the prefix and make fcBackend.Put land files at the
	// root of /srv/fc/ rather than in the matching subdir. That also
	// crashed imaged under ProtectSystem=strict + ReadWritePaths= on
	// the subdirs: the LocalStorageBackend's atomic-rename temp file
	// would land at /srv/fc/<tmp>, which is NOT whitelisted. Falling
	// through to fcBackend with the full key preserves the subdir
	// (file at /srv/fc/base/<key>) and keeps the temp at
	// /srv/fc/base/<tmp> — both inside the whitelisted subdir.
	//
	// FAAS_STORAGE_LOCAL_PREFIXES is ignored in the local backend —
	// it's an OCI-side knob that says "don't ship these to the
	// registry"; in a local-only deployment there's nothing to keep
	// apart. Honouring it here as routes would re-introduce the bug.
	//
	// CI run 30650464753 (2026-07-31) repro: deploy of PR #467 fell
	// into the rollback path because this bug pre-dates the PR. Fix
	// lives here in the router wiring, not in LocalStorageBackend.Put.
	router, err := NewPrefixRouter(
		map[string]StorageBackend{"apps/": appsBackend},
		fcBackend,
	)
	if err != nil {
		return nil, fmt.Errorf("storage: prefix router: %w", err)
	}
	return router, nil
}

// ociBackendFromEnv wires the OCIRegistryStorageBackend with the
// local-prefix set routed to a sibling LocalStorageBackend rooted
// at FAAS_STORAGE_ROOT. Production today has FAAS_STORAGE_ROOT =
// /srv/fc; the local backend holds canonical content-addressed
// blobs (snap/, base/, kernel/, layers/) and the OCI backend
// serves the per-app layer + everything else.
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
	oci, err := NewOCIRegistryStorageBackend(opts...)
	if err != nil {
		return nil, fmt.Errorf("storage: oci backend: %w", err)
	}
	storageRoot := envOr("FAAS_STORAGE_ROOT", "/srv/fc")
	prefixes, err := parseLocalPrefixes(os.Getenv("FAAS_STORAGE_LOCAL_PREFIXES"))
	if err != nil {
		return nil, err
	}
	routes := make(map[string]StorageBackend, len(prefixes))
	for _, p := range prefixes {
		// PrefixRouter strips the matched prefix before handing the
		// operation to its route. Give each local route a backend rooted
		// at that prefix's directory so kernel/1.7.0 resolves to
		// FAAS_STORAGE_ROOT/kernel/1.7.0 rather than
		// FAAS_STORAGE_ROOT/1.7.0. The old shared-root route silently
		// dropped the namespace and broke VMMD kernel/base staging.
		prefixRoot, err := NewLocalStorageBackend(filepath.Join(storageRoot, strings.TrimSuffix(p, "/")))
		if err != nil {
			return nil, fmt.Errorf("storage: local prefix %q: %w", p, err)
		}
		routes[p] = prefixRoot
	}
	router, err := NewPrefixRouter(routes, oci)
	if err != nil {
		return nil, fmt.Errorf("storage: prefix router: %w", err)
	}
	return router, nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// AsCacheBackend walks the backend chain rooted at root and returns
// the first *LocalCacheBackend it finds, or nil if none is present.
//
// The walk covers the two shapes BackendFromEnv produces today:
//
//   - root is *LocalCacheBackend directly (the wrap-with-cache
//     outer layer; rare when the inner is a *PrefixRouter because
//     wrapWithCache always sits on top).
//   - root is *PrefixRouter wrapping a *LocalCacheBackend in one
//     of its routes or its fallback (the production multi-box
//     shape: local-prefix routes hold the LocalStorageBackend, the
//     fallback holds the LocalCacheBackend → OCI registry chain).
//
// Future shapes (a metrics wrapper, a tracing wrapper, a router
// enclosing the cache instead of being enclosed by it) are handled
// by recursing through *PrefixRouter.routes and .fallback. Any
// unrecognised wrapper type is skipped; a cache observer wired by
// the caller MUST NOT silently fail to attach.
//
// Returns nil when no cache backend is reachable from root. Daemons
// rely on a nil result to log "cache not wired" at startup rather
// than install an observer that never fires — the alternative is a
// silent zero-counter that looks healthy while stale-fallbacks
// happen unmonitored.
func AsCacheBackend(root StorageBackend) *LocalCacheBackend {
	if root == nil {
		return nil
	}
	if c, ok := root.(*LocalCacheBackend); ok {
		return c
	}
	if r, ok := root.(*PrefixRouter); ok {
		for _, child := range r.routes {
			if c := AsCacheBackend(child); c != nil {
				return c
			}
		}
		if r.fallback != nil {
			return AsCacheBackend(r.fallback)
		}
	}
	return nil
}
