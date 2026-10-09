// completion_cache.go — Tier A8 / ADR-083.
//
// Slug/org/project/environment and recent build/deployment-ID cache that powers `gregale completion <shell>` for the
// per-account positional completion paths (e.g. <slug> in
// `gregale app <slug> ...` or `gregale projects info <slug>`). The cache lives at
// ${UserConfigDir}/gregale/completion-cache-<credential-hash>.json and is rewritten
// on every 2xx response from a qualifying list endpoint (/v1/apps,
// /v1/orgs, /v1/projects, /v1/projects/{slug}/environments, /v1/builds,
// /v1/deployments)
// by the c.do middleware
// (client.go::doReq). Completion
// scripts read it on TAB and emit the values; the user never has
// to refresh by hand (auto-refresh model, per ADR-083 §Decision 2).
//
// Security posture (CLAUDE.md §11, gap G2 lean):
//
//   - File path: ${UserConfigDir}/gregale/, mode 0700 for the dir
//     and 0600 for the file. World-readable is a leak of the
//     account's app, org, project, environment slugs, and recent build and
//     deployment IDs —
//     equivalent to an
//     unauthenticated `ls` against the API. The cache MUST NOT expose secrets; it
//     only stores slugs, IDs, and names. Cache filenames use an HMAC-SHA-256
//     fingerprint keyed by the bearer credential and scoped to the API base,
//     so separate account contexts cannot read each other's suggestions; the
//     credential is not stored in the filename or cache contents.
//   - Atomic writes: tmp file in the same directory, then os.Rename.
//     Mirrors LocalStorageBackend.Put (storage-tmp-sibling-of-final,
//     cmd/e2e log). A crash mid-write leaves either the previous
//     good file or a fresh tmp that the next refresh overwrites.
//   - TTL: 24h via file mtime. Operators who want a forced refresh
//     remove the matching completion-cache-<credential-hash>.json file and
//     the next list call repopulates.
//   - Errors: every write is swallowed at the call site (c.do
//     middleware) with slog.Warn. A broken cache must NEVER fail
//     a request — that would be a much worse user experience
//     than a stale completion list.
//
// File format:
//
//	{
//	  "version": 1,
//	  "apps":   [{"slug":"demo","id":"...","name":"demo"}],
//	  "orgs":   [{"slug":"acme","id":"...","name":"Acme"}],
//	  "projects": [{"slug":"shop","id":"...","name":"shop"}],
//	  "environments": [{"slug":"staging","id":"...","name":"staging"}],
//	  "builds": [{"id":"..."}],
//	  "deployments": [{"id":"..."}],
//	  "project_environments": [{"project_slug":"shop","slug":"staging"}],
//	  "saved_at": "2026-08-08T12:34:56Z"
//	}
//
// The version field is the schema rev — bumping it invalidates
// every existing cache file. Keep it at 1 for additive optional
// fields and bump it only for incompatible shape changes.

package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// CompletionCache is the on-disk cache of completion-eligible
// values (app, org, project, and project environment slugs, plus recent build
// and deployment IDs). Credential-bound
// caches use a distinct file per API base and bearer credential. Safe for
// concurrent use.
type CompletionCache struct {
	mu             sync.Mutex
	path           string // override via env; computed lazily
	pathOverridden bool
	namespace      string
	now            func() time.Time
	ttl            time.Duration // 24h by default
}

// CompletionCacheEntry is the persisted shape. The version tag
// lets the reader reject caches from incompatible schema revs
// without a per-field feature flag.
type CompletionCacheEntry struct {
	Version             int                                 `json:"version"`
	Apps                []CompletionCacheRecord             `json:"apps"`
	Orgs                []CompletionCacheRecord             `json:"orgs"`
	Projects            []CompletionCacheRecord             `json:"projects,omitempty"`
	Environments        []CompletionCacheRecord             `json:"environments,omitempty"`
	Builds              []CompletionCacheRecord             `json:"builds,omitempty"`
	Deployments         []CompletionCacheRecord             `json:"deployments,omitempty"`
	ProjectEnvironments []CompletionCacheProjectEnvironment `json:"project_environments,omitempty"`
	SavedAt             time.Time                           `json:"saved_at"`
}

