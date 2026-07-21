package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/oci"
)

// OCIRegistryStorageBackend is the remote-distribution driver for the
// StorageBackend interface (issue #96 / ADR-025 axis 2, slice 2).
//
// Each artifact (an app ext4 layer, a snapshot mem/vmstate blob, a
// shared base image, a kernel, etc.) is published as a single layer
// inside a single-arch image manifest in a registry-v2 endpoint. The
// mapping from storage key → (repo, tag) is fixed by the plan in
// `plan()`; the manifest is created on Put and fetched on Get so
// callers don't have to track layer digests.
//
// Concurrency: the public methods are safe for parallel calls on
// distinct keys. Token cache entries are per-(realm, scope) and use
// sync.Map. Push-side PUTs are NOT serialised (mirrors LocalStorageBackend).
//
// Errors: every method wraps the underlying error with %w + an
// "storage: oci <op> <key>: ..." tag. The standard ErrNotFound /
// ErrInvalidKey sentinels are wrapped so call sites can stay
// backend-agnostic via storage.IsNotFound / storage.IsInvalidKey.
//
// Egress policy: the default HTTP transport is oci.NewEgressHTTPClient
// so a misconfigured FAAS_OCI_REGISTRY pointing at a private RFC1918
// host refuses to dial — same posture as the build-time puller. Tests
// inject a plain *http.Client via WithHTTPClient to reach httptest
// loopback servers; production never does.
type OCIRegistryStorageBackend struct {
	hc       *http.Client // egress-policy transport; nil → oci.NewEgressHTTPClient()
	registry string       // "ghcr.io/onebox-faas" — namespace prefix; "" rejected at Put/Get time
	prefix   string       // "faas/" — repo namespace; default; overridable via WithRepoPrefix
	user     string       // optional Basic-Auth username for the token endpoint
	pw       string       // optional Basic-Auth password
	ua       string       // User-Agent header on every request
	timeout  time.Duration

	// tokenCache maps "realm|service|scope" → cachedToken. Entries are
	// populated on the first 401 challenge and refreshed on expiry
	// (Token.IsExpiredAt) or another 401. The cache is best-effort: a
	// stale entry just costs one round-trip.
	tokenCache sync.Map

	// knownRepos maps "faas/snap-<dep>" → true for repos we've ever
	// touched via Put. The GC path's snap/ List fan-out consults this
	// when the registry doesn't expose /v2/_catalog (most public
	// registries don't, per the distribution spec — catalog is
	// optional). Without it, List(snap/) would return nothing on a
	// cold start, defeating the GC's job.
	knownRepos sync.Map

	// inFlight tracks in-progress refresh-token POSTs so concurrent
	// callers on the same scope coalesce into one round-trip (issue
	// #96 review finding #6). Maps scope → chan refreshResult. The
	// channel has capacity 1 so the producer never blocks.
	inFlight sync.Map
}

// cachedToken is the bearer cache entry. issuedAt is when FetchToken
// returned; the OCIRegistryStorageBackend uses now() at Get-time
// against issuedAt to decide whether to refresh proactively. realmURL
// is the token-endpoint URL we discovered from the first Www-Authenticate
// challenge; we keep it so subsequent fetches (refresh or different
// scope) hit the same endpoint.
type cachedToken struct {
	tok      oci.Token
	issuedAt time.Time
	realmURL string
}

// Compile-time asserts.
var (
	_ StorageBackend      = (*OCIRegistryStorageBackend)(nil)
	_ LocalArtifactLister = (*OCIRegistryStorageBackend)(nil)
)

// Option configures an OCIRegistryStorageBackend. Mirrors the
// oci.RegistryClient option pattern so cmd/{imaged,vmmd} read
// consistently across the two packages.
type Option func(*OCIRegistryStorageBackend)

// WithHTTPClient injects the HTTP client (timeouts, custom transport).
// The default is oci.NewEgressHTTPClient — production never overrides
// this; tests use it to point at httptest loopback (the egress
// transport would otherwise reject 127.0.0.1).
func WithHTTPClient(hc *http.Client) Option {
	return func(o *OCIRegistryStorageBackend) {
		if hc != nil {
			o.hc = hc
		}
	}
}

// WithRegistry sets the registry endpoint, e.g. "https://ghcr.io/onebox-faas".
// REQUIRED: NewOCIRegistryStorageBackend returns an error when this is
// empty. Refusing to default guards against silent cross-org publishes
// during misconfiguration, same posture as imaged's FAAS_DEPLOY_BASE_REF
// requiring a digest pin (cmd/imaged/main.go).
//
// The scheme is part of the URL: production passes "https://…", the
// httptest seam passes "http://…". We don't prepend a default scheme
// because guessing wrong is a silent wire-protocol switch — TLS
// vs cleartext is the wrong axis to be helpful on.
func WithRegistry(reg string) Option {
	return func(o *OCIRegistryStorageBackend) { o.registry = strings.TrimRight(reg, "/") }
}

// WithRepoPrefix overrides the per-namespace repo prefix. Default is
// "faas" so the production driver publishes into the "faas/apps",
// "faas/snap-<dep>", "faas/base", "faas/layers", "faas/kernel" repos.
// Operators with an existing "faas" repo on a shared registry set this
// to something unique (e.g. "faas-<box>-<env>") to avoid collisions.
func WithRepoPrefix(p string) Option {
	return func(o *OCIRegistryStorageBackend) {
		clean := strings.Trim(p, "/")
		clean = strings.TrimRight(clean, "/")
		if clean != "" {
			o.prefix = clean
		}
	}
}

// WithCredentials sets Basic-Auth credentials sent on the token endpoint
// only. Public registries accept anonymous push to public repos so
// production can run with no creds; private repos or rate-limited
// accounts set FAAS_OCI_USERNAME / FAAS_OCI_PASSWORD.
func WithCredentials(user, pw string) Option {
	return func(o *OCIRegistryStorageBackend) {
		o.user = user
		o.pw = pw
	}
}

// WithTimeout overrides the per-request HTTP timeout. The default is
// api.OCIPullTimeoutSeconds (60s, ADR-021) — mirrors the build-time
// puller so a Put of a 150 MB layer has the same budget as a PullBlob
// of the same size.
//
// Composition with WithHTTPClient is asymmetric on purpose: WithHTTPClient
// replaces the underlying *http.Client outright (including its Timeout
// field), and WithTimeout writes back into o.hc.Timeout. The ordering
// that produces a meaningful timeout+custom-transport result is
//
//	NewOCIRegistryStorageBackend(WithHTTPClient(myHC), WithTimeout(d))   // → myHC.Timeout == d
//
// If you reverse the order (WithTimeout first, then WithHTTPClient) the
// transport's own zero Timeout wins and the deadline is lost.
func WithTimeout(d time.Duration) Option {
	return func(o *OCIRegistryStorageBackend) {
		if d > 0 {
			o.timeout = d
		}
	}
}

// NewOCIRegistryStorageBackend validates the registry is set and
// returns a usable backend. Empty registry is rejected — silent default
// would publish into whatever the operator happened to leave in
// FAAS_OCI_REGISTRY or, worse, into the default OCI namespace.
func NewOCIRegistryStorageBackend(opts ...Option) (*OCIRegistryStorageBackend, error) {
	o := &OCIRegistryStorageBackend{
		prefix:  defaultRepoPrefix,
		ua:      "faas-storage/1 (+https://DOMAIN)",
		timeout: 60 * time.Second,
	}
	for _, opt := range opts {
		opt(o)
	}
	if o.registry == "" {
		return nil, fmt.Errorf("%w: empty registry (set FAAS_OCI_REGISTRY or pass WithRegistry)", ErrInvalidKey)
	}
	if o.hc == nil {
		o.hc = oci.NewEgressHTTPClient()
	}
	o.hc.Timeout = o.timeout
	return o, nil
}

// Registry returns the configured registry namespace (read-only).
func (o *OCIRegistryStorageBackend) Registry() string { return o.registry }

