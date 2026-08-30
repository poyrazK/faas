package oci

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/wire"
)

// RegistryClient is a minimal OCI/Docker registry v2 client. It resolves a
// (possibly tag-only) reference to its content-addressable digest via the
// manifest endpoint, performing the anonymous Bearer-token dance public
// registries (Docker Hub, ghcr.io) require. It implements Puller.
//
// Scope (M6 groundwork): reference → digest resolution. Layer/config blob
// streaming for the app-layer build lands in a follow-up. Egress hardening
// (deny RFC1918 / metadata ranges, spec §11) is applied by injecting a
// policy-aware *http.Client via WithHTTPClient — this type does not itself
// enforce it.
//
// ADR-140: when the manifest body is an OCI image-index / Docker
// manifest-list, fetchManifestWithAuth walks Manifests[] and selects
// the descriptor whose Platform matches `matcher` (defaults to
// runtime.GOARCH via WithPlatformMatcher). The matcher field is the
// only M-3 surface on RegistryClient — the Puller / AuthPuller /
// ManifestPuller / AuthManifestPuller interface seams are unchanged.
type RegistryClient struct {
	hc      *http.Client
	scheme  string // "https" in production; the test seam sets "http"
	host    string // "" = derive from the reference; tests pin an httptest host
	ua      string
	matcher PlatformMatcher // ADR-140: nil → DefaultPlatformMatcherFromGOARCH()
}

// compile-time assertion the client satisfies the puller seam imaged consumes.
var (
	_ Puller         = (*RegistryClient)(nil)
	_ ManifestPuller = (*RegistryClient)(nil)
)

// Option configures a RegistryClient.
type Option func(*RegistryClient)

// WithHTTPClient injects the HTTP client (timeouts, egress-policy transport).
func WithHTTPClient(hc *http.Client) Option {
	return func(c *RegistryClient) {
		if hc != nil {
			c.hc = hc
		}
	}
}

// WithTimeout overrides the per-request HTTP timeout. The default is
// api.OCIPullTimeoutSeconds (60s, ADR-021).
//
// Composition with WithHTTPClient is asymmetric on purpose: WithHTTPClient
// replaces the underlying *http.Client outright (including its Timeout
// field), and WithTimeout writes back into c.hc.Timeout. The ordering
// that produces a meaningful timeout+custom-transport result is therefore
//
//	NewRegistryClient(WithHTTPClient(myHC), WithTimeout(d))   // → myHC.Timeout == d
//
// If you reverse the order (WithTimeout first, then WithHTTPClient) the
// transport's own zero Timeout wins and the deadline is lost. Pass
// WithTimeout last whenever you also pass WithHTTPClient. Callers that
// only need a deadline (no custom transport) can pass WithTimeout alone.
func WithTimeout(d time.Duration) Option {
	return func(c *RegistryClient) {
		if d <= 0 {
			return
		}
		c.hc.Timeout = d
	}
}

// WithEndpoint pins the scheme and API host for every request, bypassing the
// per-reference host derivation. Used by tests to point at an httptest server;
// not for production use.
func WithEndpoint(scheme, host string) Option {
	return func(c *RegistryClient) {
		c.scheme = scheme
		c.host = host
	}
}

// WithPlatformMatcher injects the host-arch selector used when
// fetchManifestWithAuth walks an OCI image-index / Docker
// manifest-list body (ADR-140 §Decision 1). A nil matcher falls
// back to DefaultPlatformMatcherFromGOARCH() at walk time. The
// Puller / AuthPuller / ManifestPuller / AuthManifestPuller
// interfaces gain NO new methods (ADR-140 §Decision 1: interface
// seams stable); construction-time injection is the single surface.
func WithPlatformMatcher(m PlatformMatcher) Option {
	return func(c *RegistryClient) {
		c.matcher = m
	}
}

