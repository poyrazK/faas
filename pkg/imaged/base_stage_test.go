package imaged

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/rootfs"
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

// TestEnsureBaseExt4_StagesOnFirstRun — no prior ext4, no digest sidecar →
// pulls layers, runs BuildBase, writes both the ext4 and the .digest
// sidecar. Skipped=false.
func TestEnsureBaseExt4_StagesOnFirstRun(t *testing.T) {
	mp := newTwoLayerPuller(t)
	outDir := t.TempDir()
	outImage := filepath.Join(outDir, "builder-base.ext4")

	h := &Handler{
		oci:     mp,
		builder: &fakeBuilder{},
		log:     silentLogger(),
	}
	res, err := h.EnsureBaseExt4(context.Background(),
		"ghcr.io/onebox-faas/builder-base:latest", outImage)
	if err != nil {
		t.Fatalf("EnsureBaseExt4: %v", err)
	}
	if res.Skipped {
		t.Error("Skipped=true on first run, want false")
	}
	if res.ConfigDigest == "" {
		t.Error("ConfigDigest empty, want the manifest's")
	}
	if _, err := os.Stat(outImage); err != nil {
		t.Errorf("output ext4 not created: %v", err)
	}
	digestBytes, err := os.ReadFile(outImage + ".digest")
	if err != nil {
		t.Errorf("digest sidecar not written: %v", err)
	}
	if string(digestBytes) != res.ConfigDigest {
		t.Errorf("sidecar %q != res.ConfigDigest %q", string(digestBytes), res.ConfigDigest)
	}
}

