// Package oci — OCI digest puller (spec §4.6, §9).
//
// The Puller interface is the single seam imaged uses to resolve a digest-pinned
// image and stream its layers + image config for the app-layer build.
// RegistryClient (registry.go) is the production implementation: a registry v2
// client that resolves a reference to its content digest over the public
// registry API (gap G1) and then fetches layer/config blobs. DefaultPuller is
// the offline/test default that echoes the reference and returns no layers —
// pkg/imaged's orchestration tests need no network.
package oci

import (
	"context"
	"io"
)

// ImageConfig is the parsed subset of an OCI/Docker image config blob that
// imaged needs to construct the AppManifest (spec §4.6). We intentionally
// don't model the full image config schema — just the fields we map.
//
// Field naming follows the OCI image config spec
// (https://github.com/opencontainers/image-spec/blob/main/config.md).
type ImageConfig struct {
	// Platform metadata is inspected without fetching filesystem layers.
	OS           string
	Architecture string
	Variant      string
	Volumes      map[string]struct{}
	// Entrypoint is the image's ENTRYPOINT argv (Docker v2 + OCI).
	// Joined with Cmd when projecting onto AppManifest.Entrypoint.
	// Pre-M-1 silently dropped on the registry path.
	Entrypoint []string
	// Cmd is the image's CMD argv; appended to Entrypoint per OCI
	// semantics. Already populated pre-M-1.
	Cmd []string
	// Env flattened to a map. Pre-M-1 dropped `=VALUE`-style keys
	// (commit 3 fixed that).
	Env map[string]string
	// WorkingDir → AppManifest.WorkingDir.
	WorkingDir string
	// User is the image-declared USER (numeric only, ADR-136 §Decision 5;
	// named users are deferred to M-3). Pre-M-1 silently dropped on the
	// registry path.
	User string
	// Healthcheck mirrors the OCI HEALTHCHECK shape when present.
	// Runtime wiring lands in M-2; M-1 surfaces the field for the
	// fixture harness and the M-3 follow-on.
	Healthcheck *ImageHealthcheck
	// StopSignal mirrors the OCI STOPSIGNAL value. Runtime wiring in M-2.
	StopSignal string
	// StopGracePeriod mirrors the OCI StopGracePeriodSeconds value
	// (integer seconds). Runtime wiring in M-2.
	StopGracePeriodS int
	// ExposedPorts is the set of ports the image declares; we don't use them
	// directly (the customer pins a port via the app's manifest) but parsing
	// them keeps a future "expose-all" mode cheap.
	ExposedPorts map[string]struct{}
}

// PullLayersResult is what PullLayers returns. Layers are streamed bottom-to-top
// in gzip-compressed form (the format `mkfs.ext4 -d` via rootfs.Builder
// expects, after ApplyLayerGz decompresses). Each ReadCloser MUST be closed by
// the caller; RegistryClient returns one per layer blob. Digest is the
// canonical content digest of the manifest the layers came from.
type PullLayersResult struct {
	Layers []io.ReadCloser
	Config ImageConfig
	Digest string
}

// Puller fetches OCI data for imaged.
//
// PullDigest resolves a reference to its canonical digest.
// PullImageConfig fetches only the small image-config blob and parses it —
// no layer streaming. The build pipeline uses this BEFORE PullLayers so a
// manifest that can't become a valid AppManifest (e.g. no Cmd) is rejected
// without fetching dozens of MB of layer blobs (review issue #6, DoS
// amplification on public registries).
// PullLayers streams every layer blob along with the parsed config; it
// internally uses PullImageConfig's manifest handling so the two paths
// can't drift.
type Puller interface {
	PullDigest(ctx context.Context, ref string) (string, error)
	PullImageConfig(ctx context.Context, ref string) (ImageConfig, error)
	PullLayers(ctx context.Context, ref string) (PullLayersResult, error)
}

// AuthPuller is the additive seam for per-app private-registry Basic
// Auth (issue #461 / ADR-062). Production RegistryClient satisfies
// Puller AND AuthPuller; offline DefaultPuller satisfies Puller
// (auth ignored by callers via the type-asserted fallback in imaged).
//
// The interface is intentionally separate from Puller so existing test
// doubles (cmd/e2e/fakevmm, etc.) and every Puller implementation
// across the codebase don't break. imaged type-asserts to AuthPuller
// and falls back to the anonymous path when the assertion fails —
// that mirrors the ManifestPuller pattern (puller.go:71).
//
// Pass `auth == nil` for the anonymous path; the caller's Basic Auth
// is sourced from app_registry_credentials (imaged transiently
// unseals the password). The handler-side egress gate
// (`apps.egress_allowlist`) is evaluated BEFORE the credential lookup
// so an egress-denied host fails the dial without ever touching the
// credential.
type AuthPuller interface {
	Puller
	PullDigestWithAuth(ctx context.Context, ref string, auth *BasicAuth) (string, error)
	PullImageConfigWithAuth(ctx context.Context, ref string, auth *BasicAuth) (ImageConfig, error)
	PullLayersWithAuth(ctx context.Context, ref string, auth *BasicAuth) (PullLayersResult, error)
}

