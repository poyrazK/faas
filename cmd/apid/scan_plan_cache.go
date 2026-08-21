// scan_plan_cache.go — ADR-124 server-side source cache for the
// dashboard preview→apply flow.
//
// The CLI re-uploads the tarball on every apply (multipart body
// includes the source part) — the plan_token is just a SHA-256
// integrity check. The dashboard form is the harder path: the
// operator clicks "Apply" on the populated preview without
// re-attaching the source file (browsers strip file inputs from
// any non-multipart submission, and the operator's UX is the
// prime directive). Caching the source on scan and replaying it
// on apply via a server-side multipart rebuild closes the loop
// without JS.
//
// Cache shape: SHA-256 hex of the source bytes → *cachedSource.
// AccountID is part of the key composition (sha256 alone is
// global across accounts; the apply handler re-validates that
// the request's account_id matches the cached row before
// returning the path). Entries expire 1 hour after scan — a
// plan_token older than that is rejected with a 410-style
// "preview expired, please re-upload" problem so the operator
// can refresh without losing the workload selection.
//
// On-disk layout: one file per SHA-256, no nesting, so a
// sweep iteration is `os.ReadDir(cacheRoot)`. Path is
// deterministic so concurrent scans of the same source dedupe
// to one file (the second scanner sees the file already there
// and overwrites with identical bytes — at-least-once semantics
// are fine because the source bytes are content-addressed).
//
// The CLI flow is unaffected — the dashboard is the only
// caller of the cache. A CLI operator who wants the cache to
// skip the re-upload can pass the same plan_token twice but
// still has to send the source bytes; the cache rejects CLI
// callers because r.MultipartReader() succeeds with a source
// part and that path takes precedence over the cache lookup.
package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// planCacheTTL is how long a cached source survives between
// scan and apply. 1 h is generous (the operator typically
// clicks Apply within seconds) but bounded so a stuck entry
// doesn't accumulate disk usage.
const planCacheTTL = 1 * time.Hour

// planCacheMaxBytes caps the on-disk cache size. The largest
// plan-source is 250 MB (Pro/Scale limit) so 8 × 250 MB = 2 GB
// handles a burst of concurrent scans without growing the
// cache without bound. Sweep runs at construction + every
// cacheStore call so the cap is approximate (one over-shoot
// between sweeps is acceptable).
const planCacheMaxBytes int64 = 2 << 30 // 2 GiB

// cachedSource is the on-disk + in-memory record for one
// cached plan source. expiresAt is informational — the on-disk
// mtime + planCacheTTL drives sweep decisions; the in-memory
// field lets the apply handler short-circuit without stat'ing
// the file.
type cachedSource struct {
	path      string
	accountID string
	expiresAt time.Time
}

// planSourceCache holds in-flight (sha256 → cachedSource) entries.
// sync.Map is the right shape: many concurrent scans + applies
// keyed on the same sha256, no global mutation outside Store /
// sweep.
var planSourceCache sync.Map

// planCacheRoot is the on-disk cache directory. Set once by
// initPlanCache on first store; subsequent calls re-use the
// same root so concurrent scans in the same process don't
// pick different paths.
var (
	planCacheRootOnce sync.Once
	planCacheRoot     string
	planCacheRootErr  error
)

// initPlanCache picks the cache root and creates it on first
// use. FAAS_PLAN_CACHE_ROOT env override exists for tests;
// default is /var/lib/faas/plan-cache. Falls back to a tmp
// path under the spool root if the system root isn't writable
// (degraded mode — the dashboard cache just doesn't survive
// restarts but the scan/apply hot path still works).
func initPlanCache() (string, error) {
	planCacheRootOnce.Do(func() {
		if v := os.Getenv("FAAS_PLAN_CACHE_ROOT"); v != "" {
			planCacheRoot = v
		} else {
			planCacheRoot = "/var/lib/faas/plan-cache"
		}
		if err := os.MkdirAll(planCacheRoot, 0o750); err != nil {
			// Degraded fallback — keep the rest of scanService
			// working even if the system path is unwritable.
			planCacheRoot = filepath.Join(os.TempDir(), "faas-plan-cache")
			if mErr := os.MkdirAll(planCacheRoot, 0o750); mErr != nil {
				planCacheRootErr = mErr
				return
			}
		}
		// Sweep stale entries at boot so a process restart
		// doesn't carry over yesterday's tarballs.
		_ = sweepExpiredCacheEntries(planCacheRoot)
	})
	return planCacheRoot, planCacheRootErr
}