// CompletionCacheRecord carries a slug-bearing value or an ID-only value.
// Slug and Name are omitted for ID-only records such as cached build and
// deployment IDs.
type CompletionCacheRecord struct {
	ID   string `json:"id"`
	Slug string `json:"slug,omitempty"`
	Name string `json:"name,omitempty"`
}

// CompletionCacheProjectEnvironment maps an environment slug to its project.
// Keep project_slug before slug in the JSON field order: shell completion
// readers filter these compact records without requiring jq.
type CompletionCacheProjectEnvironment struct {
	ProjectSlug string `json:"project_slug"`
	Slug        string `json:"slug"`
}

const (
	// completionCacheVersion is the schema rev. Bump on breaking
	// changes to CompletionCacheEntry.
	completionCacheVersion = 1

	// completionCacheTTL is how long a cache file is considered
	// fresh. The cache is overwritten on every 2xx list response,
	// so this is mostly a safety net for offline boxes (and a
	// guard against zombie caches persisting across account
	// changes).
	completionCacheTTL        = 24 * time.Hour
	completionCacheIDLimit    = 200
	completionCacheBuildLimit = completionCacheIDLimit

	// completionCacheEnvPath lets tests (and operators who want
	// a different location) override the computed path. Empty
	// means "use UserConfigDir".
	completionCacheEnvPath = "FAAS_COMPLETION_CACHE_PATH"
)

// NewCompletionCache returns an unscoped cache rooted at the legacy default
// path (UserConfigDir/gregale/completion-cache.json). NewClient uses
// NewCompletionCacheForCredential instead. Tests override via SetPath().
func NewCompletionCache() *CompletionCache {
	return &CompletionCache{
		now: time.Now,
		ttl: completionCacheTTL,
	}
}

// NewCompletionCacheForCredential returns a cache isolated to one API base
// and bearer credential. Only an HMAC-SHA-256 fingerprint is used in the
// filename; the credential itself is never written to the cache or its path.
func NewCompletionCacheForCredential(baseURL, token string) *CompletionCache {
	identity := hmac.New(sha256.New, []byte(token))
	_, _ = identity.Write([]byte("gregale-completion-cache-v1\x00" + baseURL))
	return &CompletionCache{
		namespace: hex.EncodeToString(identity.Sum(nil)),
		now:       time.Now,
		ttl:       completionCacheTTL,
	}
}

// SetPath overrides the on-disk path. Tests call this with a
// t.TempDir() result to keep the test hermetic.
func (c *CompletionCache) SetPath(path string) {
	c.mu.Lock()
	c.path = path
	c.pathOverridden = true
	c.mu.Unlock()
}

// Path returns the on-disk path the cache reads/writes. The path
// is computed lazily on first call (and cached) — UserConfigDir
// requires the home directory which is stable but accessing it
// before flag parsing is a tad eager.
func (c *CompletionCache) Path() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.pathLocked()
}

// pathLocked is Path's body without the lock. Callers MUST hold
// c.mu — used by readLocked and writeEntryLocked when they are
// already inside the MaybeRefresh outer critical section, to avoid
// re-entrant Lock on the same mutex.
func (c *CompletionCache) pathLocked() string {
	if c.path != "" {
		return c.path
	}
	if env := os.Getenv(completionCacheEnvPath); env != "" {
		c.path = env
		c.pathOverridden = true
		return c.path
	}
	base, err := os.UserConfigDir()
	if err != nil || base == "" {
		base = os.TempDir()
	}
	name := "completion-cache.json"
	if c.namespace != "" {
		name = "completion-cache-" + c.namespace + ".json"
	}
	c.path = filepath.Join(base, "gregale", name)
	return c.path
}