// ManifestPuller is the M6 extension surface: production's RegistryClient
// satisfies it; offline fakes do not. imaged's handleDeployment type-asserts
// to ManifestPuller and falls back to the digest-only flow when the assertion
// fails — that keeps every unit test green without bringing the network in.
//
// PullManifest returns the decoded manifest for ref, including the config
// descriptor and every layer descriptor with its size and digest. PullBlob
// streams the bytes of a blob (layer tarball or config JSON) referenced by
// digest from repo. The caller MUST close the returned reader; the reader is
// gzipped when the underlying blob is.
type ManifestPuller interface {
	Puller
	PullManifest(ctx context.Context, ref string) (Manifest, error)
	PullBlob(ctx context.Context, repo, digest string) (io.ReadCloser, error)
}

// AuthManifestPuller is the additive seam for per-app private-registry
// Basic Auth in the M6 two-drive path (issue #461 / ADR-062).
//
// Contract (mirrors AuthPuller):
//
//  1. ADDITIVE — AuthManifestPuller extends ManifestPuller. Production
//     RegistryClient satisfies both; offline DefaultPuller satisfies
//     ManifestPuller and ignores auth on the WithAuth variants
//     (the auth parameter is kept on the signature to pin the
//     interface). Existing test doubles that satisfy only
//     ManifestPuller continue to compile.
//  2. PASS `auth == nil` for the anonymous path. Callers source
//     `auth` from imaged's transient unseal of app_registry_credentials
//     (pkg/secretbox.OpenBytes, namespace "registry_creds"). The
//     plaintext lives only in the WithAuth call frame — NEVER in
//     dep, audit, log, or error.
//  3. THREADING — imaged's aboveBaseLayers passes `auth` to APP
//     manifest + app blob pulls and `nil` to BASE pulls. The base
//     image is always public `ghcr.io/onebox-faas/...` — a mismatched
//     auth header on a public base pull would break the build path.
//  4. EGRESS — auth does NOT widen the egress surface. The deny
//     gate (pkg/oci.EgressDialContext) is checked BEFORE the
//     credential lookup in imaged; an egress-denied host fails the
//     dial without ever touching the credential. Pinned by
//     TestFetchToken_EgressDeniedBeforeCredentialSent in
//     registry_auth_test.go.
//  5. FAILURE — WithAuth variants have IDENTICAL semantics to their
//     non-auth counterparts modulo the bearer-token realm Basic
//     Auth header. Errors MUST NOT echo the password, base64
//     composite, or Authorization header — scrubAuthFromError
//     (registry.go) runs before any error returns. Pinned by
//     TestScrubAuthFromError_ScrubsAllForms.
//
// imaged's aboveBaseLayers type-asserts to AuthManifestPuller and
// falls back to ManifestPuller (anonymous) when the assertion
// fails — same shape as AuthPuller's fallback in handleDeployment.
type AuthManifestPuller interface {
	ManifestPuller
	PullManifestWithAuth(ctx context.Context, ref string, auth *BasicAuth) (Manifest, error)
	PullBlobWithAuth(ctx context.Context, repo, digest string, auth *BasicAuth) (io.ReadCloser, error)
}

// DefaultPuller is the offline default — it echoes the reference back from
// PullDigest / PullImageConfig and returns no layers from PullLayers.
// imaged.New substitutes it when no puller is injected; the shape
// pkg/imaged tests exercise.
//
// Production wires oci.RegistryClient, which serves real layer blobs and
// implements ManifestPuller (M6).
type DefaultPuller struct{}

func (DefaultPuller) PullDigest(_ context.Context, ref string) (string, error) {
	return ref, nil
}

func (DefaultPuller) PullImageConfig(_ context.Context, _ string) (ImageConfig, error) {
	return ImageConfig{}, nil
}

func (DefaultPuller) PullLayers(_ context.Context, digest string) (PullLayersResult, error) {
	return PullLayersResult{Digest: digest}, nil
}

// DefaultPuller also satisfies AuthPuller (issue #461 / ADR-062). The
// auth argument is ignored — offline tests don't ship credentials and
// the seam is exercised only in production via RegistryClient. Keeping
// the auth parameter on the signature pins the AuthPuller interface.
func (DefaultPuller) PullDigestWithAuth(_ context.Context, ref string, _ *BasicAuth) (string, error) {
	return ref, nil
}

func (DefaultPuller) PullImageConfigWithAuth(_ context.Context, _ string, _ *BasicAuth) (ImageConfig, error) {
	return ImageConfig{}, nil
}

func (DefaultPuller) PullLayersWithAuth(_ context.Context, digest string, _ *BasicAuth) (PullLayersResult, error) {
	return PullLayersResult{Digest: digest}, nil
}
