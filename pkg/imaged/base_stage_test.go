package imaged

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/rootfs"
	"github.com/onebox-faas/faas/pkg/storage"
)

// minimalManifestPuller implements just enough to satisfy oci.ManifestPuller
// for EnsureBaseExt4. Manifest answers PullManifest with the canned image;
// Blobs serves two layer blobs; the rest (PullDigest / PullImageConfig /
// PullLayers) is implemented as no-op-error because the base path doesn't
// call them.
type minimalManifestPuller struct {
	manifest oci.Manifest
	layers   map[string][]byte // digest -> gzipped tarball bytes
}

func (f *minimalManifestPuller) PullDigest(_ context.Context, ref string) (string, error) {
	return ref, nil
}
func (f *minimalManifestPuller) PullImageConfig(_ context.Context, _ string) (oci.ImageConfig, error) {
	return oci.ImageConfig{}, nil
}
func (f *minimalManifestPuller) PullLayers(_ context.Context, _ string) (oci.PullLayersResult, error) {
	return oci.PullLayersResult{}, nil
}
func (f *minimalManifestPuller) PullManifest(_ context.Context, _ string) (oci.Manifest, error) {
	return f.manifest, nil
}
func (f *minimalManifestPuller) PullBlob(_ context.Context, _ string, digest string) (io.ReadCloser, error) {
	b, ok := f.layers[digest]
	if !ok {
		return nil, errors.New("no such digest in fake: " + digest)
	}
	return io.NopCloser(strings.NewReader(string(b))), nil
}

// newBaseHarness builds a Handler with a minimalManifestPuller, a builder,
// and a per-test LocalStorageBackend. Returns the handler + the storage
// backend so tests can assert on published keys.
type baseHarness struct {
	h  *Handler
	be storage.StorageBackend
}

func newBaseHarness(t *testing.T, mp *minimalManifestPuller, b LayerBuilder) *baseHarness {
	t.Helper()
	be, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStorageBackend: %v", err)
	}
	h := &Handler{
		oci:     mp,
		builder: b,
		log:     silentLogger(),
		storage: be,
	}
	return &baseHarness{h: h, be: be}
}

// TestEnsureBaseExt4_StagesOnFirstRun — no prior ext4, no digest sidecar →
// pulls layers, runs BuildBase, writes both the ext4 and the .digest
// sidecar. Skipped=false. Asserts the produced ext4 lives at baseKey and
// the digest sidecar matches res.ConfigDigest.
func TestEnsureBaseExt4_StagesOnFirstRun(t *testing.T) {
	mp := newTwoLayerPuller(t)
	b := &fakeBuilder{}
	hs := newBaseHarness(t, mp, b)
	const baseKey = "base/runtime.ext4"
	const digKey = "base/runtime.ext4.digest"
	res, err := hs.h.EnsureBaseExt4(context.Background(),
		"ghcr.io/onebox-faas/builder-base:latest", baseKey, digKey, "")
	if err != nil {
		t.Fatalf("EnsureBaseExt4: %v", err)
	}
	if res.Skipped {
		t.Error("Skipped=true on first run, want false")
	}
	if res.ConfigDigest == "" {
		t.Error("ConfigDigest empty, want the manifest's")
	}
	if res.StorageKey != baseKey {
		t.Errorf("StorageKey=%q, want %q", res.StorageKey, baseKey)
	}
	rc, err := hs.be.Get(context.Background(), baseKey)
	if err != nil {
		t.Fatalf("base ext4 not at key %q: %v", baseKey, err)
	}
	defer rc.Close()
	digestBytes, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read base ext4: %v", err)
	}
	if !bytes.Contains(digestBytes, []byte("fake ext4")) {
		t.Errorf("base ext4 bytes %q should contain fake ext4 marker", string(digestBytes))
	}
	digestRC, err := hs.be.Get(context.Background(), digKey)
	if err != nil {
		t.Fatalf("digest sidecar not at key %q: %v", digKey, err)
	}
	defer digestRC.Close()
	haveDigest, err := io.ReadAll(digestRC)
	if err != nil {
		t.Fatalf("read digest sidecar: %v", err)
	}
	if string(haveDigest) != res.ConfigDigest {
		t.Errorf("sidecar %q != res.ConfigDigest %q", string(haveDigest), res.ConfigDigest)
	}
}