// platformMatcher returns the matcher configured via
// WithPlatformMatcher, or DefaultPlatformMatcherFromGOARCH() when
// none was injected. Used by fetchManifestWithAuth at walk time.
func (c *RegistryClient) platformMatcher() PlatformMatcher {
	if c.matcher != nil {
		return c.matcher
	}
	return DefaultPlatformMatcherFromGOARCH()
}

// NewRegistryClient builds a client with sensible defaults (HTTPS,
// api.OCIPullTimeoutSeconds timeout — currently 60s). Tests that need a
// shorter deadline can pass WithTimeout; production passes
// WithHTTPClient(NewEgressHTTPClient()) for the §11 egress guard.
func NewRegistryClient(opts ...Option) *RegistryClient {
	c := &RegistryClient{
		hc:     &http.Client{Timeout: time.Duration(api.OCIPullTimeoutSeconds) * time.Second},
		scheme: "https",
		ua:     "faas-imaged/1 (+https://" + wire.PlatformHost + ")",
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// manifestAccept lists the manifest media types we can resolve a digest from.
var manifestAccept = strings.Join([]string{
	"application/vnd.oci.image.manifest.v1+json",
	"application/vnd.docker.distribution.manifest.v2+json",
	"application/vnd.oci.image.index.v1+json",
	"application/vnd.docker.distribution.manifest.list.v2+json",
}, ", ")

// (ADR-140: imageManifestMediaTypes removed; the per-arch walker in
// fetchManifestWithAuth / walkIndexToAuth uses indexMediaTypes
// (in platform.go) plus the platform matcher to resolve multi-arch
// indexes to a single-arch per-platform manifest.)

// PullDigest resolves ref to its canonical digest. A digest-pinned reference is
// confirmed to exist; a tag reference is resolved to the digest the registry
// currently serves. Satisfies Puller.
func (c *RegistryClient) PullDigest(ctx context.Context, ref string) (string, error) {
	r, err := ParseReference(ref)
	if err != nil {
		return "", err
	}
	return c.resolveDigest(ctx, r)
}

// PullDigestWithAuth is the AuthPuller variant (issue #461 / ADR-062).
// Pass `auth == nil` for the anonymous path.
func (c *RegistryClient) PullDigestWithAuth(ctx context.Context, ref string, auth *BasicAuth) (string, error) {
	r, err := ParseReference(ref)
	if err != nil {
		return "", err
	}
	return c.resolveDigestWithAuth(ctx, r, auth)
}

// imageManifest is the subset of an OCI / Docker image manifest we consume.
// We accept both v1 (OCI) and v2 (Docker) shapes; their JSON differs only in
// naming. References and configs are content-addressable by digest.
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

// PullImageConfig fetches only the manifest + image-config blob (no layer
// streaming) for a digest-pinned reference and returns the parsed
// ImageConfig. This is the cheap fail-fast path: imaged calls it BEFORE
// PullLayers so a manifest that cannot become a valid AppManifest (e.g. no
// Cmd) is rejected without burning bandwidth on every layer blob (review
// issue #6: a no-Cmd image was previously fetching dozens of MB before
// imaged's manifest validation even ran).
func (c *RegistryClient) PullImageConfig(ctx context.Context, ref string) (ImageConfig, error) {
	r, err := ParseReference(ref)
	if err != nil {
		return ImageConfig{}, err
	}
	m, _, err := c.fetchManifest(ctx, r)
	if err != nil {
		return ImageConfig{}, err
	}
	if m.Config.Digest == "" {
		return ImageConfig{}, fmt.Errorf("oci: %s manifest has no config descriptor", r.String())
	}
	cfgBytes, err := c.fetchBlob(ctx, r, m.Config.Digest)
	if err != nil {
		return ImageConfig{}, fmt.Errorf("oci: fetch image config %s: %w", r.String(), err)
	}
	cfg, err := parseImageConfig(cfgBytes)
	if err != nil {
		return ImageConfig{}, fmt.Errorf("oci: parse image config %s: %w", r.String(), err)
	}
	return cfg, nil
}

// PullImageConfigWithAuth is the AuthPuller variant (issue #461 /
// ADR-062). Pass `auth == nil` for the anonymous path.
func (c *RegistryClient) PullImageConfigWithAuth(ctx context.Context, ref string, auth *BasicAuth) (ImageConfig, error) {
	r, err := ParseReference(ref)
	if err != nil {
		return ImageConfig{}, err
	}
	m, _, err := c.fetchManifestWithAuth(ctx, r, auth)
	if err != nil {
		return ImageConfig{}, err
	}
	if m.Config.Digest == "" {
		return ImageConfig{}, fmt.Errorf("oci: %s manifest has no config descriptor", r.String())
	}
	cfgBytes, err := c.fetchBlobWithAuth(ctx, r, m.Config.Digest, auth)
	if err != nil {
		return ImageConfig{}, fmt.Errorf("oci: fetch image config %s: %w", r.String(), err)
	}
	cfg, err := parseImageConfig(cfgBytes)
	if err != nil {
		return ImageConfig{}, fmt.Errorf("oci: parse image config %s: %w", r.String(), err)
	}
	return cfg, nil
}

// PullLayers fetches the manifest, image-config blob, and every layer blob
// for a digest-pinned reference, returning them as gzip-compressed ReadClosers
// (bottom-to-top, the order rootfs.Builder expects). The caller closes each
// ReadCloser individually; an error return does NOT require closing them
// (the registry connections are already cleaned up by then).
//
// Layer blobs are streamed, not buffered — large app layers never fit in
// memory, and the build pipeline applies each layer directly into a staging
// tree as it arrives.
//
// Note: imaged calls PullImageConfig first (cheap, fail-fast validation
// before any layer blob fetches), then PullLayers. The result.Config is the
// same parsed ImageConfig; the second config-blob GET is bounded (≤1 MiB)
// and pays for a stable, self-contained PullLayers interface that doesn't
// require the caller to thread an image config through.
func (c *RegistryClient) PullLayers(ctx context.Context, ref string) (PullLayersResult, error) {
	r, err := ParseReference(ref)
	if err != nil {
		return PullLayersResult{}, err
	}
	m, manifestBytes, err := c.fetchManifest(ctx, r)
	if err != nil {
		return PullLayersResult{}, err
	}
	if m.Config.Digest == "" {
		return PullLayersResult{}, fmt.Errorf("oci: %s manifest has no config descriptor", r.String())
	}
	cfgBytes, err := c.fetchBlob(ctx, r, m.Config.Digest)
	if err != nil {
		return PullLayersResult{}, fmt.Errorf("oci: fetch image config %s: %w", r.String(), err)
	}
	cfg, err := parseImageConfig(cfgBytes)
	if err != nil {
		return PullLayersResult{}, fmt.Errorf("oci: parse image config %s: %w", r.String(), err)
	}

	// Open each layer as a streaming ReadCloser. We do NOT eagerly read.
	layers := make([]io.ReadCloser, 0, len(m.Layers))
	for i, layer := range m.Layers {
		rc, err := c.fetchBlobStream(ctx, r, layer.Digest)
		if err != nil {
			// Close any we already opened so a partial result doesn't leak.
			for _, l := range layers {
				_ = l.Close()
			}
			return PullLayersResult{}, fmt.Errorf("oci: fetch layer %d (%s) of %s: %w", i, layer.Digest, r.String(), err)
		}
		layers = append(layers, rc)
	}

	// The manifest digest is sha256(content) of the manifest body bytes — not
	// the layer blobs, which would be wildly different sizes per arch.
	sum := sha256.Sum256(manifestBytes)
	digest := digestAlgo + hex.EncodeToString(sum[:])

	return PullLayersResult{Layers: layers, Config: cfg, Digest: digest}, nil
}

// PullLayersWithAuth is the AuthPuller variant (issue #461 / ADR-062).
// Pass `auth == nil` for the anonymous path.
func (c *RegistryClient) PullLayersWithAuth(ctx context.Context, ref string, auth *BasicAuth) (PullLayersResult, error) {
	r, err := ParseReference(ref)
	if err != nil {
		return PullLayersResult{}, err
	}
	m, manifestBytes, err := c.fetchManifestWithAuth(ctx, r, auth)
	if err != nil {
		return PullLayersResult{}, err
	}
	if m.Config.Digest == "" {
		return PullLayersResult{}, fmt.Errorf("oci: %s manifest has no config descriptor", r.String())
	}
	cfgBytes, err := c.fetchBlobWithAuth(ctx, r, m.Config.Digest, auth)
	if err != nil {
		return PullLayersResult{}, fmt.Errorf("oci: fetch image config %s: %w", r.String(), err)
	}
	cfg, err := parseImageConfig(cfgBytes)
	if err != nil {
		return PullLayersResult{}, fmt.Errorf("oci: parse image config %s: %w", r.String(), err)
	}

	// Open each layer as a streaming ReadCloser. We do NOT eagerly read.
	layers := make([]io.ReadCloser, 0, len(m.Layers))
	for i, layer := range m.Layers {
		rc, err := c.fetchBlobStreamWithAuth(ctx, r, layer.Digest, auth)
		if err != nil {
			// Close any we already opened so a partial result doesn't leak.
			for _, l := range layers {
				_ = l.Close()
			}
			return PullLayersResult{}, fmt.Errorf("oci: fetch layer %d (%s) of %s: %w", i, layer.Digest, r.String(), err)
		}
		layers = append(layers, rc)
	}

	// The manifest digest is sha256(content) of the manifest body bytes — not
	// the layer blobs, which would be wildly different sizes per arch.
	sum := sha256.Sum256(manifestBytes)
	digest := digestAlgo + hex.EncodeToString(sum[:])

	return PullLayersResult{Layers: layers, Config: cfg, Digest: digest}, nil
}

// fetchManifest performs the authenticated GET on a manifest URL and parses
// it. Returns (imageManifest, raw manifest body bytes, err). Shared by
// PullImageConfig (cheap path) and PullLayers (full path), so the two can't
// drift in manifest-acceptance rules.
func (c *RegistryClient) fetchManifest(ctx context.Context, r Reference) (imageManifest, []byte, error) {
	return c.fetchManifestWithAuth(ctx, r, nil)
}

// fetchManifestWithAuth is the AuthPuller variant of fetchManifest
// (issue #461 / ADR-062). The `auth` value is forwarded to the
// realm endpoint on a 401 challenge.
//
// ADR-140: when the manifest body is an OCI image-index / Docker
// manifest-list (Content-Type matches one of indexMediaTypes), the
// walker selects the first IndexEntry whose Platform matches the
// configured PlatformMatcher (default DefaultPlatformMatcherFromGOARCH).
// The selected descriptor's Digest is fetched as the per-platform
// manifest. The walk is bounded to 2 hops — a manifest list whose
// selected entry is itself a list returns ErrImageManifestInvalid.
func (c *RegistryClient) fetchManifestWithAuth(ctx context.Context, r Reference, auth *BasicAuth) (imageManifest, []byte, error) {
	var empty imageManifest
	manifestURL := c.baseURL(r) + "/v2/" + r.Repository + "/manifests/" + r.ManifestRef()
	body, ct, err := c.fetchManifestJSONWithAuth(ctx, manifestURL, auth)
	if err != nil {
		return empty, nil, err
	}
	if isIndexContentType(ct) {
		body, err = c.walkIndexToAuth(ctx, r, body, auth, 1)
		if err != nil {
			return empty, nil, err
		}
		// After the walk, body is a per-platform single-arch manifest;
		// its Content-Type is now application/vnd.oci.image.manifest.v1+json
		// (or the docker equivalent), which we treat as a flat imageManifest.
		ct = ""
	}
	var m imageManifest
	if err := json.Unmarshal(body, &m); err != nil {
		return empty, nil, fmt.Errorf("oci: decode manifest %s: %w", r.String(), err)
	}
	if m.Config.Digest == "" {
		return empty, nil, fmt.Errorf("oci: %s manifest has no config descriptor", r.String())
	}
	if len(m.Layers) == 0 {
		return empty, nil, fmt.Errorf("oci: %s manifest has no layers", r.String())
	}
	if err := validateDigest(m.Config.Digest); err != nil {
		return empty, nil, fmt.Errorf("%w: %s config: %s", ErrImageManifestInvalid, r.String(), err.Error())
	}
	for i, l := range m.Layers {
		if err := validateDigest(l.Digest); err != nil {
			return empty, nil, fmt.Errorf("%w: %s layer %d: %s", ErrImageManifestInvalid, r.String(), i, err.Error())
		}
	}
	return m, body, nil
}

// walkIndexToAuth resolves an OCI image-index body to its per-arch
// child manifest body, then returns the child body bytes. depth is
// the current hop count (1 for the top-level index, 2 for the
// selected per-arch descriptor if it itself is an index — which is
// rejected). Returns ErrImageManifestInvalid when no Manifests[]
// entry matches the configured PlatformMatcher, or when the
// selected entry is itself an index (depth>1).
func (c *RegistryClient) walkIndexToAuth(ctx context.Context, r Reference, indexBody []byte, auth *BasicAuth, depth int) ([]byte, error) {
	var idx Index
	if err := json.Unmarshal(indexBody, &idx); err != nil {
		return nil, fmt.Errorf("%w: decode index %s: %s", ErrImageManifestInvalid, r.String(), err.Error())
	}
	if len(idx.Manifests) == 0 {
		return nil, fmt.Errorf("%w: %s is an empty image index", ErrImageManifestInvalid, r.String())
	}
	matcher := c.platformMatcher()
	var selected *IndexEntry
	for i := range idx.Manifests {
		entry := &idx.Manifests[i]
		if matcher(entry.Platform) {
			selected = entry
			break
		}
	}
	if selected == nil {
		return nil, fmt.Errorf("%w: %s index has no descriptor matching the host platform (set FAAS_BUILDER_ARCH=amd64 or FAAS_BUILDER_ARCH=arm64 and check the image publishes the requested arch)",
			ErrImageManifestInvalid, r.String())
	}
	if depth >= 2 {
		return nil, fmt.Errorf("%w: %s index descriptor %s resolves to another index (depth>1 not supported)",
			ErrImageManifestInvalid, r.String(), selected.Digest)
	}
	// Fetch the per-arch descriptor as a fresh manifest. Re-use
	// fetchManifestJSONWithAuth so the bearer-token dance on 401 is
	// the same as for top-level fetches; the URL is the same
	// /v2/<repo>/manifests/<digest> form because the descriptor's
	// digest IS its own manifest blob.
	childURL := c.baseURL(r) + "/v2/" + r.Repository + "/manifests/" + selected.Digest
	childBody, childCT, err := c.fetchManifestJSONWithAuth(ctx, childURL, auth)
	if err != nil {
		return nil, fmt.Errorf("%w: fetch index child %s (%s): %s",
			ErrImageManifestInvalid, r.String(), selected.Digest, err.Error())
	}
	if isIndexContentType(childCT) {
		return c.walkIndexToAuth(ctx, r, childBody, auth, depth+1)
	}
	return childBody, nil
}

// isIndexContentType reports whether the manifest Content-Type is
// one of the OCI image-index / Docker manifest-list media types.
// Used by fetchManifestWithAuth and walkIndexToAuth to decide
// whether to enter the walker.
func isIndexContentType(contentType string) bool {
	for _, mt := range indexMediaTypes {
		if contentType == mt {
			return true
		}
	}
	return false
}

// parseImageConfig decodes the OCI/Docker image config blob and projects
// onto the consumer-facing ImageConfig.
//
// Behaviour (ADR-136 §Decision 1-2):
//   - Both flat (Docker v2) and nested-`config` (OCI image-config)
//     envelopes are accepted; flat wins when both are present.
//   - Env flattening uses envSliceToMap (image.go), which preserves
//     `=VALUE`-style keys and treats bare entries as key="" — fixing
//     the byte-walk that previously dropped them silently.
//
// Unrecognised fields are ignored — the schema is large and we want to
// be resilient to additions upstream. New OCI fields are added to
// rawConfig in oci.go, not here — single decoder per ADR-136.
func parseImageConfig(b []byte) (ImageConfig, error) {
	raw, err := decodeRaw(bytes.NewReader(b))
	if err != nil {
		return ImageConfig{}, err
	}
	// Validate rootfs.type — both parsers must reject unsupported rootfs.
	// (parseImageConfig doesn't surface DiffIDs today, but it shares the
	// decoder; an image config that ParseConfig rejects here is rejected
	// here too.)
	if err := raw.validate(); err != nil {
		return ImageConfig{}, err
	}
	f := raw.resolved()
	var exposed map[string]struct{}
	if raw.Config != nil {
		exposed = raw.Config.ExposedPorts
	}
	return ImageConfig{
		Entrypoint:       f.Entrypoint,
		Cmd:              f.Cmd,
		Env:              envSliceToMap(f.Env),
		WorkingDir:       f.WorkingDir,
		User:             f.User,
		Healthcheck:      healthcheckFromRaw(raw.resolvedHealthcheck()),
		StopSignal:       raw.resolvedStopSignal(),
		StopGracePeriodS: stopGraceFromRaw(raw),
		ExposedPorts:     exposed,
	}, nil
}

// fetchManifestJSONWithAuth is the AuthPuller variant (issue #461 /
// ADR-062). `auth == nil` is the anonymous path; a non-nil auth is
// forwarded to the realm endpoint on a 401 challenge. Existing
// callers that don't thread auth continue to work via the
// nil-delegating wrapper above.
func (c *RegistryClient) fetchManifestJSONWithAuth(ctx context.Context, url string, auth *BasicAuth) ([]byte, string, error) {
	resp, err := c.getManifest(ctx, url, "")
	if err != nil {
		return nil, "", err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		ch := parseChallenge(resp.Header.Get("Www-Authenticate"))
		_ = resp.Body.Close()
		token, err := c.fetchToken(ctx, ch, auth)
		if err != nil {
			return nil, "", err
		}
		resp, err = c.getManifest(ctx, url, token)
		if err != nil {
			return nil, "", err
		}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, "", fmt.Errorf("oci: manifest returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, "", fmt.Errorf("oci: read manifest: %w", err)
	}
	return body, resp.Header.Get("Content-Type"), nil
}

// fetchBlob fetches a blob by digest and returns its full body. Used for the
// small image-config blob (manifests cap at 8 MiB; configs are usually < 64 KiB).
func (c *RegistryClient) fetchBlob(ctx context.Context, r Reference, digest string) ([]byte, error) {
	return c.fetchBlobWithAuth(ctx, r, digest, nil)
}

// fetchBlobWithAuth is the AuthPuller variant of fetchBlob.
func (c *RegistryClient) fetchBlobWithAuth(ctx context.Context, r Reference, digest string, auth *BasicAuth) ([]byte, error) {
	_, rc, err := c.openBlobWithAuth(ctx, r, digest, auth)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	return io.ReadAll(io.LimitReader(rc, 1<<20)) // 1 MiB cap — config blobs are tiny
}

// fetchBlobStream opens a blob as a streaming ReadCloser. The caller is
// responsible for closing it; the body is not buffered.
func (c *RegistryClient) fetchBlobStream(ctx context.Context, r Reference, digest string) (io.ReadCloser, error) {
	return c.fetchBlobStreamWithAuth(ctx, r, digest, nil)
}

// fetchBlobStreamWithAuth is the AuthPuller variant of fetchBlobStream.
func (c *RegistryClient) fetchBlobStreamWithAuth(ctx context.Context, r Reference, digest string, auth *BasicAuth) (io.ReadCloser, error) {
	_, body, err := c.openBlobWithAuth(ctx, r, digest, auth)
	if err != nil {
		return nil, err
	}
	return body, nil
}

// openBlobWithAuth is the AuthPuller variant. The `auth` value is
// forwarded to the realm endpoint on a 401 challenge (issue #461).
func (c *RegistryClient) openBlobWithAuth(ctx context.Context, r Reference, digest string, auth *BasicAuth) (string, io.ReadCloser, error) {
	url := c.baseURL(r) + "/v2/" + r.Repository + "/blobs/" + digest
	resp, err := c.getBlob(ctx, url, "")
	if err != nil {
		return "", nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		ch := parseChallenge(resp.Header.Get("Www-Authenticate"))
		_ = resp.Body.Close()
		token, err := c.fetchToken(ctx, ch, auth)
		if err != nil {
			return "", nil, err
		}
		resp, err = c.getBlob(ctx, url, token)
		if err != nil {
			return "", nil, err
		}
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		_ = resp.Body.Close()
		return "", nil, fmt.Errorf("oci: blob %s returned %d: %s", digest, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return resp.Header.Get("Content-Type"), resp.Body, nil
}

func (c *RegistryClient) getBlob(ctx context.Context, url, token string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("oci: build blob request: %w", err)
	}
	req.Header.Set("User-Agent", c.ua)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("oci: fetch blob: %w", err)
	}
	return resp, nil
}

func (c *RegistryClient) baseURL(r Reference) string {
	host := c.host
	if host == "" {
		host = r.APIHost()
	}
	return c.scheme + "://" + host
}

func (c *RegistryClient) resolveDigest(ctx context.Context, r Reference) (string, error) {
	return c.resolveDigestWithAuth(ctx, r, nil)
}

// resolveDigestWithAuth is the AuthPuller variant of resolveDigest.
// `auth == nil` is the anonymous path (issue #461 / ADR-062).
func (c *RegistryClient) resolveDigestWithAuth(ctx context.Context, r Reference, auth *BasicAuth) (string, error) {
	url := c.baseURL(r) + "/v2/" + r.Repository + "/manifests/" + r.ManifestRef()

	resp, err := c.getManifest(ctx, url, "")
	if err != nil {
		return "", err
	}
	// A 401 carries the token challenge: fetch a bearer token and retry once.
	if resp.StatusCode == http.StatusUnauthorized {
		ch := parseChallenge(resp.Header.Get("Www-Authenticate"))
		_ = resp.Body.Close()
		token, err := c.fetchToken(ctx, ch, auth)
		if err != nil {
			return "", err
		}
		if resp, err = c.getManifest(ctx, url, token); err != nil {
			return "", err
		}
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		msg := fmt.Sprintf("oci: manifest %s: registry returned %d: %s",
			r.String(), resp.StatusCode, strings.TrimSpace(string(body)))
		// ADR-021: lift the 404 (image-not-found) failure mode to a
		// sentinel that pkg/api.SentinelToCode maps to the RFC 7807
		// CodeImageNotFound so the customer / dashboard can branch on
		// a stable string. Other non-200 statuses (5xx, 401-after-
		// retry, 403) keep their free-text surface — those are not
		// the three puller-side failure modes this ADR closes.
		if resp.StatusCode == http.StatusNotFound {
			return "", fmt.Errorf("%w: %s", ErrImageNotFound, msg)
		}
		return "", fmt.Errorf("%s", msg)
	}

	// Prefer the registry's content digest header; fall back to hashing the
	// manifest body ourselves (some registries omit it).
	if dg := resp.Header.Get("Docker-Content-Digest"); dg != "" {
		if err := validateDigest(dg); err != nil {
			return "", fmt.Errorf("oci: %s returned malformed Docker-Content-Digest: %w", r.String(), err)
		}
		return dg, nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20)) // manifests are tiny; cap at 8 MiB
	if err != nil {
		return "", fmt.Errorf("oci: read manifest %s: %w", r.String(), err)
	}
	sum := sha256.Sum256(body)
	return digestAlgo + hex.EncodeToString(sum[:]), nil
}

func (c *RegistryClient) getManifest(ctx context.Context, url, token string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("oci: build manifest request: %w", err)
	}
	req.Header.Set("Accept", manifestAccept)
	req.Header.Set("User-Agent", c.ua)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("oci: fetch manifest: %w", err)
	}
	return resp, nil
}