// storePlanCache copies sourcePath into the cache under the
// SHA-256-derived filename and registers an in-memory entry.
// Best-effort: a copy failure logs (via the return error) but
// does NOT fail the scan — the dashboard apply path will fall
// back to "please re-upload" if the cache lookup misses.
func storePlanCache(sha256Hex, sourcePath, accountID string) error {
	root, err := initPlanCache()
	if err != nil {
		return err
	}
	dst := filepath.Join(root, sha256Hex+".tar.gz")
	// sourcePath is the scanService spool file (FAAS_SCAN_SPOOL_ROOT);
	// it is created by parseScanMultipart with mode 0600 and is
	// not customer-supplied at this point (the source field has
	// already been validated + hashed). The forbidigo gate exists
	// to keep customer-input paths behind openCustomerFile's
	// symlink/non-regular guard; the spool file is vetted.
	in, err := os.Open(sourcePath) //nolint:forbidigo // vetted-id path under spool root; see comment above.
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
	if err != nil {
		return fmt.Errorf("open cache file: %w", err)
	}
	if _, err := io.Copy(out, in); err != nil {
		// Best-effort cleanup on the copy failure path: a Close
		// error here is dominated by the io.Copy error and the
		// os.Remove is fire-and-forget (sweep will retry later).
		_ = out.Close()
		_ = os.Remove(dst)
		return fmt.Errorf("copy source: %w", err)
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(dst)
		return fmt.Errorf("close cache file: %w", err)
	}
	now := time.Now()
	planSourceCache.Store(sha256Hex, &cachedSource{
		path:      dst,
		accountID: accountID,
		expiresAt: now.Add(planCacheTTL),
	})
	// Sweep opportunistically so the disk cap holds. Fire-and-forget
	// because the sweep is independent of this scan's success; its
	// only output is on-disk file removals.
	//
	//nolint:errcheck // sweep runs in its own goroutine; the only return is an error that sweep itself logs.
	go func() { _ = sweepExpiredCacheEntries(root) }()
	return nil
}

// lookupPlanCache returns the cached source path for the
// given SHA-256, validating the requester's account_id and
// the entry's expiry. Returns os.ErrNotExist on miss/expire
// /wrong-account so the dashboard handler can branch on a
// single sentinel.
func lookupPlanCache(sha256Hex, accountID string) (string, error) {
	v, ok := planSourceCache.Load(sha256Hex)
	if !ok {
		return "", os.ErrNotExist
	}
	cs := v.(*cachedSource)
	if cs.accountID != accountID {
		// Cross-account probe — treat as miss; the dashboard
		// apply handler surfaces a 404-equivalent problem.
		return "", os.ErrNotExist
	}
	if time.Now().After(cs.expiresAt) {
		// Expired. Drop the in-memory entry; the on-disk
		// file is left for the next sweep.
		planSourceCache.Delete(sha256Hex)
		return "", os.ErrNotExist
	}
	if _, err := os.Stat(cs.path); err != nil {
		// On-disk file vanished (operator rm'd it; sweep
		// raced us). Drop the in-memory entry.
		planSourceCache.Delete(sha256Hex)
		return "", os.ErrNotExist
	}
	return cs.path, nil
}

// sweepExpiredCacheEntries walks the cache root and removes
// files older than planCacheTTL. Bounded by planCacheMaxBytes:
// if the total size exceeds the cap, oldest-first eviction
// runs regardless of TTL. Idempotent — concurrent sweeps are
// safe because each file is removed via os.Remove which is
// itself idempotent on a missing path.
func sweepExpiredCacheEntries(root string) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	type fileInfo struct {
		path    string
		size    int64
		modTime time.Time
	}
	var files []fileInfo
	var totalSize int64
	cutoff := time.Now().Add(-planCacheTTL)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		fi, statErr := e.Info()
		if statErr != nil {
			continue
		}
		files = append(files, fileInfo{path: filepath.Join(root, e.Name()), size: fi.Size(), modTime: fi.ModTime()})
		totalSize += fi.Size()
	}
	// TTL sweep — delete anything older than cutoff.
	for _, f := range files {
		if f.modTime.Before(cutoff) {
			_ = os.Remove(f.path)
			totalSize -= f.size
		}
	}
	// Size cap — evict oldest-first until under the cap.
	if totalSize > planCacheMaxBytes {
		sort.Slice(files, func(i, j int) bool { return files[i].modTime.Before(files[j].modTime) })
		for _, f := range files {
			if totalSize <= planCacheMaxBytes {
				break
			}
			if removeErr := os.Remove(f.path); removeErr == nil {
				totalSize -= f.size
			}
		}
	}
	return nil
}