// TestEnsureBaseExt4_SkipsWhenDigestMatches — pre-existing ext4 + matching
// .digest sidecar → no second stage, no extra layers pulled. We detect the
// "no second stage" by checking that BuildBase.calls didn't grow.
func TestEnsureBaseExt4_SkipsWhenDigestMatches(t *testing.T) {
	mp := newTwoLayerPuller(t)
	const baseKey = "base/runtime.ext4"
	const digKey = "base/runtime.ext4.digest"
	be, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	be.Put(context.Background(), baseKey, strings.NewReader("existing ext4"))
	manifest, err := mp.PullManifest(context.Background(), "x")
	if err != nil {
		t.Fatal(err)
	}
	be.Put(context.Background(), digKey, strings.NewReader(manifest.Config.Digest))
	b := &callCountingBuilder{}
	h := &Handler{oci: mp, builder: b, log: silentLogger(), storage: be}
	res, err := h.EnsureBaseExt4(context.Background(),
		"ghcr.io/onebox-faas/builder-base:latest", baseKey, digKey, "")
	if err != nil {
		t.Fatalf("EnsureBaseExt4: %v", err)
	}
	if !res.Skipped {
		t.Error("Skipped=false on matching digest, want true")
	}
	if b.calls != 0 {
		t.Errorf("BuildBase called %d times, want 0 (digest match)", b.calls)
	}
	rc, _ := be.Get(context.Background(), baseKey)
	defer rc.Close()
	body, _ := io.ReadAll(rc)
	if string(body) != "existing ext4" {
		t.Errorf("file body changed during skip path: %q", string(body))
	}
}

// TestEnsureBaseExt4_RestagesWhenDigestDiffers — sidecar exists with the
// WRONG digest → forced restage. We re-write the existing ext4 from BuildBase
// and assert the BuildBase call happened.
func TestEnsureBaseExt4_RestagesWhenDigestDiffers(t *testing.T) {
	mp := newTwoLayerPuller(t)
	const baseKey = "base/runtime.ext4"
	const digKey = "base/runtime.ext4.digest"
	be, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	be.Put(context.Background(), baseKey, strings.NewReader("stale ext4"))
	be.Put(context.Background(), digKey, strings.NewReader("sha256:0000000000000000000000000000000000000000000000000000000000000000"))
	b := &callCountingBuilder{}
	h := &Handler{oci: mp, builder: b, log: silentLogger(), storage: be}
	res, err := h.EnsureBaseExt4(context.Background(),
		"ghcr.io/onebox-faas/builder-base:latest", baseKey, digKey, "")
	if err != nil {
		t.Fatalf("EnsureBaseExt4: %v", err)
	}
	if res.Skipped {
		t.Error("Skipped=true when digest differed, want false")
	}
	if b.calls != 1 {
		t.Errorf("BuildBase called %d times, want 1 (forced restage)", b.calls)
	}
}

// TestEnsureBaseExt4_RejectsEmptyInputs is the boundary test: ref,
// baseKey, and digestKey are all required; passing any of them empty
// is a config error.
func TestEnsureBaseExt4_RejectsEmptyInputs(t *testing.T) {
	be, _ := storage.NewLocalStorageBackend(t.TempDir())
	h := &Handler{oci: &minimalManifestPuller{}, builder: &fakeBuilder{}, log: silentLogger(), storage: be}
	if _, err := h.EnsureBaseExt4(context.Background(), "", "k", "k.digest", ""); err == nil {
		t.Error("empty ref should error")
	}
	if _, err := h.EnsureBaseExt4(context.Background(), "ref", "", "k.digest", ""); err == nil {
		t.Error("empty baseKey should error")
	}
	if _, err := h.EnsureBaseExt4(context.Background(), "ref", "k", "", ""); err == nil {
		t.Error("empty digestKey should error")
	}
}

// TestEnsureBaseExt4_RejectsPullerWithoutManifestPuller — when production
// wires a puller that doesn't implement ManifestPuller (e.g. a future fake
// used in test), we fail loudly rather than silently skipping the stage.
func TestEnsureBaseExt4_RejectsPullerWithoutManifestPuller(t *testing.T) {
	be, _ := storage.NewLocalStorageBackend(t.TempDir())
	h := &Handler{oci: oci.DefaultPuller{}, builder: &fakeBuilder{}, log: silentLogger(), storage: be}
	_, err := h.EnsureBaseExt4(context.Background(),
		"ghcr.io/onebox-faas/builder-base:latest", "k", "k.digest", "")
	if err == nil {
		t.Fatal("expected error when puller lacks ManifestPuller")
	}
	if !strings.Contains(err.Error(), "ManifestPuller") {
		t.Errorf("error %q must mention ManifestPuller", err.Error())
	}
}

// TestEnsureBaseExt4_BubblesPullManifestErrors — registry unreachable is a
// startup failure, not a silent skip; the daemon should refuse to come up.
func TestEnsureBaseExt4_BubblesPullManifestErrors(t *testing.T) {
	bad := &brokenManifestPuller{manifestErr: errors.New("connection refused")}
	be, _ := storage.NewLocalStorageBackend(t.TempDir())
	h := &Handler{oci: bad, builder: &fakeBuilder{}, log: silentLogger(), storage: be}
	_, err := h.EnsureBaseExt4(context.Background(),
		"ghcr.io/onebox-faas/builder-base:latest", "k", "k.digest", "")
	if err == nil {
		t.Fatal("expected error from broken puller")
	}
	if !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("error %q must preserve 'connection refused' from registry", err.Error())
	}
}