// fetchToken performs the anonymous Bearer-token GET the WWW-Authenticate
// challenge points at (realm?service=&scope=). Public pulls need no
// credentials. The challenge-parse + token-fetch plumbing lives in auth.go
// so pkg/storage.OCIRegistryStorageBackend (issue #96 slice 2) can reuse
// it with optional Basic creds for private push.
//
// Pass `auth == nil` for the anonymous path; pass a *BasicAuth to send
// the customer's sealed registry credential over the realm endpoint
// (issue #461 / ADR-062). FetchToken already handles nil safely; the
// returned error is scrubbed of any Authorization header so the
// underlying Basic Auth value cannot leak through slog / RFC 7807
// error surfaces.
func (c *RegistryClient) fetchToken(ctx context.Context, ch authChallenge, auth *BasicAuth) (string, error) {
	tok, err := FetchToken(ctx, c.hc, c.ua, newAuthChallenge(ch), auth)
	if err != nil {
		return "", scrubAuthFromError(err, auth)
	}
	return tok.AccessToken, nil
}

// scrubAuthFromError strips the customer's Basic Auth username + password
// (and the base64-encoded composite) from any error string the realm
// endpoint or its response chain might echo back. The realm endpoint
// SHOULD NOT echo the Authorization header in its 4xx body, but a
// defence-in-depth scrub here closes the leak path the slog stack
// would otherwise walk.
//
// The replacement is "REDACTED" so logs that survive to disk are
// unambiguously scrubbed — a future forensics sweep can grep for the
// literal substring without reconstructing the original secret.
//
// Idempotent: a second call on an already-scrubbed error is a no-op.
func scrubAuthFromError(err error, auth *BasicAuth) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	scrubbed := msg
	if auth != nil {
		if auth.Username != "" {
			scrubbed = strings.ReplaceAll(scrubbed, auth.Username, "REDACTED")
		}
		if auth.Password != "" {
			scrubbed = strings.ReplaceAll(scrubbed, auth.Password, "REDACTED")
		}
		// Replace the base64-encoded "Basic <creds>" composite as well.
		// base64.StdEncoding.EncodeToString of "<username>:<password>"
		// is the form sent on the wire; some servers echo it back.
		if comp := base64.StdEncoding.EncodeToString([]byte(auth.Username + ":" + auth.Password)); comp != "" {
			scrubbed = strings.ReplaceAll(scrubbed, comp, "REDACTED")
		}
	}
	// Strip any literal "Authorization: Basic <...>" or "Bearer <...>"
	// substrings regardless of whether we have an auth to scrub —
	// registry-returned bodies occasionally echo challenge headers.
	scrubbed = authHeaderRe.ReplaceAllString(scrubbed, "Authorization: REDACTED")
	if scrubbed == msg {
		return err
	}
	return errors.New(scrubbed)
}

var authHeaderRe = regexp.MustCompile(`(?i)(authorization\s*:\s*)(basic|bearer)\s+[A-Za-z0-9+/=._-]+`)