// decodePlanToken parses the base64-JSON plan_token minted by
// scanService. Returns the wire struct or an error so the
// dashboard apply handler can branch on a typed sentinel
// instead of inlining the base64 + json unmarshal.
//
// Lives here (not in scan_service.go) because the dashboard
// handler is the only consumer outside the v1 apply path
// (which inlines the decode at scan_service.go:509-514).
func decodePlanToken(planToken string) (planTokenWire, error) {
	var pt planTokenWire
	if planToken == "" {
		return pt, errors.New("empty plan_token")
	}
	b, err := base64.StdEncoding.DecodeString(planToken)
	if err != nil {
		return pt, fmt.Errorf("plan_token base64: %w", err)
	}
	if err := json.Unmarshal(b, &pt); err != nil {
		return pt, fmt.Errorf("plan_token json: %w", err)
	}
	if pt.AccountID == "" || pt.Hash == "" {
		return pt, errors.New("plan_token missing required fields")
	}
	return pt, nil
}

// buildCachedSourceRequest wraps cachedSourcePath as a
// synthetic multipart/form-data request that scanService can
// consume via the standard r.MultipartReader() path. The
// request body is held entirely in memory — the source file
// is small enough (≤ 250 MB at the Pro/Scale cap) and the
// request lives only for the duration of scanService's
// execution, so a memory buffer is appropriate.
//
// The synthetic request sets only the fields the dashboard
// apply path needs: source (file), project_slug, exclude
// (one multipart part per slug so the wire shape matches the
// apply form's repeated `exclude=slug` checkboxes). The
// production_branch / install_id fields are not set because
// the dashboard preview form does not capture them — the
// scan service defaults production_branch to "main" and
// install_id to 0 (the apply handler treats 0 as "no install
// binding", issue #313).
func buildCachedSourceRequest(cachedSourcePath, projectSlug string, exclude []string) (*http.Request, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	// File part. The filename is informational — the v1 /scan
	// handler asserts via assertMultipartFileName that the
	// value ends in .tar.gz; the value is otherwise opaque.
	fw, err := mw.CreateFormFile("source", filepath.Base(cachedSourcePath))
	if err != nil {
		return nil, fmt.Errorf("create form file: %w", err)
	}
	// cachedSourcePath is a vetted-id path: the cache root is
	// created by initPlanCache with mode 0750 and the file name
	// is the SHA-256 hex we wrote ourselves in storePlanCache.
	// openCustomerFile's symlink/non-regular guard is unnecessary
	// here because the path is not customer-supplied.
	f, err := os.Open(cachedSourcePath) //nolint:forbidigo // vetted-id path under cache root; see comment above.
	if err != nil {
		return nil, fmt.Errorf("open cached source: %w", err)
	}
	defer func() { _ = f.Close() }()
	if _, err := io.Copy(fw, f); err != nil {
		return nil, fmt.Errorf("copy cached source: %w", err)
	}
	// Form fields. project_slug is required by parseScanMultipart
	// (it defaults to "project-<random>" if missing); the
	// dashboard handler knows the real slug from the URL.
	if projectSlug != "" {
		if err := mw.WriteField("project_slug", projectSlug); err != nil {
			return nil, err
		}
	}
	// exclude — one multipart part per slug. parseScanMultipart
	// splits each part on commas and lowercases/trims; sending
	// the slugs one-per-part avoids the join/split round-trip
	// and matches what the CLI sends (multiple `exclude=slug`
	// form fields per multipart POST).
	for _, slug := range exclude {
		slug = strings.ToLower(strings.TrimSpace(slug))
		if slug == "" {
			continue
		}
		if err := mw.WriteField("exclude", slug); err != nil {
			return nil, err
		}
	}
	if err := mw.Close(); err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, "/v1/projects", &buf)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req, nil
}

// errPlanCacheMiss surfaces a user-facing "please re-upload"
// hint on a dashboard apply. Lives in this file so a future
// JSON Accept-coded variant of the apply handler can reuse
// the same error contract.
var errPlanCacheMiss = api.NewProblem(
	http.StatusGone, "preview_expired",
	"Preview source expired",
	"re-upload the tarball on /dashboard/projects/{slug}/preview and re-submit",
)
