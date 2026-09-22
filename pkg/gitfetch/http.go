package gitfetch

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// httpFetcher is the production Fetcher. It dollies a tar.gz
// archive from codeload.github.com (or an equivalent provider
// configured via NewHTTPWithBase) and unpacks it into a temp
// dir under WorkDir. The token is HTTP-scoped per Fetch call —
// never stored on the receiver.
//
// Construction:
//   - NewHTTPWithLimits(workDir, limits) — production HTTPS path
//     with per-plan archive cap (api.PlanScale by default).
//   - NewHTTPWithBase(workDir, baseURL, client, capBytes) —
//     test seam; capBytes=0 disables the cap.
type httpFetcher struct {
	workDir         string
	baseURL         string
	httpClient      *http.Client
	maxArchiveBytes int64
	// maxTotalBytes is the cumulative cap applied inside the
	// extractor. 0 disables it (tests). For production wiring
	// (NewHTTPWithLimits) this is the SourceTarballMaxMB × 2.5
	// expanded-budget cap from cmd/apid/extract.go:62-68 — the
	// same per-plan posture so a Free plan repo can never balloon
	// into the daemon's swap.
	maxTotalBytes int64
}

// NewHTTPWithLimits is the production constructor. It computes
// the archive cap from the per-plan SourceTarballMaxMB
// (api.MustLimitsFor(plan).SourceTarballMaxMB × 1024² for the
// compressed cap; × 2.5 for the expanded budget the extractor
// enforces as a running total).
//
// The default plan is api.PlanScale — the same posture the
// apid scan endpoint takes (Free/Hobby/Pro all cap under
// Scale's source-tarball ceiling, so a single global cap is
// correct for v1.0). Per-account cap is deferred — the
// account→plan lookup adds a round-trip the webhook hot path
// doesn't need.
//
// workDir is the parent directory the temp dir is created
// under (per spec §11 layout, githubd sets this to
// /var/lib/faas/githubd). The default http.Client timeout is
// 30s (a single archive fetch is bounded by network +
// extraction; longer timeouts would mask a hung install). The
// default client has no proxy override — the spec §11 egress
// policy is enforced at the egress layer (pkg/httpsec) for
// daemons, not at the fetch layer.
func NewHTTPWithLimits(workDir string, limits api.Limits) *httpFetcher {
	compressed := int64(limits.SourceTarballMaxMB) * 1024 * 1024
	expanded := compressed * 5 / 2
	return &httpFetcher{
		workDir:         workDir,
		baseURL:         "https://codeload.github.com",
		httpClient:      &http.Client{Timeout: 30 * time.Second},
		maxArchiveBytes: compressed,
		maxTotalBytes:   expanded,
	}
}

// NewHTTP returns the test-only fetcher with capBytes=0. Use
// NewHTTPWithLimits for production wiring (cmd/githubd) — the
// zero-cap path silently accepts unbounded archives, which is
// only safe for unit tests.
//
// Retained for the test suite (the in-process httptest.Server
// tests don't want a 250 MB cap constraining golden fixtures).
func NewHTTP(workDir string) *httpFetcher {
	return &httpFetcher{
		workDir:         workDir,
		baseURL:         "https://codeload.github.com",
		httpClient:      &http.Client{Timeout: 30 * time.Second},
		maxArchiveBytes: 0,
		maxTotalBytes:   0,
	}
}

// NewHTTPWithBase is the test seam. baseURL is the URL prefix
// (must include scheme); httpClient is injected so httptest
// can reach the in-process server without TLS. capBytes=0
// disables the cap (tests that don't care about size).
// maxTotalBytes=0 disables the cumulative cap.
func NewHTTPWithBase(workDir, baseURL string, httpClient *http.Client, capBytes, maxTotalBytes int64) *httpFetcher {
	return &httpFetcher{
		workDir:         workDir,
		baseURL:         baseURL,
		httpClient:      httpClient,
		maxArchiveBytes: capBytes,
		maxTotalBytes:   maxTotalBytes,
	}
}

