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
	Cmd        []string // → AppManifest.Entrypoint
	Env        map[string]string // "KEY" → "VALUE"; imaged flattens to AppManifest.Env
	WorkingDir string   // → AppManifest.WorkingDir
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

// DefaultPuller is the offline default — it echoes the reference back from
// PullDigest / PullImageConfig and returns no layers from PullLayers.
// imaged.New substitutes it when no puller is injected; the shape
// pkg/imaged tests exercise.
//
// Production wires oci.RegistryClient, which serves real layer blobs.
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