// ClearAll removes every completion cache file in the default gregale config
// directory. With SetPath or FAAS_COMPLETION_CACHE_PATH, it removes only that
// explicit override so isolated callers do not touch the user's other caches.
// Missing files and directories are already clear and return nil.
func (c *CompletionCache) ClearAll() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	path := c.pathLocked()
	if c.pathOverridden {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("remove completion cache: %w", err)
		}
		return nil
	}

	dir := filepath.Dir(path)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read completion cache directory: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !isCompletionCacheFile(entry.Name()) {
			continue
		}
		if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("remove completion cache %q: %w", entry.Name(), err)
		}
	}
	return nil
}

func isCompletionCacheFile(name string) bool {
	return name == "completion-cache.json" ||
		(strings.HasPrefix(name, "completion-cache-") && strings.HasSuffix(name, ".json")) ||
		(strings.HasPrefix(name, "completion-cache.") && strings.HasSuffix(name, ".tmp"))
}

// Read returns the cached entry plus the file's mtime. A missing
// or stale file returns (zero, time.Time{}, nil) — callers can
// always render a no-completion fallback. A corrupt file is
// treated as missing (the next refresh overwrites it) so a
// half-flushed tmp from a previous crash never bricks the CLI.
func (c *CompletionCache) Read() (CompletionCacheEntry, time.Time, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.readLocked()
}

// readLocked is Read's lock-held body. Callers MUST hold c.mu.
// Used by MaybeRefresh to fold the read-modify-write into one
// critical section.
func (c *CompletionCache) readLocked() (CompletionCacheEntry, time.Time, error) {
	path := c.pathLocked()
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return CompletionCacheEntry{}, time.Time{}, nil
		}
		return CompletionCacheEntry{}, time.Time{}, fmt.Errorf("read completion cache: %w", err)
	}
	var e CompletionCacheEntry
	if err := json.Unmarshal(data, &e); err != nil {
		// Corrupt cache — treat as missing. Don't surface the error
		// to the user; the next refresh overwrites.
		return CompletionCacheEntry{}, time.Time{}, nil //nolint:nilerr
	}
	if e.Version != completionCacheVersion {
		return CompletionCacheEntry{}, time.Time{}, nil
	}
	st, err := os.Stat(path)
	if err != nil {
		return CompletionCacheEntry{}, time.Time{}, nil //nolint:nilerr
	}
	return e, st.ModTime(), nil
}

// IsFresh returns true if the cache file is younger than the TTL.
// A zero mtime (missing cache) is not fresh. Stale caches are
// still readable; the freshness check is advisory — completion
// scripts may choose to render stale values rather than nothing.
func (c *CompletionCache) IsFresh(mtime time.Time) bool {
	if mtime.IsZero() {
		return false
	}
	return c.now().Sub(mtime) < c.ttl
}

// WriteEntry persists entry. Atomic: tmp file in the same dir,
// then os.Rename. The dir is created with mode 0700 (the account
// owner's eyes only); the file is 0600. Errors are returned to
// the caller — the c.do middleware swallows them with slog.Warn.
func (c *CompletionCache) WriteEntry(entry CompletionCacheEntry) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.writeEntryLocked(entry)
}