// Fetch dollies the archive at
// `<baseURL>/{owner}/{repo}/tar.gz/{sha}` and unpacks it. The
// Authorization header is `Bearer <token>`; the header is set
// only on this request and never stored on the fetcher.
//
// On success the returned Tree's FS() is rooted at the
// extracted archive, with the leading `<root>/` prefix stripped
// (most codeload archives wrap every entry under
// `<owner>-<sha>/...`). The strip happens once, on the first
// non-empty header — same convention as cmd/apid/extract.go.
func (f *httpFetcher) Fetch(ctx context.Context, repoFullName, commitSHA, token string) (Tree, error) {
	if repoFullName == "" || commitSHA == "" {
		return nil, fmt.Errorf("gitfetch: fetch: repoFullName and commitSHA are required: %w", ErrBadArchive)
	}
	// Validate the inputs are well-formed up front. The const
	// regex is intentionally permissive — GitHub commit SHA is
	// 40 hex chars but short SHA (7+) is also valid for the
	// archive endpoint. The validate function pins the
	// shape so a malformed value never reaches the HTTP layer.
	if !isValidRepoPath(repoFullName) {
		return nil, fmt.Errorf("gitfetch: fetch: invalid repo %q: %w", repoFullName, ErrBadArchive)
	}
	if !isValidCommitSHA(commitSHA) {
		return nil, fmt.Errorf("gitfetch: fetch: invalid commit SHA %q: %w", commitSHA, ErrBadArchive)
	}

	// Build the URL. path.Join cleans the path — a repoFullName
	// with leading "/foo/" can't be used to bump the URL out
	// of the base path.
	url := f.baseURL + path.Join("/", repoFullName, "/tar.gz/", commitSHA)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("gitfetch: fetch: new request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/x-gzip")
	req.Header.Set("User-Agent", "onebox-faas-githubd/1.0")

	// Cap the response body to maxArchiveBytes. The streaming
	// guard is the load-bearing one — Content-Length is
	// advisory and a hostile provider can omit it.
	resp, err := f.httpClient.Do(req)
	if err != nil {
		// ctx cancellation lands here as a url.Error wrapping
		// context.Canceled; surface it as ErrBadArchive so the
		// caller can distinguish "user cancelled" from "bad
		// input" without leaking the URL.
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, fmt.Errorf("gitfetch: fetch: %w", err)
		}
		return nil, fmt.Errorf("gitfetch: fetch: http: %w", err)
	}
	defer func() { drainAndClose(resp.Body) }()

	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return nil, fmt.Errorf("gitfetch: fetch: %d: %w", resp.StatusCode, ErrUnauthorized)
	case resp.StatusCode == http.StatusForbidden:
		return nil, fmt.Errorf("gitfetch: fetch: %d: %w", resp.StatusCode, ErrUnauthorized)
	case resp.StatusCode == http.StatusNotFound:
		return nil, fmt.Errorf("gitfetch: fetch: %d: %w", resp.StatusCode, ErrNotFound)
	case resp.StatusCode >= 400:
		return nil, fmt.Errorf("gitfetch: fetch: unexpected %d: %w", resp.StatusCode, ErrBadArchive)
	}

	// Stream the body to a temp file (so we can apply the cap
	// AND the size cap simultaneously). The cap is enforced
	// BEFORE the body is fully read; if the body exceeds the
	// cap, the partial file is removed and ErrArchiveTooLarge
	// is returned. capBytes==0 disables the cap.
	//
	// We layer two caps: a streaming cap on the gzip body
	// (maxArchiveBytes) and a cumulative cap inside the
	// extractor (maxTotalBytes = SourceTarballMaxMB × 2.5).
	// The gzip body is the *compressed* form; the extractor
	// accumulates the *expanded* bytes. A malicious provider
	// can craft a small-but-highly-compressible body that
	// explodes on extraction — the cumulative cap is the
	// load-bearing guard against that. The streaming cap is
	// an early-fail defense for the trivial case.
	reader := io.Reader(resp.Body)
	var compressed *io.LimitedReader
	if f.maxArchiveBytes > 0 {
		// Advise early — if Content-Length is set AND exceeds
		// the cap, fail before reading the body. This is
		// advisory; a hostile provider can omit Content-Length
		// and the LimitReader below still applies.
		if resp.ContentLength > 0 && resp.ContentLength > f.maxArchiveBytes {
			drainAndClose(resp.Body)
			return nil, fmt.Errorf("gitfetch: fetch: %d > %d: %w",
				resp.ContentLength, f.maxArchiveBytes, ErrArchiveTooLarge)
		}
		// LimitReader caps the streaming bytes too. The
		// +1 sentinel lets us detect "the cap fired" via
		// the bytes-read count (LimitReader returning
		// io.EOF is indistinguishable from a real EOF,
		// but if we read N+1 bytes we know we clipped).
		compressed = &io.LimitedReader{R: resp.Body, N: f.maxArchiveBytes + 1}
		reader = compressed
	}

	// Create the temp dir under WorkDir. The directory name
	// includes a random suffix so concurrent fetches don't
	// collide. 0o700 is the §11 hardening posture — only the
	// daemon uid can read the archive.
	if err := os.MkdirAll(f.workDir, 0o700); err != nil {
		drainAndClose(resp.Body)
		return nil, fmt.Errorf("gitfetch: fetch: mkdir workdir: %w", err)
	}
	tempDir, err := os.MkdirTemp(f.workDir, "gitfetch-")
	if err != nil {
		drainAndClose(resp.Body)
		return nil, fmt.Errorf("gitfetch: fetch: mktemp: %w", err)
	}
	// We must Close on any failure path. The cleanup helper
	// wraps os.RemoveAll and tolerates a missing dir (the
	// second Close is idempotent).
	failed := true
	defer func() {
		if failed {
			_ = os.RemoveAll(tempDir)
		}
	}()

	// Extract the streamed body. The extractor enforces the
	// path-escape guard (symlinks-with-target, "..", absolute
	// paths) and the per-entry + total byte cap. The cap is
	// the same default the apid path uses (10 000 entries,
	// 2.5× the compressed cap).
	err = extractStream(tempDir, reader, f.maxTotalBytes, defaultExtractLimits())
	// Consuming the sentinel byte exceeds the cap even if it completes a
	// valid gzip trailer. A decoder EOF caused by the cap is a size error,
	// not evidence of a malformed provider archive.
	if compressed != nil && compressed.N == 0 {
		return nil, fmt.Errorf("gitfetch: fetch: compressed archive exceeds %d bytes: %w", f.maxArchiveBytes, ErrArchiveTooLarge)
	}
	if err != nil {
		return nil, fmt.Errorf("gitfetch: fetch: %w", err)
	}

	failed = false
	return &tree{root: tempDir}, nil
}