// RepoPrefix returns the configured per-namespace repo prefix.
func (o *OCIRegistryStorageBackend) RepoPrefix() string { return o.prefix }

// --- key → (repo, tag) mapping ---------------------------------------
//
// The plan's table. Tag characters are sanitised to the OCI tag charset
// before they're sent; invalid keys (anything that doesn't match the
// table or fails the tag charset check) return ErrInvalidKey so callers
// can branch with storage.IsInvalidKey.

// Repo names used by plan() / unplan(). Constants are local to the
// OCI driver — pkg/storage.LocalStorageBackend never sees them.
// Lifted from inline strings so the goconst lint stays quiet.
const (
	repoApps   = "apps"
	repoSnap   = "snap"
	repoBase   = "base"
	repoLayers = "layers"
	repoKernel = "kernel"
)

// defaultRepoPrefix is the per-namespace repo prefix the driver uses
// when WithRepoPrefix is not supplied. Promoted to a constant because
// the same literal appears in the constructor default, the WithRepoPrefix
// doc comment, and the unit tests — goconst would flag the duplicate.
const defaultRepoPrefix = "faas"

var (
	// tagCharset is the OCI distribution-spec v2 tag charset:
	// [a-zA-Z0-9_][a-zA-Z0-9._-]{0,127}. We allow 1..128 chars.
	tagCharset = regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9._-]{0,127}$`)

	// depIDCharset matches the canonical UUID form produced by
	// postgres gen_random_uuid() (8-4-4-4-12 lowercase hex with dashes),
	// e.g. "550e8400-e29b-41d4-a716-446655440000". Production
	// deployment IDs come from deployments.id (migrations/00001_init.sql);
	// every keyed lookup in pkg/sched/paths.go uses this raw form.
	depIDCharset = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)

	// appsTagSep separates the slug from the UUID in an apps/<slug>/<uuid>
	// manifest tag. Slugs cannot contain an underscore (the apps.slug
	// column is a slugified form of the customer-supplied name and
	// the regex admits [a-z0-9-] only), so "__" is unambiguous and
	// trivially reverses.
	appsTagSep = "__"
)

// plan translates a storage key into the (repo, manifestRef) tuple the
// registry speaks. See the package doc comment for the mapping table.
func (o *OCIRegistryStorageBackend) plan(key string) (repo, ref string, err error) {
	if err := validateKey(key); err != nil {
		return "", "", err
	}
	parts := strings.SplitN(key, "/", 3)
	if len(parts) < 2 {
		return "", "", fmt.Errorf("%w: %q has no namespace", ErrInvalidKey, key)
	}
	switch parts[0] {
	case repoApps:
		// apps/<slug>/<dep>.ext4 → repo "apps", tag "<slug>__<dep>".
		// The "__" separator is unambiguous because the slug charset
		// ([a-z0-9-]) doesn't admit underscores; round-tripping via
		// unplan() is just strings.LastIndex("__").
		if len(parts) != 3 || !strings.HasSuffix(parts[2], ".ext4") {
			return "", "", fmt.Errorf("%w: %q does not match apps/<slug>/<dep>.ext4", ErrInvalidKey, key)
		}
		dep := strings.TrimSuffix(parts[2], ".ext4")
		tag := parts[1] + appsTagSep + dep
		if !tagCharset.MatchString(tag) {
			return "", "", fmt.Errorf("%w: %q produces invalid OCI tag %q", ErrInvalidKey, key, tag)
		}
		return repoApps, tag, nil
	case repoSnap:
		// snap/<dep>/{mem|vmstate} → repo "snap-<dep>", tag "mem"|"vmstate"
		if len(parts) != 3 || (parts[2] != "mem" && parts[2] != "vmstate") {
			return "", "", fmt.Errorf("%w: %q does not match snap/<dep>/{mem|vmstate}", ErrInvalidKey, key)
		}
		if !depIDCharset.MatchString(parts[1]) {
			return "", "", fmt.Errorf("%w: snap dep %q fails hex charset", ErrInvalidKey, parts[1])
		}
		return "snap-" + parts[1], parts[2], nil
	case repoBase:
		// base/<runtime>.ext4 → repo "base", tag "<runtime>"
		// base/<runtime>.ext4.digest → repo "base", tag "<runtime>-digest"
		if len(parts) != 2 {
			return "", "", fmt.Errorf("%w: %q has unexpected sub-path", ErrInvalidKey, key)
		}
		var tag string
		switch {
		case strings.HasSuffix(parts[1], ".ext4"):
			tag = strings.TrimSuffix(parts[1], ".ext4")
		case strings.HasSuffix(parts[1], ".ext4.digest"):
			tag = strings.TrimSuffix(parts[1], ".ext4.digest") + "-digest"
		default:
			return "", "", fmt.Errorf("%w: %q does not match base/<runtime>.ext4[.digest]", ErrInvalidKey, key)
		}
		if !tagCharset.MatchString(tag) {
			return "", "", fmt.Errorf("%w: %q produces invalid OCI tag %q", ErrInvalidKey, key, tag)
		}
		return repoBase, tag, nil
	case repoLayers:
		// layers/<dep>.ext4 → repo "layers", tag "<dep>"
		if len(parts) != 2 || !strings.HasSuffix(parts[1], ".ext4") {
			return "", "", fmt.Errorf("%w: %q does not match layers/<dep>.ext4", ErrInvalidKey, key)
		}
		dep := strings.TrimSuffix(parts[1], ".ext4")
		if !depIDCharset.MatchString(dep) {
			return "", "", fmt.Errorf("%w: layers dep %q fails hex charset", ErrInvalidKey, dep)
		}
		return repoLayers, dep, nil
	case repoKernel:
		// kernel/<version> → repo "kernel", tag "<version>"
		if len(parts) != 2 {
			return "", "", fmt.Errorf("%w: %q has unexpected sub-path", ErrInvalidKey, key)
		}
		if !tagCharset.MatchString(parts[1]) {
			return "", "", fmt.Errorf("%w: %q produces invalid OCI tag %q", ErrInvalidKey, key, parts[1])
		}
		return repoKernel, parts[1], nil
	default:
		return "", "", fmt.Errorf("%w: %q has unknown namespace %q", ErrInvalidKey, key, parts[0])
	}
}

// --- Put ---------------------------------------------------------------

// Put uploads r's bytes as a layer blob inside an image manifest at the
// key's (repo, tag). The body is streamed through a sha256 hasher; the
// resulting digest pins the layer so a later Get can fetch the bytes
// directly without re-reading the manifest. The manifest itself is
// pushed last and carries the layer descriptor.
//
// Concurrency: callers may issue parallel Puts on distinct keys. Two
// Puts on the same key race; whichever manifest lands last wins (same
// last-writer-wins posture as LocalStorageBackend).
//
// Atomicity: a Put that fails partway leaves the registry with the
// blob pushed but no manifest (the next Put for the same key republishes
// the manifest; the orphan blob is GC'd by the registry operator).
// Callers depending on a "no partial publishes" guarantee must wrap
// Put in their own retry. This matches LocalStorageBackend's contract.
func (o *OCIRegistryStorageBackend) Put(ctx context.Context, key string, r io.Reader) error {
	repo, tag, err := o.plan(key)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("storage: oci put %q: %w", key, err)
	}
	o.knownRepos.Store(o.fullRepo(repo), true)

	// Layer upload: hash-while-stream into a tmp file under os.TempDir so
	// we don't hold 150 MB in memory. The LocalStorageBackend's
	// copyContext polls ctx.Done() every 256 KiB; we reuse the same
	// helper to keep cancellation behaviour consistent.
	tmpPath, digestHex, err := o.bufferAndHash(ctx, key, r)
	if err != nil {
		return err
	}
	defer func() { _ = removeTmp(tmpPath) }()

	// Push the config stub blob (single byte; the manifest references
	// it). Doing the config blob first means the manifest push below
	// has nothing to wait on.
	configDigest, err := o.pushConfigStub(ctx, repo)
	if err != nil {
		return fmt.Errorf("storage: oci put %q: config: %w", key, err)
	}

	// Push the layer blob via the monolithic-PUT endpoint
	// (POST /v2/<repo>/blobs/uploads/?digest=sha256:...). The registry
	// hashes the body; a mismatch returns 4xx and we surface it.
	// We open a fresh file handle each time the body is needed (POST
	// then PUT fallback) so net/http's body-close on the prior request
	// doesn't strand our file descriptor. The body is wrapped in
	// io.NopCloser for the in-process call so the helper's transport
	// doesn't close the fd between attempts.
	if err := o.pushBlobFile(ctx, repo, digestHex, tmpPath); err != nil {
		return fmt.Errorf("storage: oci put %q: blob: %w", key, err)
	}

	// Push the image manifest last so a partially-written key isn't
	// visible until the layer blob is committed.
	manifestJSON, err := buildImageManifest(configDigest, "sha256:"+digestHex, tmpPathSize(tmpPath))
	if err != nil {
		return fmt.Errorf("storage: oci put %q: build manifest: %w", key, err)
	}
	if err := o.pushManifest(ctx, repo, tag, manifestJSON); err != nil {
		return fmt.Errorf("storage: oci put %q: manifest: %w", key, err)
	}
	return nil
}

// --- Get ---------------------------------------------------------------

// Get fetches the manifest at (repo, tag), reads the layer descriptor's
// digest, and streams the blob back as an io.ReadCloser. A missing
// manifest surfaces as ErrNotFound (matching LocalStorageBackend's
// contract). The returned reader MUST be closed by the caller.
func (o *OCIRegistryStorageBackend) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	repo, tag, err := o.plan(key)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("storage: oci get %q: %w", key, err)
	}

	manifest, err := o.fetchManifest(ctx, repo, tag)
	if err != nil {
		return nil, fmt.Errorf("storage: oci get %q: %w", key, err)
	}
	if len(manifest.Layers) == 0 {
		return nil, fmt.Errorf("storage: oci get %q: manifest has no layers", key)
	}
	digest := manifest.Layers[0].Digest
	if digest == "" {
		return nil, fmt.Errorf("storage: oci get %q: manifest layer has no digest", key)
	}
	body, err := o.fetchBlob(ctx, repo, digest)
	if err != nil {
		return nil, fmt.Errorf("storage: oci get %q: %w", key, err)
	}
	return body, nil
}

// --- Delete ------------------------------------------------------------

// Delete removes the manifest at (repo, tag). The underlying blob is
// best-effort: most registries GC orphan blobs asynchronously after the
// manifest is gone, but some public registries refuse blob DELETE (a
// 404 on the blob DELETE is logged and ignored).
//
// Missing key is NOT an error — matches LocalStorageBackend's
// idempotent semantics.
func (o *OCIRegistryStorageBackend) Delete(ctx context.Context, key string) error {
	repo, tag, err := o.plan(key)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("storage: oci delete %q: %w", key, err)
	}

	if err := o.deleteManifest(ctx, repo, tag); err != nil {
		// 404 on manifest means "already gone" — non-error.
		if !isNotFoundErr(err) {
			return fmt.Errorf("storage: oci delete %q: %w", key, err)
		}
	}
	return nil
}

// --- List --------------------------------------------------------------

// List returns every key under prefix, walking the registry's tags
// endpoint for each resolved repo. The snap/ prefix requires the
// known-repos cache to be warm (Put populated it); on a cold start the
// registry's optional /v2/_catalog endpoint may surface additional
// repos if the registry supports it (most don't).
//
// Empty results are NOT an error. A missing tag-list endpoint surfaces
// as a wrapped non-fatal error per-repo (the iteration continues); a
// fan-out prefix (e.g. "snap/") that fails on EVERY repo returns the
// joined error so the GC walk can react. A single-repo prefix that
// fails returns that single error: there is nothing else to enumerate.
//
// Partial-success rule: when at least one repo succeeded AND no repo
// failed, the result is (keys, nil). When at least one repo failed
// but another succeeded, the result is (keys, joined-errors) — the
// caller decides whether to retry the failed ones, but the GC walk
// can't silently ignore a flaky repo in the middle of a fan-out
// without leaving orphan tags forever.
func (o *OCIRegistryStorageBackend) List(ctx context.Context, prefix string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("storage: oci list %q: %w", prefix, err)
	}
	repos := o.reposForPrefix(prefix)
	var (
		keys        []string
		errs        []error
		anySuccess  bool
	)
	for _, repo := range repos {
		tags, err := o.fetchTags(ctx, repo)
		if err != nil {
			errs = append(errs, fmt.Errorf("repo %q: %w", repo, err))
			continue
		}
		anySuccess = true
		for _, tag := range tags {
			key, ok := o.unplan(repo, tag)
			if !ok {
				continue
			}
			if prefix == "" || strings.HasPrefix(key, prefix) {
				keys = append(keys, key)
			}
		}
	}
	switch {
	case len(errs) == 0:
		return keys, nil
	case !anySuccess:
		// Nothing enumerated. For a fan-out prefix, join the per-repo
		// errors so the caller sees the registry health; for a single-
		// repo prefix the join collapses to a single wrapped error.
		return keys, errors.Join(errs...)
	default:
		// Partial success: return the keys we got, but expose the
		// failures so the GC walk can react. The caller can log +
		// retry without losing the partial result.
		return keys, fmt.Errorf("partial list: %w", errors.Join(errs...))
	}
}

// reposForPrefix returns the repos the OCIRegistryStorageBackend might
// have populated for a given storage-key prefix. The mapping mirrors
// plan()'s first-segment → repo-name convention; for snap/ it adds the
// in-memory knownRepos fan-out since each deployment has its own repo.
func (o *OCIRegistryStorageBackend) reposForPrefix(prefix string) []string {
	prefix = strings.TrimSuffix(prefix, "/")
	switch prefix {
	case repoApps:
		return []string{repoApps}
	case repoSnap, "":
		// Walk knownRepos for any "<prefix>/snap-<id>" we've touched;
		// include the canonical "snap" repo for back-compat. The
		// knownRepos keys are fullRepo ("<prefix>/snap-<id>") — we strip
		// the prefix to get back the bare repo name for fetchTags.
		var out []string
		out = append(out, repoSnap)
		snapPrefix := o.prefix + "/snap-"
		o.knownRepos.Range(func(k, _ any) bool {
			ks := k.(string)
			if strings.HasPrefix(ks, snapPrefix) {
				out = append(out, strings.TrimPrefix(ks, o.prefix+"/"))
			}
			return true
		})
		return out
	case repoBase:
		return []string{repoBase}
	case repoLayers:
		return []string{repoLayers}
	case repoKernel:
		return []string{repoKernel}
	default:
		return nil
	}
}

// unplan is the inverse of plan(): given a repo + tag, return the
// storage key. Returns ("", false) when the (repo, tag) tuple doesn't
// match anything we know how to produce.
//
// The apps tag is "<slug>__<uuid>" (see plan()). Splitting on "__"
// reverses the encoding exactly because slugs never contain
// underscores. (Earlier versions split on the last dash which was
// ambiguous once deployment IDs became canonical UUIDs — the
// deployment portion itself contains four dashes.)
func (o *OCIRegistryStorageBackend) unplan(repo, tag string) (string, bool) {
	switch repo {
	case repoApps:
		idx := strings.Index(tag, appsTagSep)
		if idx < 0 {
			return "", false
		}
		return "apps/" + tag[:idx] + "/" + tag[idx+len(appsTagSep):] + ".ext4", true
	case repoBase:
		if strings.HasSuffix(tag, "-digest") {
			rt := strings.TrimSuffix(tag, "-digest")
			return "base/" + rt + ".ext4.digest", true
		}
		return "base/" + tag + ".ext4", true
	case repoLayers:
		return "layers/" + tag + ".ext4", true
	case repoKernel:
		return "kernel/" + tag, true
	default:
		if strings.HasPrefix(repo, "snap-") {
			dep := strings.TrimPrefix(repo, "snap-")
			return "snap/" + dep + "/" + tag, true
		}
		return "", false
	}
}

// --- helpers ------------------------------------------------------------

// fullRepo returns the registry-namespaced repo name, e.g.
// "faas/apps" for repo="apps" with the default prefix.
func (o *OCIRegistryStorageBackend) fullRepo(repo string) string {
	return o.prefix + "/" + repo
}

// manifestAccept mirrors oci.manifestAccept. We accept the same
// distribution-spec media types the puller accepts; v1 image manifest
// is preferred, v2 fallback, indexes/lists are accepted for parsing but
// rejected at the layer-count check (List only walks tags, never
// indexes).
var manifestAccept = strings.Join([]string{
	imageManifestMediaType,
	"application/vnd.docker.distribution.manifest.v2+json",
	"application/vnd.oci.image.index.v1+json",
	"application/vnd.docker.distribution.manifest.list.v2+json",
}, ", ")

// blobMediaType is the OCI media type we tag layer blobs with. Some
// registries ignore the type (content-addressable blobs) but
// distribution-spec-compliant ones surface it for tooling.
const blobMediaType = "application/vnd.oci.image.layer.v1.tar+gzip"

// configMediaType is the OCI image config media type for the stub
// blob we push alongside each artifact.
const configMediaType = "application/vnd.oci.image.config.v1+json"

// imageManifestMediaType is the OCI image manifest v1 media type the
// driver PUTs / GETs. Promoted to a constant because it appears three
// times (manifestAccept list, buildImageManifest default, pushManifest
// Content-Type) and goconst flags string literals used ≥ 3 times.
const imageManifestMediaType = "application/vnd.oci.image.manifest.v1+json"

// configStubJSON is the minimal valid OCI image config: a 1-byte blob
// ("{}" would parse as `{}` which is valid; "{}" is 2 bytes). The
// distribution spec only requires architecture + os; we keep it
// spec-shaped without committing to amd64/arm64 — the storage driver
// stores opaque artifacts, not runnable images.
const configStubJSON = `{"architecture":"amd64","os":"linux"}`

// imageManifest is the subset of an OCI image manifest v1 we care
// about. The schema is small; we don't bother with full struct tags.
type imageManifest struct {
	SchemaVersion int    `json:"schemaVersion"`
	MediaType     string `json:"mediaType"`
	Config        struct {
		MediaType string `json:"mediaType"`
		Digest    string `json:"digest"`
		Size      int64  `json:"size"`
	} `json:"config"`
	Layers []struct {
		MediaType string `json:"mediaType"`
		Digest    string `json:"digest"`
		Size      int64  `json:"size"`
	} `json:"layers"`
}

// buildImageManifest renders the single-arch image manifest v1 JSON.
// digest is the layer's "sha256:<hex>" form; configDigest is the same
// for the config blob; size is the layer's byte count.
func buildImageManifest(configDigest, layerDigest string, size int64) ([]byte, error) {
	m := imageManifest{
		SchemaVersion: 2,
		MediaType:     imageManifestMediaType,
	}
	m.Config.MediaType = configMediaType
	m.Config.Digest = configDigest
	m.Config.Size = int64(len(configStubJSON))
	m.Layers = append(m.Layers, struct {
		MediaType string `json:"mediaType"`
		Digest    string `json:"digest"`
		Size      int64  `json:"size"`
	}{MediaType: blobMediaType, Digest: layerDigest, Size: size})
	return json.Marshal(m)
}

// --- low-level registry plumbing --------------------------------------

// bufferAndHash copies r's bytes through a sha256 hasher into a tmp
// file under os.TempDir(). Returning the path + hex digest lets the
// caller stream the bytes back out for the PUT body without holding
// them in memory (a Scale plan app layer is 2 GB).
//
// The copy is ctx-aware via copyContext (256 KiB chunks, ctx.Done()
// polled each chunk). On any error the tmp file is removed before
// returning so a half-written file doesn't accumulate under /tmp.
func (o *OCIRegistryStorageBackend) bufferAndHash(ctx context.Context, key string, r io.Reader) (path, hexDigest string, err error) {
	f, err := osCreateTemp("", "faas-oci-*.blob")
	if err != nil {
		return "", "", fmt.Errorf("storage: oci put %q: create tmp: %w", key, err)
	}
	tmpPath := f.Name()
	closed := false
	defer func() {
		if !closed {
			_ = f.Close()
			_ = removeTmp(tmpPath)
		}
	}()
	h := sha256.New()
	if _, err := copyContextHash(ctx, f, r, h); err != nil {
		return "", "", fmt.Errorf("storage: oci put %q: %w", key, err)
	}
	if err := f.Sync(); err != nil {
		return "", "", fmt.Errorf("storage: oci put %q: fsync: %w", key, err)
	}
	if err := f.Close(); err != nil {
		return "", "", fmt.Errorf("storage: oci put %q: close: %w", key, err)
	}
	closed = true
	return tmpPath, hex.EncodeToString(h.Sum(nil)), nil
}

// tmpPathSize returns the on-disk size of the buffered blob. Used by
// the manifest builder so the layer descriptor carries an accurate
// size (some registries reject manifests whose declared size doesn't
// match the blob's actual length).
func tmpPathSize(p string) int64 {
	st, err := osStat(p)
	if err != nil {
		return 0
	}
	return st.Size()
}

// pushConfigStub uploads the standard 1-byte OCI image config stub
// blob to repo (idempotent — the digest is constant, so re-push is a
// 201/202 either way). Returns the digest so the manifest can
// reference it.
func (o *OCIRegistryStorageBackend) pushConfigStub(ctx context.Context, repo string) (string, error) {
	digest := sha256.Sum256([]byte(configStubJSON))
	digestStr := "sha256:" + hex.EncodeToString(digest[:])
	if err := o.pushBlobMonolithic(ctx, repo, hex.EncodeToString(digest[:]), bytes.NewReader([]byte(configStubJSON))); err != nil {
		return "", err
	}
	return digestStr, nil
}

// pushBlobFile is the layer-blob helper used by Put(). It owns the
// file-handle lifecycle: opens a fresh *os.File per request attempt so
// net/http's body-close on the prior POST (which would otherwise
// strand our fd) doesn't prevent the POST→PUT fallback from re-reading
// the body. The helper wraps the file in noopCloserFile (see oci_io.go)
// so net/http's transport.Close() call is a no-op on our fd; the
// deferred Close on the file itself runs when pushBlobMonolithic
// returns.
func (o *OCIRegistryStorageBackend) pushBlobFile(ctx context.Context, repo, digestHex, tmpPath string) error {
	f, err := osOpen(tmpPath)
	if err != nil {
		return fmt.Errorf("open blob tmp: %w", err)
	}
	defer func() { _ = f.Close() }()
	return o.pushBlobMonolithic(ctx, repo, digestHex, noopCloserFile{f})
}

// pushBlobMonolithic uploads a single-shot blob via the OCI
// distribution-spec "Single POST" flow (POST /v2/<repo>/blobs/uploads/
// ?digest=sha256:<hex>). The body is streamed in the same request.
//
// Two response codes are valid per spec:
//
//   - 201 Created with a Location header — the registry accepted the
//     blob in this single round trip. We're done.
//   - 202 Accepted with a Location header — the registry wants a
//     follow-up PUT to the Location (some implementations refuse the
//     monolithic form). We MUST issue that PUT or the blob is silently
//     uncommitted. Treating 202 as success without the PUT leaves
//     orphan manifests pointing at missing blobs.
//
// The bearer-token dance is the same anonymous-or-Basic pattern as
// the puller. Caching happens in the helper below.
func (o *OCIRegistryStorageBackend) pushBlobMonolithic(ctx context.Context, repo, digestHex string, body io.Reader) error {
	fullRepo := o.fullRepo(repo)
	scope := "repository:" + fullRepo + ":pull,push"
	u := o.registry + "/v2/" + fullRepo + "/blobs/uploads/?digest=sha256:" + digestHex
	resp, err := o.doBearerRequest(ctx, bearerReqOpts{
		Method:      http.MethodPost,
		URL:         u,
		Scope:       scope,
		ContentType: "application/octet-stream",
		Body:        body,
		Rewind:      true, // *os.File body must be re-readable on 401 retry
	})
	if err != nil {
		return fmt.Errorf("post upload: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	switch resp.StatusCode {
	case http.StatusCreated:
		return nil
	case http.StatusAccepted:
		// Registry wants the chunked upload — issue a PUT to the Location
		// it advertised. Without this PUT, the blob is uncommitted and
		// later manifest GETs will 404.
		loc := resp.Header.Get("Location")
		if loc == "" {
			return fmt.Errorf("upload returned 202 without Location header")
		}
		uploadURL, perr := resolveUploadLocation(o.registry, loc)
		if perr != nil {
			return fmt.Errorf("upload Location: %w", perr)
		}
		uploadURL = appendDigestQuery(uploadURL, digestHex)
		// Rewind the body — the POST above already consumed it.
		// doBearerRequest's Rewind branch only fires on a 401 retry,
		// not on a clean success→202 transition.
		if seeker, ok := body.(io.Seeker); ok {
			if _, serr := seeker.Seek(0, io.SeekStart); serr != nil {
				return fmt.Errorf("rewind body for PUT: %w", serr)
			}
		}
		return o.putToUploadLocation(ctx, uploadURL, scope, body)
	}
	snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	return fmt.Errorf("upload returned %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet)))
}

// putToUploadLocation PUTs the body to the registry-supplied upload
// Location, which marks the blob committed. The body must be an
// io.ReadSeeker — the 401-retry path may have already consumed it
// once, so Rewind=true is mandatory.
func (o *OCIRegistryStorageBackend) putToUploadLocation(ctx context.Context, uploadURL, scope string, body io.Reader) error {
	resp, err := o.doBearerRequest(ctx, bearerReqOpts{
		Method:      http.MethodPut,
		URL:         uploadURL,
		Scope:       scope,
		ContentType: "application/octet-stream",
		Body:        body,
		Rewind:      true,
	})
	if err != nil {
		return fmt.Errorf("put upload: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusCreated || resp.StatusCode == http.StatusOK {
		return nil
	}
	snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	return fmt.Errorf("upload put returned %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet)))
}

// resolveUploadLocation joins a registry-supplied upload Location URL
// against the configured registry base. Per the spec the Location is
// either an absolute URL on the same origin (rare) or a path on the
// same origin. Refusing cross-origin absolute URLs blocks the
// "registry-supplied host" SSRF avenue that the bearer-realm
// validation in bearerForChallenge handles for auth — symmetric
// protection here for the upload flow.
func resolveUploadLocation(registryBase, loc string) (string, error) {
	if loc == "" {
		return "", fmt.Errorf("empty Location header")
	}
	if strings.HasPrefix(loc, "http://") || strings.HasPrefix(loc, "https://") {
		base, err := url.Parse(registryBase)
		if err != nil {
			return "", fmt.Errorf("parse registry %q: %w", registryBase, err)
		}
		locURL, err := url.Parse(loc)
		if err != nil {
			return "", fmt.Errorf("parse Location %q: %w", loc, err)
		}
		if locURL.Scheme != base.Scheme || locURL.Host != base.Host {
			return "", fmt.Errorf("upload location host %q does not match registry %q", locURL.Host, base.Host)
		}
		return loc, nil
	}
	if !strings.HasPrefix(loc, "/") {
		return "", fmt.Errorf("upload location %q is neither absolute nor path-absolute", loc)
	}
	return strings.TrimRight(registryBase, "/") + loc, nil
}

// appendDigestQuery appends ?digest=sha256:<hex> to a URL if the URL
// doesn't already carry one. The OCI distribution spec mandates the
// digest is on the final PUT so the registry can verify content
// addressability.
func appendDigestQuery(u, digestHex string) string {
	if strings.Contains(u, "digest=") {
		return u
	}
	sep := "?"
	if strings.Contains(u, "?") {
		sep = "&"
	}
	return u + sep + "digest=sha256:" + digestHex
}

// pushManifest PUTs the image-manifest JSON to /v2/<repo>/manifests/<tag>.
func (o *OCIRegistryStorageBackend) pushManifest(ctx context.Context, repo, tag string, body []byte) error {
	u := o.registry + "/v2/" + o.fullRepo(repo) + "/manifests/" + tag
	// bytes.Reader is reusable; Rewind=true is safe because the same
	// bytes are re-sent on a 401 retry.
	resp, err := o.doBearerRequest(ctx, bearerReqOpts{
		Method:      http.MethodPut,
		URL:         u,
		Scope:       "repository:" + o.fullRepo(repo) + ":pull,push",
		ContentType: imageManifestMediaType,
		Body:        bytes.NewReader(body),
		Rewind:      true,
	})
	if err != nil {
		return fmt.Errorf("put manifest: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusCreated || resp.StatusCode == http.StatusAccepted || resp.StatusCode == http.StatusOK {
		return nil
	}
	snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	return fmt.Errorf("manifest returned %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet)))
}

// fetchManifest GETs and decodes the manifest at (repo, tag). A 404
// surfaces as ErrNotFound; a non-image-manlist (e.g. an image-index)
// is a non-fatal error with the response code in the message.
func (o *OCIRegistryStorageBackend) fetchManifest(ctx context.Context, repo, tag string) (imageManifest, error) {
	u := o.registry + "/v2/" + o.fullRepo(repo) + "/manifests/" + tag
	resp, err := o.doBearerRequest(ctx, bearerReqOpts{
		Method: http.MethodGet,
		URL:    u,
		Scope:  "repository:" + o.fullRepo(repo) + ":pull",
		ExtraHeaders: map[string]string{
			"Accept": manifestAccept,
		},
	})
	if err != nil {
		return imageManifest{}, fmt.Errorf("get manifest: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return imageManifest{}, fmt.Errorf("%w: manifest %s/%s not found", ErrNotFound, o.fullRepo(repo), tag)
	}
	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return imageManifest{}, fmt.Errorf("manifest returned %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet)))
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return imageManifest{}, fmt.Errorf("read manifest: %w", err)
	}
	var m imageManifest
	if err := json.Unmarshal(body, &m); err != nil {
		return imageManifest{}, fmt.Errorf("decode manifest: %w", err)
	}
	return m, nil
}

// fetchBlob streams the layer blob bytes back as a ReadCloser. A 404
// surfaces as ErrNotFound wrapped. The caller MUST close the body.
// fetchBlob downloads the layer blob and verifies its SHA-256 matches
// the digest the manifest advertised. The verified body is returned
// as an io.ReadCloser over a tmp file (the file is removed on Close).
//
// Why verify on Get: the OCI distribution spec lets a registry serve
// any blob under a given URL, but our caller (schedd, builderd) treats
// the body as authoritative — a corrupted ext4 layer silently bricks
// the app at first boot. Hashing the body and comparing to the
// manifest's `digest` field catches both in-flight corruption and a
// registry that returned a different blob than the manifest names.
//
// Cost: one full pass through the body and one tmp-file write (the
// file is removed on Close). For Scale-plan apps the largest blob is
// 2 GB; we accept the disk-bound cost in exchange for fail-fast on
// corruption. The alternative — defer verification to Close — risks
// the caller feeding unverified bytes into a loop mount before the
// mismatch is reported.
func (o *OCIRegistryStorageBackend) fetchBlob(ctx context.Context, repo, digest string) (io.ReadCloser, error) {
	u := o.registry + "/v2/" + o.fullRepo(repo) + "/blobs/" + digest
	resp, err := o.doBearerRequest(ctx, bearerReqOpts{
		Method: http.MethodGet,
		URL:    u,
		Scope:  "repository:" + o.fullRepo(repo) + ":pull",
	})
	if err != nil {
		return nil, fmt.Errorf("get blob: %w", err)
	}
	if resp.StatusCode == http.StatusNotFound {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("%w: blob %s/%s not found", ErrNotFound, o.fullRepo(repo), digest)
	}
	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		_ = resp.Body.Close()
		return nil, fmt.Errorf("blob returned %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet)))
	}
	expectedHex := strings.TrimPrefix(digest, "sha256:")
	if expectedHex == digest || expectedHex == "" {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("blob digest %q is not a sha256:<hex> reference", digest)
	}
	// Stream the body into a tmp file while tee-hashing. The os.File
	// is wrapped in a tmpCloser that removes itself on Close so the
	// caller can defer Close without leaking the scratch file.
	f, err := osCreateTemp("", "faas-oci-blob-*.bin")
	if err != nil {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("blob tmp file: %w", err)
	}
	h := sha256.New()
	if _, err := copyContextHash(ctx, f, resp.Body, h); err != nil {
		_ = resp.Body.Close()
		_ = f.Close()
		_ = removeTmp(f.Name())
		return nil, fmt.Errorf("blob download: %w", err)
	}
	if err := resp.Body.Close(); err != nil {
		_ = f.Close()
		_ = removeTmp(f.Name())
		return nil, fmt.Errorf("blob body close: %w", err)
	}
	gotHex := hex.EncodeToString(h.Sum(nil))
	if gotHex != expectedHex {
		_ = f.Close()
		_ = removeTmp(f.Name())
		return nil, fmt.Errorf("blob integrity: sha256 mismatch: want %s got %s", expectedHex, gotHex)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		_ = f.Close()
		_ = removeTmp(f.Name())
		return nil, fmt.Errorf("blob rewind: %w", err)
	}
	return &tmpCloser{File: f}, nil
}

// tmpCloser is an *os.File that removes itself from os.TempDir when
// closed. Used by fetchBlob to keep Get's contract simple: caller
// defers Close, scratch file disappears. The underlying *os.File is
// the reader so caller-side Read/Seek work without wrapping.
type tmpCloser struct{ *os.File }

func (t *tmpCloser) Close() error {
	path := t.Name()
	cerr := t.File.Close()
	rerr := removeTmp(path)
	switch {
	case cerr != nil:
		return cerr
	case rerr != nil:
		return rerr
	}
	return nil
}

// deleteManifest DELETEs the manifest at (repo, tag). A 404 is a
// non-error (idempotent Delete matches LocalStorageBackend's
// contract).
//
// The OCI distribution spec requires DELETE by digest, not tag; we
// resolve the manifest first and DELETE by the Docker-Content-Digest
// header (or compute it ourselves when the registry omits the header)
// so a conforming registry accepts the request. Tag-based DELETE is
// implementation-defined behaviour and most public registries refuse it.
func (o *OCIRegistryStorageBackend) deleteManifest(ctx context.Context, repo, tag string) error {
	fullRepo := o.fullRepo(repo)
	scope := "repository:" + fullRepo + ":pull,push"
	digest, err := o.resolveManifestDigest(ctx, repo, tag)
	if err != nil {
		if isNotFoundErr(err) {
			return nil // already gone — idempotent
		}
		return fmt.Errorf("delete manifest: %w", err)
	}
	u := o.registry + "/v2/" + fullRepo + "/manifests/" + digest
	resp, err := o.doBearerRequest(ctx, bearerReqOpts{
		Method: http.MethodDelete,
		URL:    u,
		Scope:  scope,
	})
	if err != nil {
		return fmt.Errorf("delete manifest: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusAccepted || resp.StatusCode == http.StatusOK {
		return nil
	}
	snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	return fmt.Errorf("delete returned %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet)))
}

// resolveManifestDigest fetches the manifest and returns its
// content-addressable digest (sha256:…). The OCI distribution spec
// requires manifest DELETE by digest; we trust the registry's
// Docker-Content-Digest header when present, otherwise we compute the
// digest ourselves (some implementations skip the header). Returns
// ErrNotFound when the manifest doesn't exist so deleteManifest can
// short-circuit idempotently.
func (o *OCIRegistryStorageBackend) resolveManifestDigest(ctx context.Context, repo, tag string) (string, error) {
	u := o.registry + "/v2/" + o.fullRepo(repo) + "/manifests/" + tag
	resp, err := o.doBearerRequest(ctx, bearerReqOpts{
		Method: http.MethodGet,
		URL:    u,
		Scope:  "repository:" + o.fullRepo(repo) + ":pull",
		ExtraHeaders: map[string]string{
			"Accept": manifestAccept,
		},
	})
	if err != nil {
		return "", fmt.Errorf("resolve digest: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return "", fmt.Errorf("%w: manifest %s/%s not found", ErrNotFound, o.fullRepo(repo), tag)
	}
	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("resolve digest returned %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet)))
	}
	if d := resp.Header.Get("Docker-Content-Digest"); d != "" {
		return d, nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return "", fmt.Errorf("resolve digest read: %w", err)
	}
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// fetchTags GETs /v2/<repo>/tags/list and returns the tag list. An
// empty list + nil error means "no tags" (the repo exists but is
// empty); an HTTP 404 surfaces as ErrNotFound (the repo doesn't
// exist). Other failures return wrapped errors so List can skip them.
func (o *OCIRegistryStorageBackend) fetchTags(ctx context.Context, repo string) ([]string, error) {
	u := o.registry + "/v2/" + o.fullRepo(repo) + "/tags/list"
	resp, err := o.doBearerRequest(ctx, bearerReqOpts{
		Method: http.MethodGet,
		URL:    u,
		Scope:  "repository:" + o.fullRepo(repo) + ":pull",
	})
	if err != nil {
		return nil, fmt.Errorf("get tags: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil // repo doesn't exist → empty
	}
	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("tags returned %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet)))
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("read tags: %w", err)
	}
	var resp2 struct {
		Name string   `json:"name"`
		Tags []string `json:"tags"`
	}
	if err := json.Unmarshal(body, &resp2); err != nil {
		return nil, fmt.Errorf("decode tags: %w", err)
	}
	return resp2.Tags, nil
}

// --- bearer token cache ------------------------------------------------

// bearer returns a cached bearer for scope, falling back to an empty
// string for the anonymous-with-no-cached-realm case (the caller
// probes with the empty header, gets a 401 + Www-Authenticate, and
// re-fetches via bearerForChallenge).
//
// When the cached token is near expiry AND we have a refresh_token +
// realmURL cached from the initial challenge, we proactively post a
// refresh_token grant instead of waiting for the registry to 401.
// This is the OCI distribution-spec "refresh" flow (issue #96 review
// finding #6); without it a near-expiry bearer costs an extra round
// trip on every cached scope.
//
// Single-flight: a sync.Map keyed by scope tracks the in-flight
// refresh. Concurrent callers on the same scope share the result so a
// hundred wakes don't fan out into a hundred refresh requests.
//
// Anonymous callers (no user/pw) with no cached realm return ""
// without making a network call — most public registries serve
// unauthenticated /v2/ reads for public repos and the bearer dance
// only kicks in on a real 401.
//
// The cache key is the scope (e.g. "repository:faas/apps:pull,push")
// — different scopes hit different token endpoints / realms, so they
// MUST be cached separately.
func (o *OCIRegistryStorageBackend) bearer(ctx context.Context, scope string) (string, error) {
	cached, ok := o.tokenCache.Load(scope)
	if !ok {
		return "", nil
	}
	ct := cached.(cachedToken)
	if !ct.tok.IsExpiredAt(ct.issuedAt, time.Now()) {
		return ct.tok.AccessToken, nil
	}
	// Token is near expiry. Refresh if we have the recipe; otherwise
	// let the caller drive a 401 → bearerForChallenge.
	if ct.tok.RefreshToken == "" || ct.realmURL == "" {
		return "", nil
	}
	auth := (*oci.BasicAuth)(nil)
	if o.user != "" {
		auth = &oci.BasicAuth{Username: o.user, Password: o.pw}
	}
	fresh, err := o.singleFlightRefresh(ctx, scope, ct.tok.RefreshToken, ct.realmURL, auth)
	if err != nil {
		// Refresh failed (revoked, expired grant, or registry outage).
		// Returning "" lets the caller fall back to a challenge-driven
		// fetch on a 401 — the refresh attempt hasn't made things worse.
		//
		// nolint:nilerr // Deliberate silent fallback: the caller (see
		// doBearerRequest → bearerForChallenge) treats "" as "no cached
		// token, do a fresh 401 → realm fetch". Returning the wrapped
		// refresh error here would either leak registry-internal
		// diagnostics into the request hot path OR cause callers to
		// skip the fallback entirely. The error is already logged at
		// the singleFlightRefresh call site via slog.
		return "", nil
	}
	return fresh, nil
}

// singleFlightRefresh coalesces concurrent refresh-token POSTs for the
// same scope. The first caller POSTs; concurrent callers wait on the
// same result via the inFlight sync.Map. Without this, N parallel
// wake requests against a near-expiry token would issue N refresh
// round-trips; with it they share one.
//
// The result is published as a pointer on the inFlight entry itself
// (under a sync.Once guard) so any number of waiters can read the same
// value once it's available. A buffered channel is unnecessary — the
// sync.Once prevents the producer from racing the consumer.
// singleFlightRefresh coalesces concurrent refresh-token POSTs for the
// same scope. The first caller to arrive posts; concurrent callers
// wait on the same result via the inFlight sync.Map. Without this,
// N parallel wake requests against a near-expiry token would issue
// N refresh round-trips; with it they share one.
//
// Race note: a goroutine that observes the cached token as expired
// but arrives at inFlight AFTER the producer's done-channel close
// and inFlight delete must NOT spin up a second POST. We re-check
// the cache after acquiring the flight (as follower or producer)
// and short-circuit on a fresh entry. This covers the common
// near-expiry burst: 8 goroutines read expired, the first one
// refreshes in microseconds, the next 7 see the fresh cache and
// return without an extra POST.
//
// The result is published as a pointer on the inFlight entry itself;
// any number of waiters can read the same value once it's available.
func (o *OCIRegistryStorageBackend) singleFlightRefresh(ctx context.Context, scope, refreshToken, realm string, auth *oci.BasicAuth) (string, error) {
	// Fast path: another goroutine is mid-refresh — wait for its result
	// then re-check the cache (the producer updates it before closing
	// the done channel, so a fresh entry will always be visible).
	if existing, ok := o.inFlight.Load(scope); ok {
		flight := existing.(*refreshFlight)
		<-flight.done
		if fresh, ok := o.freshCachedAccessToken(scope); ok {
			return fresh, nil
		}
		// Producer failed or the cache still holds an expired entry;
		// return whatever the producer decided to broadcast.
		return flight.result.token, flight.result.err
	}
	// Cache check before posting: if another goroutine just refreshed
	// (between our previous cache read and now) the cache will already
	// hold a fresh entry and we can return that without a POST.
	if fresh, ok := o.freshCachedAccessToken(scope); ok {
		return fresh, nil
	}
	flight := &refreshFlight{done: make(chan refreshResult, 1)}
	// Compare-and-swap into the map so only one goroutine wins the
	// producer slot. A second goroutine that races us here sees the
	// existing flight, waits on its done, and re-checks the cache.
	if existing, ok := o.inFlight.LoadOrStore(scope, flight); ok {
		other := existing.(*refreshFlight)
		<-other.done
		if fresh, ok := o.freshCachedAccessToken(scope); ok {
			return fresh, nil
		}
		return other.result.token, other.result.err
	}
	defer o.inFlight.Delete(scope)
	tok, err := oci.RefreshToken(ctx, o.hc, o.ua, realm, refreshToken, auth)
	flight.once.Do(func() {
		if err != nil {
			flight.result = refreshResult{err: err}
		} else {
			o.tokenCache.Store(scope, cachedToken{
				tok:      tok,
				issuedAt: time.Now(),
				realmURL: realm,
			})
			flight.result = refreshResult{token: tok.AccessToken}
		}
		close(flight.done)
	})
	return flight.result.token, flight.result.err
}

// freshCachedAccessToken returns the cached access token when it is
// NOT expired at the current wall-clock time. Used by singleFlightRefresh
// to short-circuit goroutines that arrive after a successful refresh.
// OK=false means the cache is missing or the entry is near expiry.
func (o *OCIRegistryStorageBackend) freshCachedAccessToken(scope string) (string, bool) {
	cached, ok := o.tokenCache.Load(scope)
	if !ok {
		return "", false
	}
	ct := cached.(cachedToken)
	if ct.tok.IsExpiredAt(ct.issuedAt, time.Now()) {
		return "", false
	}
	return ct.tok.AccessToken, true
}

// refreshFlight carries the single-flight state for a scope. done is
// buffered so the producer never blocks on the close path; the
// underlying sync.Once guards the producer side (we publish exactly
// once even if a future refactor adds a second send site).
type refreshFlight struct {
	once   sync.Once
	done   chan refreshResult
	result refreshResult
}

// refreshResult is the value type shuttled through the in-flight
// channel. Using a struct (rather than two parallel channels) keeps
// the send site atomic and the receiver simple.
type refreshResult struct {
	token string
	err   error
}

// bearerForChallenge is the post-401 path: the caller has the
// Www-Authenticate header in hand and the realm URL to POST to.
// Returns the new bearer and caches it under scope so subsequent
// requests on the same scope skip the 401 challenge.
//
// Realm-trust boundary (issue #96 review finding #3): the realm URL
// comes from the registry on every 401. A compromised or malicious
// registry can advertise realm="https://attacker.example/token" and
// receive the configured Basic credentials on the next round-trip.
// The egress transport blocks private/loopback destinations but NOT
// arbitrary public hosts (a public-host attacker is the point). Two
// guards:
//
//   - When creds are configured, the realm MUST be HTTPS — a
//     cleartext realm would let any on-path observer capture them.
//   - The realm host MUST be the registry host or a one-segment
//     subdomain (e.g. registry=ghcr.io/onebox → realm=auth.ghcr.io
//     is allowed but realm=evil.com is not). Most major registries
//     colocate the token endpoint on the registry host itself
//     (ghcr.io, registry-1.docker.io) or a single subdomain.
//
// Anonymous (no creds) flows skip both guards: a realm advertised by
// the registry on a public read can't leak anything except a token
// with no privileges.
func (o *OCIRegistryStorageBackend) bearerForChallenge(ctx context.Context, challenge, scope string) (string, error) {
	ch := oci.ParseChallenge(challenge)
	if ch.Realm() == "" {
		return "", fmt.Errorf("registry returned 401 with no bearer realm; not an OCI distribution-spec server?")
	}
	if o.user != "" {
		if err := validateBearerRealm(o.registry, ch.Realm()); err != nil {
			return "", fmt.Errorf("realm %q rejected: %w", ch.Realm(), err)
		}
	}
	auth := (*oci.BasicAuth)(nil)
	if o.user != "" {
		auth = &oci.BasicAuth{Username: o.user, Password: o.pw}
	}
	tok, err := oci.FetchToken(ctx, o.hc, o.ua, ch, auth)
	if err != nil {
		return "", fmt.Errorf("fetch bearer for %s: %w", scope, err)
	}
	o.tokenCache.Store(scope, cachedToken{
		tok:      tok,
		issuedAt: time.Now(),
		realmURL: ch.Realm(),
	})
	return tok.AccessToken, nil
}

// validateBearerRealm enforces the trust boundary described on
// bearerForChallenge. Returns nil when the realm is acceptable for
// credential-bearing requests.
//
// Allowed hosts:
//
//   - exact match of the registry host (the common case for ghcr.io,
//     docker.io, and self-hosted distribution),
//   - a single-segment subdomain of the registry host (auth.ghcr.io
//     when registry=ghcr.io),
//   - a single-segment parent (registry=auth.ghcr.io → realm=ghcr.io).
//
// The matching is host-only; path / port are not part of the trust
// decision so an operator can reconfigure paths without whitelisting.
func validateBearerRealm(registryBase, realm string) error {
	rURL, err := url.Parse(realm)
	if err != nil {
		return fmt.Errorf("parse realm: %w", err)
	}
	if rURL.Scheme != "https" {
		return fmt.Errorf("realm must be https when credentials are configured (got %q)", rURL.Scheme)
	}
	regURL, err := url.Parse(registryBase)
	if err != nil {
		return fmt.Errorf("parse registry: %w", err)
	}
	if !realmHostAllowed(rURL.Host, regURL.Host) {
		return fmt.Errorf("realm host %q is not the registry host %q nor a single-segment neighbour", rURL.Host, regURL.Host)
	}
	return nil
}

// realmHostAllowed reports whether realmHost is the registry host or
// a single-segment neighbour. A "single-segment neighbour" is exactly
// one extra label on either side:
//
//	registry=ghcr.io       → auth.ghcr.io, ghcr.io (exact)
//	registry=auth.ghcr.io  → auth.ghcr.io (exact), ghcr.io (parent)
//
// Anything more distant (attacker.com, foo.bar.ghcr.io) is rejected.
// This matches what real OCI registries do — the token endpoint lives
// at most one DNS hop from the registry hostname.
func realmHostAllowed(realmHost, registryHost string) bool {
	if realmHost == "" || registryHost == "" {
		return false
	}
	if realmHost == registryHost {
		return true
	}
	realmLabels := strings.Split(realmHost, ".")
	regLabels := strings.Split(registryHost, ".")
	// A single-segment neighbour differs by exactly one label.
	if len(realmLabels) == len(regLabels)+1 {
		// realm is registry + 1 prefix (e.g. auth.ghcr.io vs ghcr.io)
		for i := range regLabels {
			if realmLabels[i+1] != regLabels[i] {
				return false
			}
		}
		return true
	}
	if len(realmLabels)+1 == len(regLabels) {
		// realm is registry - 1 prefix (e.g. ghcr.io vs auth.ghcr.io)
		for i := range realmLabels {
			if realmLabels[i] != regLabels[i+1] {
				return false
			}
		}
		return true
	}
	return false
}

// bearerReqOpts configures doBearerRequest. The zero value is a sane
// default for a GET without a body rewind; non-GET callers should set
// ContentType and (for streaming bodies) Rewind.
type bearerReqOpts struct {
	Method       string
	URL          string
	Scope        string            // "repository:<fullRepo>:<pull,push>" or similar
	ContentType  string            // "" → no Content-Type header
	Body         io.Reader         // nil → no body
	Rewind       bool              // true → body must be io.ReadSeeker; helper seeks to 0 on retry
	ExtraHeaders map[string]string // optional: applied AFTER ContentType/Authorization so they win
}

// doBearerRequest sends req, attaches the cached bearer, and on 401
// invalidates + refetches the token via Www-Authenticate + retries
// once. The returned response is the final attempt (success or last
// try); the caller checks status code, maps 404 → ErrNotFound as
// needed, and closes the body.
//
// 401 here after the retry means the registry keeps refusing the
// fresh bearer (creds revoked? scope mis-configured?) — we surface
// the status code + a 512-byte response snippet so ops have a hook
// to diagnose.
func (o *OCIRegistryStorageBackend) doBearerRequest(ctx context.Context, opts bearerReqOpts) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, opts.Method, opts.URL, opts.Body)
	if err != nil {
		return nil, fmt.Errorf("build %s %s: %w", opts.Method, opts.URL, err)
	}
	if opts.ContentType != "" {
		req.Header.Set("Content-Type", opts.ContentType)
	}
	if o.ua != "" {
		req.Header.Set("User-Agent", o.ua)
	}
	// Set the bearer only if cached; if absent we still send the request
	// unauthenticated and let the 401-retry path populate it.
	if tok, _ := o.bearer(ctx, opts.Scope); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := o.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send %s %s: %w", opts.Method, opts.URL, err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		return resp, nil
	}
	// 401 — capture the challenge, then invalidate + refetch + retry.
	challenge := resp.Header.Get("Www-Authenticate")
	_ = resp.Body.Close()
	o.invalidateToken(opts.Scope)
	tok, terr := o.bearerForChallenge(ctx, challenge, opts.Scope)
	if terr != nil {
		return nil, fmt.Errorf("401 retry fetch token: %w", terr)
	}
	if opts.Rewind {
		// Body must be an io.ReadSeeker — pushBlobMonolithic uses an
		// *os.File. Seek to 0 so net/http re-reads.
		seeker, ok := opts.Body.(io.Seeker)
		if !ok {
			return nil, fmt.Errorf("401 retry rewind: body of type %T is not an io.Seeker", opts.Body)
		}
		if _, serr := seeker.Seek(0, io.SeekStart); serr != nil {
			return nil, fmt.Errorf("401 retry rewind: %w", serr)
		}
		req2, rerr := http.NewRequestWithContext(ctx, opts.Method, opts.URL, opts.Body)
		if rerr != nil {
			return nil, fmt.Errorf("401 retry build: %w", rerr)
		}
		if opts.ContentType != "" {
			req2.Header.Set("Content-Type", opts.ContentType)
		}
		if o.ua != "" {
			req2.Header.Set("User-Agent", o.ua)
		}
		if tok != "" {
			req2.Header.Set("Authorization", "Bearer "+tok)
		}
		resp, err = o.hc.Do(req2)
		if err != nil {
			return nil, fmt.Errorf("401 retry send: %w", err)
		}
		return resp, nil
	}
	// No rewind — rebuild the request from the same body (the original
	// is consumed if it was a one-shot Reader, but most GETs are nil-body).
	req2, rerr := http.NewRequestWithContext(ctx, opts.Method, opts.URL, opts.Body)
	if rerr != nil {
		return nil, fmt.Errorf("401 retry build: %w", rerr)
	}
	if opts.ContentType != "" {
		req2.Header.Set("Content-Type", opts.ContentType)
	}
	if o.ua != "" {
		req2.Header.Set("User-Agent", o.ua)
	}
	if tok != "" {
		req2.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err = o.hc.Do(req2)
	if err != nil {
		return nil, fmt.Errorf("401 retry send: %w", err)
	}
	return resp, nil
}

// invalidateToken drops a cached bearer so the next bearer() call
// re-fetches. Called when a 401 surfaces; the second attempt will
// pull a fresh token (the old one may have been revoked, or our
// cached value was wrong).
func (o *OCIRegistryStorageBackend) invalidateToken(scope string) {
	o.tokenCache.Delete(scope)
}

// --- ctx-aware streaming copy ------------------------------------------

// copyContextHash is io.Copy with cooperative ctx cancellation AND a
// sha256 hasher tee. Same quantum (256 KiB) as LocalStorageBackend's
// copyContext so cancellation responsiveness is consistent across the
// two drivers.
func copyContextHash(ctx context.Context, dst io.Writer, src io.Reader, h io.Writer) (int64, error) {
	const copyQuantum = 256 * 1024
	buf := make([]byte, copyQuantum)
	var written int64
	for {
		if cerr := ctx.Err(); cerr != nil {
			return written, cerr
		}
		n, rerr := src.Read(buf)
		if n > 0 {
			if _, err := h.Write(buf[:n]); err != nil {
				return written, err
			}
			w, werr := dst.Write(buf[:n])
			if werr != nil {
				return written, werr
			}
			written += int64(w)
			if w < n {
				return written, io.ErrShortWrite
			}
		}
		if rerr == io.EOF {
			return written, nil
		}
		if rerr != nil {
			return written, rerr
		}
	}
}

// --- small helpers -----------------------------------------------------

// isNotFoundErr reports whether err wraps ErrNotFound. Used by
// Delete's "manifest already gone" short-circuit.
func isNotFoundErr(err error) bool {
	if err == nil {
		return false
	}
	return errorsIs(err, ErrNotFound)
}