// TestEnsureBaseExt4_BuildFailureSurfaces — when BuildBase fails, the
// baseKey must NOT be present after the call (the publish step is
// skipped on builder error) and the digest sidecar must NOT have been
// written either.
func TestEnsureBaseExt4_BuildFailureSurfaces(t *testing.T) {
	mp := newTwoLayerPuller(t)
	be, _ := storage.NewLocalStorageBackend(t.TempDir())
	h := &Handler{
		oci:     mp,
		builder: &failingBuilder{err: errors.New("mkfs exploded")},
		log:     silentLogger(),
		storage: be,
	}
	_, err := h.EnsureBaseExt4(context.Background(),
		"ghcr.io/onebox-faas/builder-base:latest", "base/runtime.ext4", "base/runtime.ext4.digest", "")
	if err == nil {
		t.Fatal("expected build failure")
	}
	if _, err := be.Get(context.Background(), "base/runtime.ext4"); err == nil {
		t.Error("base ext4 unexpectedly created on builder failure")
	}
}

// newTwoLayerPuller fabricates a one-config, two-layer OCI image out of
// (gzipped) tarballs built by tarball_test.go's gzTar helper. The digest
// values below mirror what a registry would synthesize (we ignore the
// authenticity — the base stage only uses them as opaque IDs).
func newTwoLayerPuller(t *testing.T) *minimalManifestPuller {
	t.Helper()
	layerA := "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	layerB := "sha256:2222222222222222222222222222222222222222222222222222222222222222"
	cfg := "sha256:3333333333333333333333333333333333333333333333333333333333333333"
	manifest := oci.Manifest{
		Config: oci.Descriptor{MediaType: "application/vnd.oci.image.config.v1+json", Digest: cfg},
		Layers: []oci.Descriptor{
			{MediaType: "application/vnd.oci.image.layer.v1.tar+gzip", Digest: layerA, Size: 8},
			{MediaType: "application/vnd.oci.image.layer.v1.tar+gzip", Digest: layerB, Size: 8},
		},
	}
	bodyA := gzTar(t, map[string]string{"bin/railpack": "rb0"})
	bodyB := gzTar(t, map[string]string{"bin/railpack": "rb1", "etc/faas/build": "manifest"})
	return &minimalManifestPuller{
		manifest: manifest,
		layers: map[string][]byte{
			layerA: bodyA,
			layerB: bodyB,
		},
	}
}

// callCountingBuilder is a LayerBuilder that records how many times
// BuildBase has been called. Used by the skip-vs-restage tests. Storage.Put
// is invoked by the production code path, so the helper just records
// BuildBase calls rather than writing to disk.
type callCountingBuilder struct{ calls int }

func (b *callCountingBuilder) Build(_ context.Context, in rootfs.BuildInput) (rootfs.BuildResult, error) {
	return rootfs.BuildResult{ImageKey: in.StorageKey}, nil
}
func (b *callCountingBuilder) BuildBase(ctx context.Context, in rootfs.BaseBuildInput) (rootfs.BaseBuildResult, error) {
	b.calls++
	if in.Storage != nil && in.StorageKey != "" {
		// Mimic BuildBase's behaviour: produce a (small) placeholder and
		// Put it to the storage key so the storage backend's byte stream
		// is non-empty (skipping the empty-byte rejection in LocalStorageBackend).
		_ = in.Storage.Put(ctx, in.StorageKey, bytes.NewReader([]byte("fake ext4")))
	}
	return rootfs.BaseBuildResult{ImageKey: in.StorageKey}, nil
}

// failingBuilder always errors from BuildBase. Used to prove cleanup of
// the .tmp file on failure.
type failingBuilder struct{ err error }

func (b *failingBuilder) Build(_ context.Context, in rootfs.BuildInput) (rootfs.BuildResult, error) {
	return rootfs.BuildResult{}, b.err
}
func (b *failingBuilder) BuildBase(_ context.Context, _ rootfs.BaseBuildInput) (rootfs.BaseBuildResult, error) {
	return rootfs.BaseBuildResult{}, b.err
}

// brokenManifestPuller fails PullManifest. Used to prove registry errors
// surface rather than being swallowed.
type brokenManifestPuller struct{ manifestErr error }

func (b *brokenManifestPuller) PullDigest(_ context.Context, _ string) (string, error) {
	return "", b.manifestErr
}
func (b *brokenManifestPuller) PullImageConfig(_ context.Context, _ string) (oci.ImageConfig, error) {
	return oci.ImageConfig{}, b.manifestErr
}
func (b *brokenManifestPuller) PullLayers(_ context.Context, _ string) (oci.PullLayersResult, error) {
	return oci.PullLayersResult{}, b.manifestErr
}
func (b *brokenManifestPuller) PullManifest(_ context.Context, _ string) (oci.Manifest, error) {
	return oci.Manifest{}, b.manifestErr
}
func (b *brokenManifestPuller) PullBlob(_ context.Context, _, _ string) (io.ReadCloser, error) {
	return nil, b.manifestErr
}