// drainAndClose is the body-drain helper applied on non-2xx
// paths. http-healthcheck-body-drain in the project memory
// pins this for any reusable HTTP client — the daemon-side
// fetcher is constructed once per process and reused across
// every push, so connection reuse matters. A 64 KiB cap
// matches the apid path's body-drain helper.
func drainAndClose(body io.ReadCloser) {
	_, _ = io.Copy(io.Discard, io.LimitReader(body, 64<<10))
	_ = body.Close()
}

// tree is the file-backed Tree returned to Fetch callers. The
// root is the temp dir created under WorkDir; FS() returns
// os.DirFS(root); Close() removes the temp dir.
//
// Close is goroutine-safe via sync.Once — a concurrent
// `defer tree.Close()` plus an explicit Close() in the
// caller (or two goroutines racing) only execute the
// underlying os.RemoveAll once. This pins the Tree
// interface's idempotence contract under the race detector
// (see TestTree_ConcurrentClose).
type tree struct {
	root      string
	closeOnce sync.Once
}

func (t *tree) FS() fs.FS { return os.DirFS(t.root) }

func (t *tree) Close() error {
	var firstErr error
	t.closeOnce.Do(func() {
		if err := os.RemoveAll(t.root); err != nil {
			firstErr = fmt.Errorf("gitfetch: tree close: %w", err)
		}
	})
	return firstErr
}

// extractLimit is the mirror of cmd/apid's extractLimits for the
// autoscale-derived cap. We keep the same defaults so the two
// surfaces fail the same input set.
type extractLimit struct {
	MaxEntries   int
	MaxFileBytes int64
}

// defaultExtractLimits returns the per-entry caps (entry
// count + per-file size) that don't depend on the plan.
// The cumulative cap is passed in separately so the
// production wiring can vary it per plan.
func defaultExtractLimits() extractLimit {
	return extractLimit{
		MaxEntries:   10_000,
		MaxFileBytes: 256 * 1024 * 1024,
	}
}