// writeEntryLocked is WriteEntry's lock-held body. Callers MUST
// hold c.mu. Used by MaybeRefresh to fold the read-modify-write
// into one critical section.
func (c *CompletionCache) writeEntryLocked(entry CompletionCacheEntry) error {
	entry.Version = completionCacheVersion
	if entry.SavedAt.IsZero() {
		entry.SavedAt = c.now().UTC()
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal completion cache: %w", err)
	}
	path := c.pathLocked()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mkdir completion cache dir: %w", err)
	}
	// MkdirAll is umask-honoring; explicit chmod guarantees the dir
	// is 0700 even when the process umask would otherwise strip
	// the execute bit. The file mode is enforced via tmp.Chmod
	// below for the same reason.
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("chmod completion cache dir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, "completion-cache.*.tmp")
	if err != nil {
		return fmt.Errorf("create completion cache tmp: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) } //nolint:errcheck
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close() //nolint:errcheck
		cleanup()
		return fmt.Errorf("write completion cache tmp: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close() //nolint:errcheck
		cleanup()
		return fmt.Errorf("chmod completion cache tmp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("close completion cache tmp: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return fmt.Errorf("rename completion cache tmp: %w", err)
	}
	return nil
}

// MaybeRefresh inspects a 2xx response body and, if path matches
// a known list endpoint, decodes the relevant completion values and
// rewrites the cache. Errors are swallowed at the call site
// (c.do logs via slog.Warn) — a broken cache must never fail a
// request.
//
// Recognised paths today:
//
//   - GET /v1/apps   → bare JSON array of AppResponse (slug field).
//   - GET /v1/orgs   → {"orgs":[OrgResponse, ...]} envelope.
//   - GET /v1/projects → bare ProjectSummaryResponse array.
//   - GET /v1/projects/{slug}/environments → bare environment array.
//   - GET /v1/builds → a paged build list whose items carry UUIDs.
//   - GET /v1/deployments → a paged account-wide deployment list whose items
//     carry UUIDs.
//
// Add new endpoints by extending the path switch. Each qualifying
// response updates only its matching cache field.
//
// Concurrency: the read-modify-write is serialised under c.mu so
// concurrent refreshes of DIFFERENT fields don't clobber each other.
// Read + WriteEntry both take the lock individually; without the
// outer lock here, the inner locks allow a baseline read, a peer
// rewrite, and a stale baseline write that loses the peer's field.
// The outer lock collapses the RMW into one critical section.
func (c *CompletionCache) MaybeRefresh(path string, body []byte) {
	if len(body) == 0 {
		return
	}
	switch path {
	case "/v1/builds", "/v1/deployments":
		var page struct {
			Items []struct {
				ID string `json:"id"`
			} `json:"items"`
		}
		if err := json.Unmarshal(body, &page); err != nil {
			return
		}
		recent := make([]CompletionCacheRecord, 0, len(page.Items))
		for _, item := range page.Items {
			if item.ID == "" {
				continue
			}
			recent = append(recent, CompletionCacheRecord{ID: item.ID})
		}
		if len(recent) == 0 {
			return
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		existing, _, _ := c.readLocked()
		if path == "/v1/builds" {
			existing.Builds = mergeCompletionBuildRecords(recent, existing.Builds)
		} else {
			existing.Deployments = mergeCompletionIDRecords(recent, existing.Deployments)
		}
		_ = c.writeEntryLocked(existing)
	case "/v1/apps":
		var apps []struct {
			ID   string `json:"id"`
			Slug string `json:"slug"`
			Name string `json:"name"`
		}
		if err := json.Unmarshal(body, &apps); err != nil {
			return
		}
		recs := make([]CompletionCacheRecord, 0, len(apps))
		for _, a := range apps {
			if a.Slug == "" {
				continue
			}
			recs = append(recs, CompletionCacheRecord{ID: a.ID, Slug: a.Slug, Name: a.Name})
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		existing, _, _ := c.readLocked()
		existing.Apps = recs
		_ = c.writeEntryLocked(existing)
	case "/v1/orgs":
		var env struct {
			Orgs []struct {
				ID   string `json:"id"`
				Slug string `json:"slug"`
				Name string `json:"name"`
			} `json:"orgs"`
		}
		if err := json.Unmarshal(body, &env); err != nil {
			return
		}
		recs := make([]CompletionCacheRecord, 0, len(env.Orgs))
		for _, o := range env.Orgs {
			if o.Slug == "" {
				continue
			}
			recs = append(recs, CompletionCacheRecord{ID: o.ID, Slug: o.Slug, Name: o.Name})
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		existing, _, _ := c.readLocked()
		existing.Orgs = recs
		_ = c.writeEntryLocked(existing)
	case "/v1/projects":
		var projects []struct {
			ID   string `json:"id"`
			Slug string `json:"slug"`
		}
		if err := json.Unmarshal(body, &projects); err != nil {
			return
		}
		recs := make([]CompletionCacheRecord, 0, len(projects))
		known := make(map[string]struct{}, len(projects))
		for _, project := range projects {
			if project.Slug == "" {
				continue
			}
			recs = append(recs, CompletionCacheRecord{ID: project.ID, Slug: project.Slug, Name: project.Slug})
			known[project.Slug] = struct{}{}
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		existing, _, _ := c.readLocked()
		existing.Projects = recs
		// The project list is authoritative. Drop per-project environment
		// entries for projects that were removed since the previous list.
		projectEnvironments := make([]CompletionCacheProjectEnvironment, 0, len(existing.ProjectEnvironments))
		for _, environment := range existing.ProjectEnvironments {
			if _, ok := known[environment.ProjectSlug]; ok {
				projectEnvironments = append(projectEnvironments, environment)
			}
		}
		existing.ProjectEnvironments = projectEnvironments
		_ = c.writeEntryLocked(existing)
	default:
		if !isProjectEnvironmentsListPath(path) {
			return
		}
		var environments []struct {
			ID   string `json:"id"`
			Slug string `json:"slug"`
		}
		if err := json.Unmarshal(body, &environments); err != nil {
			return
		}
		recs := make([]CompletionCacheRecord, 0, len(environments))
		for _, environment := range environments {
			if environment.Slug == "" {
				continue
			}
			recs = append(recs, CompletionCacheRecord{ID: environment.ID, Slug: environment.Slug, Name: environment.Slug})
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		project := projectSlugFromEnvironmentsPath(path)
		if project == "" {
			return
		}
		existing, _, _ := c.readLocked()
		// Keep the flat list for generic scope completions, and retain a
		// separate project-keyed list so another project's last list request
		// cannot change project-aware environment suggestions.
		existing.Environments = recs
		projectKnown := false
		for _, projectRecord := range existing.Projects {
			if projectRecord.Slug == project {
				projectKnown = true
				break
			}
		}
		if !projectKnown {
			existing.Projects = append(existing.Projects, CompletionCacheRecord{Slug: project, Name: project})
		}
		projectRecs := make([]CompletionCacheProjectEnvironment, 0, len(existing.ProjectEnvironments)+len(recs))
		for _, environment := range existing.ProjectEnvironments {
			if environment.ProjectSlug != project {
				projectRecs = append(projectRecs, environment)
			}
		}
		for _, environment := range recs {
			projectRecs = append(projectRecs, CompletionCacheProjectEnvironment{ProjectSlug: project, Slug: environment.Slug})
		}
		existing.ProjectEnvironments = projectRecs
		_ = c.writeEntryLocked(existing)
	}
}

func mergeCompletionBuildRecords(recent, existing []CompletionCacheRecord) []CompletionCacheRecord {
	return mergeCompletionIDRecords(recent, existing)
}

func mergeCompletionIDRecords(recent, existing []CompletionCacheRecord) []CompletionCacheRecord {
	merged := make([]CompletionCacheRecord, 0, completionCacheIDLimit)
	seen := make(map[string]struct{}, len(recent)+len(existing))
	for _, records := range [][]CompletionCacheRecord{recent, existing} {
		for _, record := range records {
			if record.ID == "" {
				continue
			}
			if _, ok := seen[record.ID]; ok {
				continue
			}
			seen[record.ID] = struct{}{}
			merged = append(merged, record)
			if len(merged) == completionCacheIDLimit {
				return merged
			}
		}
	}
	return merged
}

func isProjectEnvironmentsListPath(path string) bool {
	return projectSlugFromEnvironmentsPath(path) != ""
}

func projectSlugFromEnvironmentsPath(path string) string {
	const prefix = "/v1/projects/"
	const suffix = "/environments"
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) {
		return ""
	}
	project := strings.TrimSuffix(strings.TrimPrefix(path, prefix), suffix)
	if project == "" || strings.Contains(project, "/") {
		return ""
	}
	return project
}
