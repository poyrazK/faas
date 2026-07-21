package oci

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
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
type RegistryClient struct {
	hc     *http.Client
	scheme string // "https" in production; the test seam sets "http"
	host   string // "" = derive from the reference; tests pin an httptest host
	ua     string
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

// NewRegistryClient builds a client with sensible defaults (HTTPS,
// api.OCIPullTimeoutSeconds timeout — currently 60s). Tests that need a
// shorter deadline can pass WithTimeout; production passes
// WithHTTPClient(NewEgressHTTPClient()) for the §11 egress guard.
func NewRegistryClient(opts ...Option) *RegistryClient {
	c := &RegistryClient{
		hc:     &http.Client{Timeout: time.Duration(api.OCIPullTimeoutSeconds) * time.Second},
		scheme: "https",
		ua:     "faas-imaged/1 (+https://DOMAIN)",
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

// layerMediaTypes are the manifest media types we will walk for layer blobs.
// Indexes / manifest lists are NOT supported here — they require choosing a
// platform; a digest-pinned reference to a single-arch image is the M5
// contract (spec §17 G1: public registries, digest-pinned).
var imageManifestMediaTypes = []string{
	"application/vnd.oci.image.manifest.v1+json",
	"application/vnd.docker.distribution.manifest.v2+json",
}

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

// fetchManifest performs the authenticated GET on a manifest URL and parses
// it. Returns (imageManifest, raw manifest body bytes, err). Shared by
// PullImageConfig (cheap path) and PullLayers (full path), so the two can't
// drift in manifest-acceptance rules.
func (c *RegistryClient) fetchManifest(ctx context.Context, r Reference) (imageManifest, []byte, error) {
	var empty imageManifest
	manifestURL := c.baseURL(r) + "/v2/" + r.Repository + "/manifests/" + r.ManifestRef()
	body, ct, err := c.fetchManifestJSON(ctx, manifestURL)
	if err != nil {
		return empty, nil, err
	}
	var m imageManifest
	if err := json.Unmarshal(body, &m); err != nil {
		return empty, nil, fmt.Errorf("oci: decode manifest %s: %w", r.String(), err)
	}
	// Reject index/manifest-list for v1 — we accept only single-arch image
	// manifests. Surfacing a clear error here is friendlier than silently
	// ignoring the layers array.
	//
	// ADR-021: this is one of the puller-side failure modes that maps to
	// the RFC 7807 CodeImageManifestInvalid (422). imaged's buildImageLayer
	// failure path runs errors.As(err, ErrImageManifestInvalid) and
	// persists the resulting code on deployments.error_code. Wrap with %w
	// so errors.Is matches both the bare sentinel and any further
	// %w-wrapped form by imaged.
	if !isImageManifest(ct, m.MediaType) {
		return empty, nil, fmt.Errorf("%w: %s is a manifest list/index, not an image manifest (mediaType=%q); digest-pinned to a single-arch image is required",
			ErrImageManifestInvalid, r.String(), m.MediaType)
	}
	return m, body, nil
}

// isImageManifest reports whether the manifest is a single-arch image
// manifest (as opposed to an index / manifest-list).
func isImageManifest(contentType, mediaType string) bool {
	if mediaType != "" {
		for _, mt := range imageManifestMediaTypes {
			if mediaType == mt {
				return true
			}
		}
		return false
	}
	// Some registries omit mediaType in the body; fall back to the response's
	// Content-Type header.
	for _, mt := range imageManifestMediaTypes {
		if contentType == mt {
			return true
		}
	}
	return false
}

// parseImageConfig decodes the subset of the OCI image config we care about.
// Unrecognised fields are ignored — the schema is large and we want to be
// resilient to additions upstream. The OCI spec allows either flat fields
// (Cmd, Env, WorkingDir at the top level) or a nested "config" envelope —
// we accept whichever the registry produced, preferring the flat fields when
// both are present (Docker v2 convention).
func parseImageConfig(b []byte) (ImageConfig, error) {
	var raw struct {
		Cmd        []string `json:"Cmd"`
		Env        []string `json:"Env"`
		WorkingDir string   `json:"WorkingDir"`
		Config     struct {
			Cmd          []string            `json:"Cmd"`
			Env          []string            `json:"Env"`
			WorkingDir   string              `json:"WorkingDir"`
			ExposedPorts map[string]struct{} `json:"ExposedPorts"`
		} `json:"config"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return ImageConfig{}, err
	}
	cmd := raw.Cmd
	if len(cmd) == 0 {
		cmd = raw.Config.Cmd
	}
	envSlice := raw.Env
	if len(envSlice) == 0 {
		envSlice = raw.Config.Env
	}
	wd := raw.WorkingDir
	if wd == "" {
		wd = raw.Config.WorkingDir
	}
	env := make(map[string]string, len(envSlice))
	for _, kv := range envSlice {
		for i := 0; i < len(kv); i++ {
			if kv[i] == '=' {
				env[kv[:i]] = kv[i+1:]
				break
			}
		}
	}
	return ImageConfig{
		Cmd:          cmd,
		Env:          env,
		WorkingDir:   wd,
		ExposedPorts: raw.Config.ExposedPorts,
	}, nil
}

// fetchManifestJSON performs an authenticated GET on a manifest URL and
// returns (body, content-type, err). Handles the anonymous Bearer challenge
// exactly once.
func (c *RegistryClient) fetchManifestJSON(ctx context.Context, url string) ([]byte, string, error) {
	resp, err := c.getManifest(ctx, url, "")
	if err != nil {
		return nil, "", err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		ch := parseChallenge(resp.Header.Get("Www-Authenticate"))
		_ = resp.Body.Close()
		token, err := c.fetchToken(ctx, ch)
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
	_, rc, err := c.openBlob(ctx, r, digest)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	return io.ReadAll(io.LimitReader(rc, 1<<20)) // 1 MiB cap — config blobs are tiny
}

// fetchBlobStream opens a blob as a streaming ReadCloser. The caller is
// responsible for closing it; the body is not buffered.
func (c *RegistryClient) fetchBlobStream(ctx context.Context, r Reference, digest string) (io.ReadCloser, error) {
	_, body, err := c.openBlob(ctx, r, digest)
	if err != nil {
		return nil, err
	}
	return body, nil
}

// openBlob performs the GET /v2/<repo>/blobs/<digest> request with one retry
// after a 401 challenge. Returns (content-type, body, err).
func (c *RegistryClient) openBlob(ctx context.Context, r Reference, digest string) (string, io.ReadCloser, error) {
	url := c.baseURL(r) + "/v2/" + r.Repository + "/blobs/" + digest
	resp, err := c.getBlob(ctx, url, "")
	if err != nil {
		return "", nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		ch := parseChallenge(resp.Header.Get("Www-Authenticate"))
		_ = resp.Body.Close()
		token, err := c.fetchToken(ctx, ch)
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
	url := c.baseURL(r) + "/v2/" + r.Repository + "/manifests/" + r.ManifestRef()

	resp, err := c.getManifest(ctx, url, "")
	if err != nil {
		return "", err
	}
	// A 401 carries the token challenge: fetch a bearer token and retry once.
	if resp.StatusCode == http.StatusUnauthorized {
		ch := parseChallenge(resp.Header.Get("Www-Authenticate"))
		_ = resp.Body.Close()
		token, err := c.fetchToken(ctx, ch)
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
func (c *RegistryClient) fetchToken(ctx context.Context, ch authChallenge) (string, error) {
	tok, err := FetchToken(ctx, c.hc, c.ua, newAuthChallenge(ch), nil)
	if err != nil {
		return "", err
	}
	return tok.AccessToken, nil
}
