package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
		prefix:  "faas",
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

var (
	// tagCharset is the OCI distribution-spec v2 tag charset:
	// [a-zA-Z0-9_][a-zA-Z0-9._-]{0,127}. We allow 1..128 chars.
	tagCharset = regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9._-]{0,127}$`)

	// depIDCharset matches the deployment-ID format produced by
	// pkg/state (lowercase hex). snap keys embed the depID in the repo
	// name; everything else uses it in the tag.
	depIDCharset = regexp.MustCompile(`^[a-f0-9]{6,64}$`)
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
		// apps/<slug>/<dep>.ext4 → repo "apps", tag "<slug>-<dep>"
		if len(parts) != 3 || !strings.HasSuffix(parts[2], ".ext4") {
			return "", "", fmt.Errorf("%w: %q does not match apps/<slug>/<dep>.ext4", ErrInvalidKey, key)
		}
		dep := strings.TrimSuffix(parts[2], ".ext4")
		tag := parts[1] + "-" + dep
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
	// We re-open the tmp file as an io.ReadSeeker so net/http can
	// stream the body AND pushBlobMonolithic can rewind on a 401
	// retry without re-reading the whole 150 MB.
	blobRC, err := osOpen(tmpPath)
	if err != nil {
		return fmt.Errorf("storage: oci put %q: reopen blob: %w", key, err)
	}
	if err := o.pushBlobMonolithic(ctx, repo, digestHex, blobRC); err != nil {
		_ = blobRC.Close()
		return fmt.Errorf("storage: oci put %q: blob: %w", key, err)
	}
	_ = blobRC.Close()

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
// as a wrapped non-fatal error per-repo (the iteration continues).
func (o *OCIRegistryStorageBackend) List(ctx context.Context, prefix string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("storage: oci list %q: %w", prefix, err)
	}
	repos := o.reposForPrefix(prefix)
	var keys []string
	for _, repo := range repos {
		tags, err := o.fetchTags(ctx, repo)
		if err != nil {
			// A registry without /tags/list is non-fatal — log via
			// wrapping and continue. Without this a single missing
			// repo would abort the whole GC walk.
			continue
		}
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
	return keys, nil
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
// The apps tag is "<slug>-<dep>" but BOTH slug and dep are allowed to
// contain dashes (the slug regex permits dashes; the dep regex requires
// hex digits but the dep's tag is the trim of ".ext4" so it can be any
// charset-bounded string). We split on the LAST dash so multi-dash slugs
// round-trip cleanly. If a future change makes the dep ambiguous we
// need a non-conflicting separator here.
func (o *OCIRegistryStorageBackend) unplan(repo, tag string) (string, bool) {
	switch repo {
	case repoApps:
		// apps tag is "<slug>-<dep>"; split on the LAST dash.
		idx := strings.LastIndexByte(tag, '-')
		if idx < 0 {
			return "", false
		}
		return "apps/" + tag[:idx] + "/" + tag[idx+1:] + ".ext4", true
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
	"application/vnd.oci.image.manifest.v1+json",
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
		MediaType:     "application/vnd.oci.image.manifest.v1+json",
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

// pushBlobMonolithic uploads a single-shot blob via the
// POST /v2/<repo>/blobs/uploads/?digest=... monolithic PUT. The body
// is the file at path OR an io.Reader (the config-stub case uses the
// latter). On 4xx/5xx we surface the status code + a 512-byte snippet
// of the response body to make failures debuggable.
//
// The bearer-token dance is the same anonymous-or-Basic pattern as
// the puller. Caching happens in the helper below.
func (o *OCIRegistryStorageBackend) pushBlobMonolithic(ctx context.Context, repo, digestHex string, body io.Reader) error {
	// If body is a *os.File (the layer-blob path), the Reader-from-File
	// pattern lets net/http stream it; for the config-stub case body is
	// already a bytes.Reader.
	u := o.registry + "/v2/" + o.fullRepo(repo) + "/blobs/uploads/?digest=sha256:" + digestHex
	// Try POST first (some registries reject PUT on the upload URL);
	// fall back to PUT on the upload URL the response Location header
	// points at (RFC defines two-step POST-init then PUT-finalize).
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, body)
	if err != nil {
		return fmt.Errorf("build upload request: %w", err)
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	if o.ua != "" {
		req.Header.Set("User-Agent", o.ua)
	}
	if seeker, ok := body.(io.Seeker); ok {
		// Monolithic POST needs a known Content-Length; for a file
		// (most cases) we can seek to set it.
		if size, err := seeker.Seek(0, io.SeekEnd); err == nil {
			_, _ = seeker.Seek(0, io.SeekStart)
			req.ContentLength = size
		}
	}
	tok, err := o.bearer(ctx, "repository:"+o.fullRepo(repo)+":pull,push")
	if err == nil && tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := o.hc.Do(req)
	if err != nil {
		return fmt.Errorf("post upload: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusCreated {
		return nil
	}
	if resp.StatusCode == http.StatusAccepted {
		return nil
	}
	// Some registries reply 401 with a token challenge; refresh + retry once.
	if resp.StatusCode == http.StatusUnauthorized {
		o.invalidateToken("repository:" + o.fullRepo(repo) + ":pull,push")
		// Re-build the request with the fresh token.
		body2, err2 := rewindBody(body)
		if err2 != nil {
			return fmt.Errorf("401 retry rewind: %w", err2)
		}
		req2, err2 := http.NewRequestWithContext(ctx, http.MethodPost, u, body2)
		if err2 != nil {
			return fmt.Errorf("401 retry build: %w", err2)
		}
		req2.Header.Set("Content-Type", "application/octet-stream")
		if o.ua != "" {
			req2.Header.Set("User-Agent", o.ua)
		}
		tok2, err2 := o.bearerForChallenge(ctx, resp.Header.Get("Www-Authenticate"),
			"repository:"+o.fullRepo(repo)+":pull,push")
		if err2 != nil {
			return fmt.Errorf("401 retry fetch token: %w", err2)
		}
		if tok2 != "" {
			req2.Header.Set("Authorization", "Bearer "+tok2)
		}
		resp2, err2 := o.hc.Do(req2)
		if err2 != nil {
			return fmt.Errorf("401 retry do: %w", err2)
		}
		defer func() { _ = resp2.Body.Close() }()
		if resp2.StatusCode == http.StatusCreated || resp2.StatusCode == http.StatusAccepted {
			return nil
		}
		snippet, _ := io.ReadAll(io.LimitReader(resp2.Body, 512))
		return fmt.Errorf("upload returned %d: %s", resp2.StatusCode, strings.TrimSpace(string(snippet)))
	}
	snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	return fmt.Errorf("upload returned %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet)))
}

// pushManifest PUTs the image-manifest JSON to /v2/<repo>/manifests/<tag>.
func (o *OCIRegistryStorageBackend) pushManifest(ctx context.Context, repo, tag string, body []byte) error {
	u := o.registry + "/v2/" + o.fullRepo(repo) + "/manifests/" + tag
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build manifest request: %w", err)
	}
	req.Header.Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
	if o.ua != "" {
		req.Header.Set("User-Agent", o.ua)
	}
	tok, terr := o.bearer(ctx, "repository:"+o.fullRepo(repo)+":pull,push")
	if terr == nil && tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := o.hc.Do(req)
	if err != nil {
		return fmt.Errorf("put manifest: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusCreated || resp.StatusCode == http.StatusAccepted || resp.StatusCode == http.StatusOK {
		return nil
	}
	if resp.StatusCode == http.StatusUnauthorized {
		// One retry with a freshly-refreshed token.
		o.invalidateToken("repository:" + o.fullRepo(repo) + ":pull,push")
		tok2, terr2 := o.bearerForChallenge(ctx, resp.Header.Get("Www-Authenticate"),
			"repository:"+o.fullRepo(repo)+":pull,push")
		if terr2 != nil {
			return fmt.Errorf("manifest 401 fetch token: %w", terr2)
		}
		if tok2 != "" {
			req.Header.Set("Authorization", "Bearer "+tok2)
		}
		resp2, err2 := o.hc.Do(req)
		if err2 != nil {
			return fmt.Errorf("manifest 401 retry: %w", err2)
		}
		defer func() { _ = resp2.Body.Close() }()
		if resp2.StatusCode == http.StatusCreated || resp2.StatusCode == http.StatusAccepted || resp2.StatusCode == http.StatusOK {
			return nil
		}
		snippet, _ := io.ReadAll(io.LimitReader(resp2.Body, 512))
		return fmt.Errorf("manifest returned %d: %s", resp2.StatusCode, strings.TrimSpace(string(snippet)))
	}
	snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	return fmt.Errorf("manifest returned %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet)))
}

// fetchManifest GETs and decodes the manifest at (repo, tag). A 404
// surfaces as ErrNotFound; a non-image-manlist (e.g. an image-index)
// is a non-fatal error with the response code in the message.
func (o *OCIRegistryStorageBackend) fetchManifest(ctx context.Context, repo, tag string) (imageManifest, error) {
	u := o.registry + "/v2/" + o.fullRepo(repo) + "/manifests/" + tag
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return imageManifest{}, fmt.Errorf("build manifest request: %w", err)
	}
	req.Header.Set("Accept", manifestAccept)
	if o.ua != "" {
		req.Header.Set("User-Agent", o.ua)
	}
	tok, terr := o.bearer(ctx, "repository:"+o.fullRepo(repo)+":pull")
	if terr == nil && tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := o.hc.Do(req)
	if err != nil {
		return imageManifest{}, fmt.Errorf("get manifest: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return imageManifest{}, fmt.Errorf("%w: manifest %s/%s not found", ErrNotFound, o.fullRepo(repo), tag)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		o.invalidateToken("repository:" + o.fullRepo(repo) + ":pull")
		tok2, terr2 := o.bearerForChallenge(ctx, resp.Header.Get("Www-Authenticate"),
			"repository:"+o.fullRepo(repo)+":pull")
		if terr2 != nil {
			return imageManifest{}, fmt.Errorf("manifest 401 fetch token: %w", terr2)
		}
		if tok2 != "" {
			req.Header.Set("Authorization", "Bearer "+tok2)
		}
		resp2, err2 := o.hc.Do(req)
		if err2 != nil {
			return imageManifest{}, fmt.Errorf("401 retry: %w", err2)
		}
		defer func() { _ = resp2.Body.Close() }()
		if resp2.StatusCode == http.StatusNotFound {
			return imageManifest{}, fmt.Errorf("%w: manifest %s/%s not found", ErrNotFound, o.fullRepo(repo), tag)
		}
		resp = resp2
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
func (o *OCIRegistryStorageBackend) fetchBlob(ctx context.Context, repo, digest string) (io.ReadCloser, error) {
	u := o.registry + "/v2/" + o.fullRepo(repo) + "/blobs/" + digest
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("build blob request: %w", err)
	}
	if o.ua != "" {
		req.Header.Set("User-Agent", o.ua)
	}
	tok, terr := o.bearer(ctx, "repository:"+o.fullRepo(repo)+":pull")
	if terr == nil && tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := o.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("get blob: %w", err)
	}
	if resp.StatusCode == http.StatusNotFound {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("%w: blob %s/%s not found", ErrNotFound, o.fullRepo(repo), digest)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		_ = resp.Body.Close()
		o.invalidateToken("repository:" + o.fullRepo(repo) + ":pull")
		tok2, terr2 := o.bearerForChallenge(ctx, resp.Header.Get("Www-Authenticate"),
			"repository:"+o.fullRepo(repo)+":pull")
		if terr2 != nil {
			return nil, fmt.Errorf("blob 401 fetch token: %w", terr2)
		}
		if tok2 != "" {
			req.Header.Set("Authorization", "Bearer "+tok2)
		}
		resp2, err2 := o.hc.Do(req)
		if err2 != nil {
			return nil, fmt.Errorf("401 retry: %w", err2)
		}
		if resp2.StatusCode == http.StatusNotFound {
			_ = resp2.Body.Close()
			return nil, fmt.Errorf("%w: blob %s/%s not found", ErrNotFound, o.fullRepo(repo), digest)
		}
		if resp2.StatusCode != http.StatusOK {
			snippet, _ := io.ReadAll(io.LimitReader(resp2.Body, 512))
			_ = resp2.Body.Close()
			return nil, fmt.Errorf("blob returned %d: %s", resp2.StatusCode, strings.TrimSpace(string(snippet)))
		}
		return resp2.Body, nil
	}
	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		_ = resp.Body.Close()
		return nil, fmt.Errorf("blob returned %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet)))
	}
	return resp.Body, nil
}

// deleteManifest DELETEs the manifest at (repo, tag). A 404 is a
// non-error (idempotent Delete matches LocalStorageBackend's
// contract).
func (o *OCIRegistryStorageBackend) deleteManifest(ctx context.Context, repo, tag string) error {
	u := o.registry + "/v2/" + o.fullRepo(repo) + "/manifests/" + tag
	scope := "repository:" + o.fullRepo(repo) + ":pull,push"
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, u, nil)
	if err != nil {
		return fmt.Errorf("build delete request: %w", err)
	}
	if o.ua != "" {
		req.Header.Set("User-Agent", o.ua)
	}
	tok, terr := o.bearer(ctx, scope)
	if terr == nil && tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := o.hc.Do(req)
	if err != nil {
		return fmt.Errorf("delete manifest: %w", err)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		_ = resp.Body.Close()
		o.invalidateToken(scope)
		tok2, terr2 := o.bearerForChallenge(ctx, resp.Header.Get("Www-Authenticate"), scope)
		if terr2 != nil {
			return fmt.Errorf("delete 401 fetch token: %w", terr2)
		}
		if tok2 != "" {
			req.Header.Set("Authorization", "Bearer "+tok2)
		}
		resp, err = o.hc.Do(req)
		if err != nil {
			return fmt.Errorf("delete 401 retry: %w", err)
		}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusAccepted || resp.StatusCode == http.StatusOK {
		return nil
	}
	snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	return fmt.Errorf("delete returned %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet)))
}

// fetchTags GETs /v2/<repo>/tags/list and returns the tag list. An
// empty list + nil error means "no tags" (the repo exists but is
// empty); an HTTP 404 surfaces as ErrNotFound (the repo doesn't
// exist). Other failures return wrapped errors so List can skip them.
func (o *OCIRegistryStorageBackend) fetchTags(ctx context.Context, repo string) ([]string, error) {
	u := o.registry + "/v2/" + o.fullRepo(repo) + "/tags/list"
	scope := "repository:" + o.fullRepo(repo) + ":pull"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("build tags request: %w", err)
	}
	if o.ua != "" {
		req.Header.Set("User-Agent", o.ua)
	}
	tok, terr := o.bearer(ctx, scope)
	if terr == nil && tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := o.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("get tags: %w", err)
	}
	if resp.StatusCode == http.StatusNotFound {
		_ = resp.Body.Close()
		return nil, nil // repo doesn't exist → empty
	}
	if resp.StatusCode == http.StatusUnauthorized {
		_ = resp.Body.Close()
		o.invalidateToken(scope)
		tok2, terr2 := o.bearerForChallenge(ctx, resp.Header.Get("Www-Authenticate"), scope)
		if terr2 != nil {
			return nil, fmt.Errorf("tags 401 fetch token: %w", terr2)
		}
		if tok2 != "" {
			req.Header.Set("Authorization", "Bearer "+tok2)
		}
		resp, err = o.hc.Do(req)
		if err != nil {
			return nil, fmt.Errorf("tags 401 retry: %w", err)
		}
	}
	defer func() { _ = resp.Body.Close() }()
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
// Anonymous callers (no user/pw) with no cached realm return ""
// without making a network call — most public registries serve
// unauthenticated /v2/ reads for public repos and the bearer dance
// only kicks in on a real 401.
//
// The cache key is the scope (e.g. "repository:faas/apps:pull,push")
// — different scopes hit different token endpoints / realms, so they
// MUST be cached separately.
func (o *OCIRegistryStorageBackend) bearer(ctx context.Context, scope string) (string, error) {
	if cached, ok := o.tokenCache.Load(scope); ok {
		ct := cached.(cachedToken)
		if !ct.tok.IsExpiredAt(ct.issuedAt, time.Now()) {
			return ct.tok.AccessToken, nil
		}
	}
	return "", nil
}

// bearerForChallenge is the post-401 path: the caller has the
// Www-Authenticate header in hand and the realm URL to POST to.
// Returns the new bearer and caches it under scope so subsequent
// requests on the same scope skip the 401 challenge.
func (o *OCIRegistryStorageBackend) bearerForChallenge(ctx context.Context, challenge, scope string) (string, error) {
	ch := oci.ParseChallenge(challenge)
	if ch.Realm() == "" {
		return "", fmt.Errorf("registry returned 401 with no bearer realm; not an OCI distribution-spec server?")
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

// rewindBody returns a fresh reader over the same bytes. For *os.File
// (the layer-blob case) it seeks to 0; for io.ReadSeeker in general it
// calls Seek(0, 0). Used by the 401-retry path which has to re-send the
// body.
func rewindBody(r io.Reader) (io.Reader, error) {
	switch v := r.(type) {
	case io.ReadSeeker:
		if _, err := v.Seek(0, io.SeekStart); err != nil {
			return nil, err
		}
		return v, nil
	default:
		return nil, fmt.Errorf("cannot rewind body of type %T", r)
	}
}