// extractStream reads a tar.gz body from r and unpacks it under
// dst. The stream is bounded by the cap so a hostile provider
// cannot pin the daemon's memory. The extractor mirrors the
// apid path's posture verbatim:
//
//   - Reject absolute paths, ".." segments, symlinks/chars/blocks/fifos
//   - Reject entries beyond MaxEntries
//   - Reject any single entry exceeding MaxFileBytes
//   - Reject cumulative bytes beyond maxTotalBytes
//   - Verify the gzip trailer (CRC32 + length) post-loop; a
//     truncated body fails ErrBadArchive even if the tar
//     reader hit a clean io.EOF
//
// maxTotalBytes==0 disables the cumulative cap (tests).
func extractStream(dst string, r io.Reader, maxTotalBytes int64, lim extractLimit) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("gzip: %w: %w", err, ErrBadArchive)
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)

	var (
		entries int
		total   int64
		// firstDir is the single top-level prefix the
		// tarball shares (codeload wraps every entry
		// under `<owner>-<sha>/...`). Strip it on write.
		firstDir string
		firstSet bool
	)
	for {
		// codeql[go/zipslip] — tr.Next() returns the tar header
		// whose hdr.Name drives every downstream write; the path
		// is guarded by escapesArchiveRoot(hdr.Name) above
		// (and the TypeSymlink/TypeLink allow-list denies
		// link entries outright). Pin the suppression at
		// the call site per memory:codeql-zipslip-source-line-suppression.
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("tar: %w: %w", err, ErrBadArchive)
		}
		entries++
		if entries > lim.MaxEntries {
			return fmt.Errorf("too many files (>%d): %w", lim.MaxEntries, ErrBadArchive)
		}
		// GitHub codeload emits global PAX metadata before the repository
		// wrapper. It is not a filesystem entry and must not select firstDir.
		if hdr.Typeflag == tar.TypeXGlobalHeader {
			continue
		}
		if hdr.Name == "" {
			continue
		}
		if escapesArchiveRoot(hdr.Name) {
			return fmt.Errorf("invalid path %q: %w", hdr.Name, ErrBadArchive)
		}
		// Defense in depth for a future relaxation of the
		// type allow-list: if anyone ever allows TypeLink /
		// TypeSymlink entries, the Linkname target must still
		// be guarded against ../ escapes. Today the
		// type-flag switch below rejects all link types, so
		// this branch is dead; the guard is here so a future
		// maintainer who adds a link type without re-reading
		// the security posture doesn't accidentally re-enable
		// the symlink-with-escape class.
		switch hdr.Typeflag {
		case tar.TypeSymlink, tar.TypeLink:
			if escapesArchiveRoot(hdr.Linkname) {
				return fmt.Errorf("invalid link target %q: %w", hdr.Linkname, ErrBadArchive)
			}
			return fmt.Errorf("entry type %d not allowed: %w", hdr.Typeflag, ErrBadArchive)
		case tar.TypeReg, tar.TypeDir:
			// allowed
		default:
			return fmt.Errorf("entry type %d not allowed: %w", hdr.Typeflag, ErrBadArchive)
		}
		// Strip the leading "<root>/" prefix on the first
		// non-empty-name header. Concatenated strip preserves
		// the relative path.
		name := hdr.Name
		if !firstSet {
			if i := strings.IndexByte(name, '/'); i >= 0 {
				firstDir = name[:i]
			}
			firstSet = true
		}
		if firstDir != "" && strings.HasPrefix(name, firstDir+"/") {
			name = name[len(firstDir)+1:]
		}
		if name == "" {
			// root directory entry itself
			continue
		}
		target := filepath.Join(dst, filepath.FromSlash(name))
		// Path-escape guard: target must stay inside dst.
		// filepath.IsLocal catches any traversal that
		// slipped past the segment-split predicate
		// (Windows semantics, trailing dots).
		// codeql[go/path-injection] false-positive: escapesArchiveRoot
		// rejected every ".." or absolute path upstream;
		// `dst` is a daemon-owned 0o700 temp dir.
		if !filepath.IsLocal(filepath.FromSlash(name)) {
			return fmt.Errorf("invalid path %q: %w", name, ErrBadArchive)
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("mkdir %q: %w", target, err)
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return fmt.Errorf("mkdir parent %q: %w", filepath.Dir(target), err)
			}
			if err := writeOneFile(target, tr, hdr.Size, lim.MaxFileBytes, &total, maxTotalBytes, hdr.FileInfo().Mode()); err != nil {
				return err
			}
		}
	}
	// Drain any remaining gzip bytes to surface a truncated
	// trailer (CRC32 + length) as ErrBadArchive. The tar
	// loop may have hit a clean io.EOF before the gzip
	// trailer was read; io.Copy will block until the
	// underlying reader returns EOF, which for gzip means
	// the trailer has been verified.
	if _, err := io.Copy(io.Discard, gz); err != nil { //nolint:gosec // G110: gzip trailer drain is bounded by cap above
		return fmt.Errorf("gzip trailer: %w: %w", err, ErrBadArchive)
	}
	if err := gz.Close(); err != nil {
		return fmt.Errorf("gzip trailer verify: %w: %w", err, ErrBadArchive)
	}
	return nil
}