// TestEnsureBaseExt4_SkipsWhenDigestMatches — pre-existing ext4 + matching
// .digest sidecar → no second stage, no extra layers pulled. We detect the
// "no second stage" by checking that BuildBase.calls didn't grow.
func TestEnsureBaseExt4_SkipsWhenDigestMatches(t *testing.T) {
	mp := newTwoLayerPuller(t)
	outDir := t.TempDir()
	outImage := filepath.Join(outDir, "builder-base.ext4")
	if err := os.WriteFile(outImage, []byte("existing ext4"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Sidecar encodes the same config digest the test's manifest carries —
	// constructing it independently of the manifest inside EnsureBaseExt4
	// means we'd be racing the manifest fetch for the answer; pin it as
	// whatever the puller returns.
	manifest, err := mp.PullManifest(context.Background(), "x")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outImage+".digest", []byte(manifest.Config.Digest), 0o644); err != nil {
		t.Fatal(err)
	}
	// Pre-record call count: BuildBase has never run yet.
	b := &callCountingBuilder{}
	h := &Handler{oci: mp, builder: b, log: silentLogger()}
	res, err := h.EnsureBaseExt4(context.Background(),
		"ghcr.io/onebox-faas/builder-base:latest", outImage)
	if err != nil {
		t.Fatalf("EnsureBaseExt4: %v", err)
	}
	if !res.Skipped {
		t.Error("Skipped=false on matching digest, want true")
	}
	if b.calls != 0 {
		t.Errorf("BuildBase called %d times, want 0 (digest match)", b.calls)
	}
	// Original file content preserved.
	body, err := os.ReadFile(outImage)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "existing ext4" {
		t.Errorf("file body changed during skip path: %q", string(body))
	}
}

// TestEnsureBaseExt4_RestagesWhenDigestDiffers — sidecar exists with the
// WRONG digest → forced restage. We re-write the existing ext4 from BuildBase
// (the fakeBuilder echoes its OutImage, so the file's mtime would change in
// a real run; here we just check the BuildBase call happened).
func TestEnsureBaseExt4_RestagesWhenDigestDiffers(t *testing.T) {
	mp := newTwoLayerPuller(t)
	outDir := t.TempDir()
	outImage := filepath.Join(outDir, "builder-base.ext4")
	if err := os.WriteFile(outImage, []byte("stale ext4"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outImage+".digest", []byte("sha256:0000000000000000000000000000000000000000000000000000000000000000"), 0o644); err != nil {
		t.Fatal(err)
	}
	b := &callCountingBuilder{}
	h := &Handler{oci: mp, builder: b, log: silentLogger()}
	res, err := h.EnsureBaseExt4(context.Background(),
		"ghcr.io/onebox-faas/builder-base:latest", outImage)
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

// TestEnsureBaseExt4_RejectsEmptyInputs is the boundary test: both ref and
// OutImage are required; passing either empty is a config error.
func TestEnsureBaseExt4_RejectsEmptyInputs(t *testing.T) {
	h := &Handler{oci: &minimalManifestPuller{}, builder: &fakeBuilder{}, log: silentLogger()}
	if _, err := h.EnsureBaseExt4(context.Background(), "", "/tmp/x"); err == nil {
		t.Error("empty ref should error")
	}
	if _, err := h.EnsureBaseExt4(context.Background(), "ghcr.io/x:latest", ""); err == nil {
		t.Error("empty OutImage should error")
	}
}

// TestEnsureBaseExt4_RejectsPullerWithoutManifestPuller — when production
// wires a puller that doesn't implement ManifestPuller (e.g. a future fake
// used in test), we fail loudly rather than silently skipping the stage.
func TestEnsureBaseExt4_RejectsPullerWithoutManifestPuller(t *testing.T) {
	h := &Handler{oci: oci.DefaultPuller{}, builder: &fakeBuilder{}, log: silentLogger()}
	_, err := h.EnsureBaseExt4(context.Background(),
		"ghcr.io/onebox-faas/builder-base:latest", "/tmp/x.ext4")
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
	h := &Handler{oci: bad, builder: &fakeBuilder{}, log: silentLogger()}
	_, err := h.EnsureBaseExt4(context.Background(),
		"ghcr.io/onebox-faas/builder-base:latest", "/tmp/x.ext4")
	if err == nil {
		t.Fatal("expected error from broken puller")
	}
	if !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("error %q must preserve 'connection refused' from registry", err.Error())
	}
}

// TestEnsureBaseExt4_CleansUpOnBuildFailure — when BuildBase fails, the
// .tmp file must NOT be left behind; the next run must try again instead
// of trusting a half-written image.
func TestEnsureBaseExt4_CleansUpOnBuildFailure(t *testing.T) {
	mp := newTwoLayerPuller(t)
	outDir := t.TempDir()
	outImage := filepath.Join(outDir, "builder-base.ext4")
	h := &Handler{
		oci:     mp,
		builder: &failingBuilder{err: errors.New("mkfs exploded")},
		log:     silentLogger(),
	}
	_, err := h.EnsureBaseExt4(context.Background(),
		"ghcr.io/onebox-faas/builder-base:latest", outImage)
	if err == nil {
		t.Fatal("expected build failure")
	}
	if _, err := os.Stat(outImage + ".tmp"); err == nil {
		t.Error(".tmp file left behind after BuildBase failure")
	}
	if _, err := os.Stat(outImage); err == nil {
		t.Error("OutImage file unexpectedly created")
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
// BuildBase has been called. Used by the skip-vs-restage tests. Writes a
// placeholder so the rename in EnsureBaseExt4 has a real source file.
type callCountingBuilder struct{ calls int }

func (b *callCountingBuilder) Build(_ context.Context, in rootfs.BuildInput) (rootfs.BuildResult, error) {
	return rootfs.BuildResult{ImagePath: in.OutImage}, nil
}
func (b *callCountingBuilder) BuildBase(_ context.Context, in rootfs.BaseBuildInput) (rootfs.BaseBuildResult, error) {
	b.calls++
	if err := os.WriteFile(in.OutImage, []byte("fake ext4"), 0o644); err != nil {
		return rootfs.BaseBuildResult{}, err
	}
	return rootfs.BaseBuildResult{ImagePath: in.OutImage}, nil
}

// failingBuilder always errors from BuildBase. Used to prove cleanup of
// the .tmp file on failure.
type failingBuilder struct{ err error }

func (b *failingBuilder) Build(_ context.Context, in rootfs.BuildInput) (rootfs.BuildResult, error) {
	return rootfs.BuildResult{}, b.err
}
func (b *failingBuilder) BuildBase(_ context.Context, in rootfs.BaseBuildInput) (rootfs.BaseBuildResult, error) {
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