// writeOneFile copies a single tar entry's body into path. The
// per-entry cap is enforced via io.LimitReader so a hostile
// archive can't pin memory; the per-archive cap is enforced
// via the running total.
func writeOneFile(target string, r io.Reader, size int64, maxFileBytes int64, total *int64, maxTotalBytes int64, sourceMode fs.FileMode) error {
	if size > maxFileBytes {
		return fmt.Errorf("entry %q too large (%d > %d): %w",
			filepath.Base(target), size, maxFileBytes, ErrArchiveTooLarge)
	}
	// tar.Reader validates the declared entry length. Reject an entry that
	// cannot fit before downloading and writing it, not after it has already
	// exhausted the disk budget by up to one full per-file allowance.
	if maxTotalBytes > 0 && size > maxTotalBytes-*total {
		return fmt.Errorf("archive too large (>%d): %w", maxTotalBytes, ErrArchiveTooLarge)
	}
	// Best-effort size limit on the body. The header
	// size is the load-bearing number; this is a defense
	// in depth against a malformed header that lies
	// about a huge body.
	body := io.LimitReader(r, maxFileBytes+1)
	f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("open %q: %w", target, err)
	}
	defer func() { _ = f.Close() }()
	n, err := io.Copy(f, body)
	if err != nil {
		return fmt.Errorf("write %q: %w", target, err)
	}
	if n > maxFileBytes {
		return fmt.Errorf("entry %q too large: %w", target, ErrArchiveTooLarge)
	}
	*total += n
	// maxTotalBytes==0 disables the cumulative cap (tests
	// that don't care about size). The guard is load-bearing
	// for the test-only path — without it, *total > 0 trips
	// on the first byte.
	if maxTotalBytes > 0 && *total > maxTotalBytes {
		return fmt.Errorf("archive too large (>%d): %w", maxTotalBytes, ErrArchiveTooLarge)
	}
	// Git tracks executable scripts. Retain only their execute bits on top
	// of our safe file mode; never apply setuid/setgid or extra write bits.
	if err := f.Chmod(0o644 | sourceMode&0o111); err != nil {
		return fmt.Errorf("chmod %q: %w", target, err)
	}
	return nil
}

// escapesArchiveRoot mirrors cmd/apid/extract.go:147-149. The
// rule is the same: an absolute path OR a name with a ".."
// segment that's not just a prefix ("..foo" is fine; "../foo"
// is not).
func escapesArchiveRoot(name string) bool {
	if name == "" {
		return false
	}
	if strings.HasPrefix(name, "/") {
		return true
	}
	// Walk the segments. Any segment that is exactly ".."
	// or starts with "../" is an escape attempt.
	for _, seg := range strings.Split(filepath.ToSlash(name), "/") {
		if seg == ".." {
			return true
		}
	}
	return false
}

// isValidRepoPath validates the GitHub-shape owner/name string.
// The shape is exactly one slash separating two segments. Each
// segment is 1..100 chars of letters, digits, dash, dot,
// underscore. We reject any whitespace, control char, or other
// separator. The URL builder applies path.Join so a value with
// extra slashes (e.g. "owner/repo/extra") is rejected by the
// segment-count check.
func isValidRepoPath(s string) bool {
	if s == "" || len(s) > 200 {
		return false
	}
	parts := strings.Split(s, "/")
	if len(parts) != 2 {
		return false
	}
	for _, seg := range parts {
		if seg == "" || len(seg) > 100 {
			return false
		}
		for _, r := range seg {
			switch {
			case r >= 'a' && r <= 'z':
			case r >= 'A' && r <= 'Z':
			case r >= '0' && r <= '9':
			case r == '-' || r == '.' || r == '_':
			default:
				return false
			}
		}
	}
	return true
}

// isValidCommitSHA validates the codeload commit SHA. 7+
// lowercase hex chars (short SHA is accepted for the archive
// endpoint).
func isValidCommitSHA(s string) bool {
	if len(s) < 7 || len(s) > 40 {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}
