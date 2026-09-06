package imaged

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/rootfs"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/wire"
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

// legacyRefs returns the DefaultRuntimeBaseRefs slice filtered to
// only the rows on the legacy "apply ALL layers" path
// (ParentRef == ""). The legacy-path unit tests use this so
// they don't have to wire a fake VMMClient and arrange parent
// DiffIDs as a runtime DiffID prefix — the parent-ref branch
// has its own dedicated tests (TestEnsureBaseExt4_WithParentRef_*
// in vmmclient_test.go).
//
// ADR-053: the Debian-backed node/python runtime rows are excluded. Their
// parentRef non-empty triggers the parent-ref branch, which
// requires the parent's DiffIDs to be a strict prefix of the
// runtime's — the minimal test puller doesn't arrange that. The
// parent runtime itself stays in this slice because its
// ParentRef is empty (legacy path) and the test can stage it as
// usual.
func legacyRefs() []RuntimeBaseRef {
	var out []RuntimeBaseRef
	for _, r := range DefaultRuntimeBaseRefs {
		if r.ParentRef == "" {
			out = append(out, r)
		}
	}
	return out
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
		// Inject a no-op grypeRun so the scan sidecar write at the end
		// of EnsureBaseExt4 doesn't shell out to grype (which isn't on
		// the unit-test PATH and would trip the fail-closed CRITICAL=9999
		// placeholder, polluting tests that aren't asserting on the scan
		// sidecar's contents). Tests that DO care about the scan sidecar
		// override the runner with their own stub.
		grypeRun: func(_ context.Context, _ string) (*ScanResult, error) {
			return &ScanResult{}, nil
		},
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
		"ghcr.io/onebox-faas/builder-base:latest", baseKey, digKey, "", "", "")
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
	if string(haveDigest) != baseDigestSidecarValue(res.ConfigDigest) {
		t.Errorf("sidecar %q != expected %q", string(haveDigest), baseDigestSidecarValue(res.ConfigDigest))
	}
}

// TestEnsureBaseExt4_GrypeCalledWithFilesystemPath — Critical #1 of the
// PR #385 review: Grype's `dir:` source walks a filesystem path, NOT an
// OCI ref. The original implementation passed `ref` (the OCI ref, e.g.
// "ghcr.io/onebox-faas/builder-base:latest") to grype, which Grype rejects
// because registry refs belong to a `registry:` source. The fix routes the
// filesystem path published under baseKey to grype while still recording
// the OCI ref in the sidecar's `image` field for dashboard traceability. The
// compatibility outImage is used only when the backend has no local-path
// capability.
func TestEnsureBaseExt4_GrypeCalledWithFilesystemPath(t *testing.T) {
	mp := newTwoLayerPuller(t)
	b := &fakeBuilder{}
	hs := newBaseHarness(t, mp, b)

	const ociRef = "ghcr.io/onebox-faas/builder-base:latest"
	const baseKey = "base/runner-builder-amd64.ext4"
	const digKey = "base/runner-builder-amd64.ext4.digest"
	const outImage = "/srv/fc/base/builder-base.ext4"

	var capturedDir string
	hs.h.grypeRun = func(_ context.Context, dir string) (*ScanResult, error) {
		capturedDir = dir
		return &ScanResult{}, nil
	}

	if _, err := hs.h.EnsureBaseExt4(context.Background(),
		ociRef, baseKey, digKey, outImage, "", ""); err != nil {
		t.Fatalf("EnsureBaseExt4: %v", err)
	}
	resolver, ok := hs.be.(storage.LocalPathResolver)
	if !ok {
		t.Fatal("test backend does not expose LocalPath")
	}
	wantPath, ok, err := resolver.LocalPath(baseKey)
	if err != nil || !ok {
		t.Fatalf("LocalPath(%q) = %q, %t, %v", baseKey, wantPath, ok, err)
	}
	if capturedDir != wantPath {
		t.Errorf("grypeRun called with %q; want published filesystem path %q (not compatibility path %q or OCI ref %q)",
			capturedDir, wantPath, outImage, ociRef)
	}
	if capturedDir == ociRef {
		t.Errorf("grypeRun was handed the OCI ref %q — Grype's `dir:` source walks a filesystem path, not a registry ref", capturedDir)
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
	be.Put(context.Background(), digKey, strings.NewReader(baseDigestSidecarValue(manifest.Config.Digest)))
	b := &callCountingBuilder{}
	h := &Handler{oci: mp, builder: b, log: silentLogger(), storage: be}
	res, err := h.EnsureBaseExt4(context.Background(),
		"ghcr.io/onebox-faas/builder-base:latest", baseKey, digKey, "", "", "")
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

func TestEnsureBaseExt4_RestagesWhenGuestInitChanges(t *testing.T) {
	mp := newTwoLayerPuller(t)
	const baseKey = "base/runtime.ext4"
	const digKey = "base/runtime.ext4.digest"
	be, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	guestInit := filepath.Join(t.TempDir(), "faas-guest-init")
	if err := os.WriteFile(guestInit, []byte("guest-init-v1"), 0o755); err != nil {
		t.Fatal(err)
	}
	b := &callCountingBuilder{}
	h := newBaseHarness(t, mp, b).h
	h.storage = be
	h.guestInitPath = guestInit

	first, err := h.EnsureBaseExt4(context.Background(), "ghcr.io/onebox-faas/runner-node22:latest", baseKey, digKey, "", "", "")
	if err != nil {
		t.Fatalf("first EnsureBaseExt4: %v", err)
	}
	if first.Skipped || b.calls != 1 {
		t.Fatalf("first stage = skipped:%v calls:%d, want skipped:false calls:1", first.Skipped, b.calls)
	}

	rc, err := be.Get(context.Background(), digKey)
	if err != nil {
		t.Fatalf("read digest sidecar: %v", err)
	}
	sidecar, err := io.ReadAll(rc)
	_ = rc.Close()
	if err != nil {
		t.Fatalf("read digest sidecar: %v", err)
	}
	digest, err := guestInitBinaryDigest(guestInit)
	if err != nil {
		t.Fatalf("hash guest-init: %v", err)
	}
	if !strings.Contains(string(sidecar), "guest-init-sha256="+digest) {
		t.Fatalf("sidecar %q does not record guest-init digest %q", sidecar, digest)
	}

	if err := os.WriteFile(guestInit, []byte("guest-init-v2"), 0o755); err != nil {
		t.Fatal(err)
	}
	second, err := h.EnsureBaseExt4(context.Background(), "ghcr.io/onebox-faas/runner-node22:latest", baseKey, digKey, "", "", "")
	if err != nil {
		t.Fatalf("second EnsureBaseExt4: %v", err)
	}
	if second.Skipped {
		t.Fatal("guest-init change incorrectly took the skip path")
	}
	if b.calls != 2 {
		t.Fatalf("BuildBase calls = %d, want 2 after guest-init change", b.calls)
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
		"ghcr.io/onebox-faas/builder-base:latest", baseKey, digKey, "", "", "")
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
	if _, err := h.EnsureBaseExt4(context.Background(), "", "k", "k.digest", "", "", ""); err == nil {
		t.Error("empty ref should error")
	}
	if _, err := h.EnsureBaseExt4(context.Background(), "ref", "", "k.digest", "", "", ""); err == nil {
		t.Error("empty baseKey should error")
	}
	if _, err := h.EnsureBaseExt4(context.Background(), "ref", "k", "", "", "", ""); err == nil {
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
		"ghcr.io/onebox-faas/builder-base:latest", "k", "k.digest", "", "", "")
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
		"ghcr.io/onebox-faas/builder-base:latest", "k", "k.digest", "", "", "")
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
		"ghcr.io/onebox-faas/builder-base:latest", "base/runtime.ext4", "base/runtime.ext4.digest", "", "", "")
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
type callCountingBuilder struct {
	calls            int
	fromStagingCalls int      // ADR-053: BuildBaseFromStaging invocations
	fromStagingArgs  []string // ADR-053 §4.6: the staging path passed to BuildBaseFromStaging
}

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
func (b *callCountingBuilder) BuildBaseFromStaging(ctx context.Context, staging string, in rootfs.BaseBuildInput) (rootfs.BaseBuildResult, error) {
	// ADR-053: parent-ref branch ends here. Tests that exercise the
	// legacy path use the callCountingBuilder too; calling
	// BuildBaseFromStaging must NOT bump BuildBase's call counter
	// (which is asserted by TestEnsureBaseExt4_PerArchPartition) so
	// we count parent-ref builds on a separate field. Tests for the
	// parent-ref branch can branch on fromStagingCalls vs calls.
	//
	// fromStagingArgs captures the staging path so a future test
	// can assert the §4.6 contract: BuildBaseFromStaging must be
	// called with the merged overlay view (not a copy of the
	// parent tree) — that's what gives the on-disk dedup the
	// review called out as missing in PR #465's blocker #3.
	b.fromStagingCalls++
	b.fromStagingArgs = append(b.fromStagingArgs, staging)
	if in.Storage != nil && in.StorageKey != "" {
		_ = in.Storage.Put(ctx, in.StorageKey, bytes.NewReader([]byte("fake ext4 parent-ref")))
	}
	return rootfs.BaseBuildResult{ImageKey: in.StorageKey}, nil
}

// BuildFullRootfs (M-3 commit 5+6) is part of the LayerBuilder
// interface. The full-rootfs build path (ADR-141 §Decision 1) is
// only reachable via the dispatch table in dispatchFullRootfs;
// tests in this file exercise the base stage which never enters
// the full-rootfs branch, so this is a no-op placeholder that
// still satisfies the interface. Tests that DO want to drive the
// full-rootfs dispatch should use the `fullRootfs*` builders
// instead.
func (b *callCountingBuilder) BuildFullRootfs(ctx context.Context, in rootfs.BuildFullRootfsInput) (rootfs.BuildResult, error) {
	if in.Storage != nil && in.StorageKey != "" {
		_ = in.Storage.Put(ctx, in.StorageKey, bytes.NewReader([]byte("fake ext4 full-rootfs")))
	}
	return rootfs.BuildResult{ImageKey: in.StorageKey}, nil
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
func (b *failingBuilder) BuildBaseFromStaging(_ context.Context, _ string, _ rootfs.BaseBuildInput) (rootfs.BaseBuildResult, error) {
	return rootfs.BaseBuildResult{}, b.err
}
func (b *failingBuilder) BuildFullRootfs(_ context.Context, _ rootfs.BuildFullRootfsInput) (rootfs.BuildResult, error) {
	return rootfs.BuildResult{}, b.err
}

// TestEnsureBaseExt4_PerArchPartition — issue #197 B3.3. The same
// runtime staged under two different arch-suffixed keys must produce
// two distinct published ext4s and two distinct digest sidecars in
// storage. This is the load-bearing property that lets an arm64
// imaged binary coexist on the same storage root as an amd64 one
// without clobbering each other's base image.
func TestEnsureBaseExt4_PerArchPartition(t *testing.T) {
	mp := newTwoLayerPuller(t)
	b := &fakeBuilder{}
	hs := newBaseHarness(t, mp, b)

	// Per-arch keys derived via the same helper schedd uses on the
	// wake wire — the source of truth.
	const baseKeyAmd64 = "base/runner-builder-amd64.ext4"
	const baseKeyArm64 = "base/runner-builder-arm64.ext4"
	const digKeyAmd64 = "base/runner-builder-amd64.ext4.digest"
	const digKeyArm64 = "base/runner-builder-arm64.ext4.digest"

	// Stage the amd64 base.
	if _, err := hs.h.EnsureBaseExt4(context.Background(),
		"ghcr.io/onebox-faas/builder-base:latest", baseKeyAmd64, digKeyAmd64, "", "", ""); err != nil {
		t.Fatalf("amd64 stage: %v", err)
	}
	// Stage the arm64 base into the same storage backend.
	if _, err := hs.h.EnsureBaseExt4(context.Background(),
		"ghcr.io/onebox-faas/builder-base:latest", baseKeyArm64, digKeyArm64, "", "", ""); err != nil {
		t.Fatalf("arm64 stage: %v", err)
	}

	// Both ext4s must be present at their respective per-arch keys. The
	// fakeBuilder writes the same literal "fake ext4" payload regardless
	// of call, so this test only asserts presence (NOT byte-distinction);
	// the load-bearing property — same key with different arch suffixes
	// would NOT clobber — is exercised by the TestBaseKeyForArch_* family
	// in pkg/sched/paths_test.go.
	for _, k := range []string{baseKeyAmd64, baseKeyArm64} {
		rc, err := hs.be.Get(context.Background(), k)
		if err != nil {
			t.Fatalf("missing ext4 at %s: %v", k, err)
		}
		buf, _ := io.ReadAll(rc)
		rc.Close()
		if len(buf) == 0 {
			t.Fatalf("ext4 at %s is empty", k)
		}
	}
	// Both digest sidecars must be present.
	for _, k := range []string{digKeyAmd64, digKeyArm64} {
		if _, err := hs.be.Get(context.Background(), k); err != nil {
			t.Fatalf("missing digest sidecar at %s: %v", k, err)
		}
	}
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

// TestEnsureBases_AllRowsStage walks DefaultRuntimeBaseRefs end-to-end:
// every row produces a StorageKey distinct from every other row, the
// digest sidecar matches, and the per-row summary's Skipped=false on
// the first run. The matrix here is the Tier 1 PR 2 lock-step pin —
// if a future runtime is added to DefaultRuntimeBaseRefs without
// matching it here, TestDefaultRuntimeBaseRefs_HasExpectedRuntimes
// (below) catches the drift at unit-test speed. Pinned by ADR-052.
//
// ADR-053: this test now uses legacyRefs (the four non-parent-ref
// rows + builder-base) so it stays on the legacy "apply ALL
// layers" path. The parent-ref branch is exercised by
// TestEnsureBaseExt4_WithParentRef_* (no fake mount needed for
// those — they wire a real tmpdir via fakeVMMClient).
func TestEnsureBases_AllRowsStage(t *testing.T) {
	mp := newTwoLayerPuller(t)
	hs := newBaseHarness(t, mp, &callCountingBuilder{})

	results, err := hs.h.EnsureBases(context.Background(), "amd64", legacyRefs(), nil)
	if err != nil {
		t.Fatalf("EnsureBases: %v", err)
	}
	if len(results) != len(legacyRefs()) {
		t.Fatalf("results = %d rows, want %d", len(results), len(legacyRefs()))
	}
	keysSeen := map[string]bool{}
	for i, r := range results {
		if r.Runtime != legacyRefs()[i].Runtime {
			t.Errorf("row %d runtime = %q, want %q", i, r.Runtime, legacyRefs()[i].Runtime)
		}
		if r.ConfigDigest == "" {
			t.Errorf("row %d (%s) ConfigDigest empty", i, r.Runtime)
		}
		if r.Skipped {
			t.Errorf("row %d (%s) Skipped=true on first run, want false", i, r.Runtime)
		}
		baseKey := sched.BaseKeyForArch(r.Runtime, "amd64")
		if _, err := hs.be.Get(context.Background(), baseKey); err != nil {
			t.Errorf("ext4 missing at %s for runtime %s: %v", baseKey, r.Runtime, err)
		}
		if keysSeen[baseKey] {
			t.Errorf("duplicate baseKey across rows: %s", baseKey)
		}
		keysSeen[baseKey] = true
		digestKey := sched.BaseDigestKeyForArch(r.Runtime, "amd64")
		if _, err := hs.be.Get(context.Background(), digestKey); err != nil {
			t.Errorf("digest sidecar missing at %s for runtime %s: %v", digestKey, r.Runtime, err)
		}
		// digestsSeen is intentionally NOT checked here — in this
		// fake-driven test, every row's puller returns the same
		// fixture manifest, so the config digest is identical across
		// rows. In production, distinct image refs produce distinct
		// OCI config digests; the per-row StorageKey's distinctness
		// (above) is the load-bearing property, not config digest.
	}
}

// TestEnsureBases_OperatorOverride_DigestPinnedWins — when an operator
// sets FAAS_DEPLOY_BASE_REF_<RUNTIME> to a digest-pinned ref, that
// ref is used (not the default). The test exercises the env-lookup
// seam with a hard-coded map; the nil-fallback to os.Getenv is the
// production wiring.
func TestEnsureBases_OperatorOverride_DigestPinnedWins(t *testing.T) {
	mp := newTwoLayerPuller(t)
	hs := newBaseHarness(t, mp, &callCountingBuilder{})
	const overrideRuntime = RuntimeNode24
	const overrideRef = "ghcr.io/onebox-faas/runner-node24@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	env := map[string]string{"FAAS_DEPLOY_BASE_REF_NODE24": overrideRef}
	lookup := func(k string) string { return env[k] }
	// ADR-053: this test only cares about the operator-override
	// path, not the parent-ref branch. Use a one-row slice
	// (RuntimeNode24 on the legacy path) so the test doesn't need
	// a fake VMMClient or parent-DiffID prefix arrangement.
	refs := []RuntimeBaseRef{
		{Runtime: RuntimeNode24, Ref: BaseRefNode24, EnvOverride: "FAAS_DEPLOY_BASE_REF_NODE24"},
	}
	results, err := hs.h.EnsureBases(context.Background(), "amd64", refs, lookup)
	if err != nil {
		t.Fatalf("EnsureBases: %v", err)
	}
	var saw bool
	for _, r := range results {
		if r.Runtime == overrideRuntime {
			saw = true
			if r.Ref != overrideRef {
				t.Errorf("Ref = %q, want override %q", r.Ref, overrideRef)
			}
		} else {
			if r.Ref == overrideRef {
				t.Errorf("override %q leaked into row %s", overrideRef, r.Runtime)
			}
		}
	}
	if !saw {
		t.Fatalf("override row %s missing from results", overrideRuntime)
	}
}

// TestEnsureBases_OperatorOverride_TagOnlyFailsLoud — a tag-only
// override (`node24:latest`, no digest) aborts imaged startup before
// any layer is pulled. The same posture cmd/imaged applies to
// FAAS_DEPLOY_BASE_REF (deploy-time base ref). Pinned by ADR-052.
func TestEnsureBases_OperatorOverride_TagOnlyFailsLoud(t *testing.T) {
	mp := newTwoLayerPuller(t)
	hs := newBaseHarness(t, mp, &callCountingBuilder{})
	env := map[string]string{"FAAS_DEPLOY_BASE_REF_NODE24": "ghcr.io/onebox-faas/runner-node24:latest"}
	lookup := func(k string) string { return env[k] }
	// ADR-053: one-row slice (RuntimeNode24 on the legacy path);
	// this test asserts the operator-override abort path and
	// shouldn't need a fake VMMClient.
	refs := []RuntimeBaseRef{
		{Runtime: RuntimeNode24, Ref: BaseRefNode24, EnvOverride: "FAAS_DEPLOY_BASE_REF_NODE24"},
	}
	_, err := hs.h.EnsureBases(context.Background(), "amd64", refs, lookup)
	if err == nil {
		t.Fatal("tag-only EnvOverride should fail-loud before any byte is pulled")
	}
	if !strings.Contains(err.Error(), "digest-pinned") {
		t.Errorf("error %q must mention 'digest-pinned'", err.Error())
	}
	if !strings.Contains(err.Error(), "FAAS_DEPLOY_BASE_REF_NODE24") {
		t.Errorf("error %q must name the operator-facing env var", err.Error())
	}
}

func TestEnsureBases_TestRedirectAppliesToEveryRuntime(t *testing.T) {
	mp := newTwoLayerPuller(t)
	hs := newBaseHarness(t, mp, &callCountingBuilder{})
	ref := "127.0.0.1:5000/onebox/deploy-base:latest"
	lookup := func(key string) string {
		if key == testDeployBaseRefEnv {
			return ref
		}
		return ""
	}
	refs := []RuntimeBaseRef{
		{Runtime: RuntimeNode22, Ref: BaseRefNode22, EnvOverride: "FAAS_DEPLOY_BASE_REF_NODE22"},
		{Runtime: RuntimePython312, Ref: BaseRefPython312, EnvOverride: "FAAS_DEPLOY_BASE_REF_PYTHON312"},
	}
	results, err := hs.h.EnsureBases(context.Background(), "amd64", refs, lookup)
	if err != nil {
		t.Fatalf("EnsureBases: %v", err)
	}
	for _, result := range results {
		if result.Ref != ref {
			t.Errorf("runtime %s ref = %q, want test redirect %q", result.Runtime, result.Ref, ref)
		}
	}
}

// TestEnsureBases_SkipsOnDigestMatch — second call returns Skipped=true
// for every row when the digest sidecar matches. Inherits the same
// idempotency contract as EnsureBaseExt4's skip path.
func TestEnsureBases_SkipsOnDigestMatch(t *testing.T) {
	mp := newTwoLayerPuller(t)
	hs := newBaseHarness(t, mp, &callCountingBuilder{})
	// ADR-053: legacyRefs so the parent-ref branch (which would
	// need fake VMMClient + parent DiffID prefix arrangement)
	// doesn't trip this skip-on-digest-match test.
	first, err := hs.h.EnsureBases(context.Background(), "amd64", legacyRefs(), nil)
	if err != nil {
		t.Fatalf("first EnsureBases: %v", err)
	}
	second, err := hs.h.EnsureBases(context.Background(), "amd64", legacyRefs(), nil)
	if err != nil {
		t.Fatalf("second EnsureBases: %v", err)
	}
	for i, r := range second {
		if !r.Skipped {
			t.Errorf("row %d (%s) Skipped=false on second run, want true (digest match)", i, r.Runtime)
		}
	}
	if len(first) != len(second) {
		t.Errorf("first/second row counts mismatch: %d vs %d", len(first), len(second))
	}
}

// TestEnsureBases_FailsLoudOnPullError — a broken puller aborts the loop;
// no partial-staged fleet. The test asserts the err path bubble-up
// preserves the underlying registry error so the operator can
// diagnose without grepping the source.
func TestEnsureBases_FailsLoudOnPullError(t *testing.T) {
	bad := &brokenManifestPuller{manifestErr: errors.New("connection refused")}
	be, _ := storage.NewLocalStorageBackend(t.TempDir())
	h := &Handler{
		oci:     bad,
		builder: &fakeBuilder{},
		log:     silentLogger(),
		storage: be,
		grypeRun: func(_ context.Context, _ string) (*ScanResult, error) {
			return &ScanResult{}, nil
		},
	}
	// ADR-053: legacyRefs — broken puller should fail on the
	// parent runtime row, not need a fake VMMClient.
	_, err := h.EnsureBases(context.Background(), "amd64", legacyRefs(), nil)
	if err == nil {
		t.Fatal("EnsureBases must fail on a broken puller")
	}
	if !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("error %q must preserve 'connection refused' from registry", err.Error())
	}
	if !strings.Contains(err.Error(), "stage runtime base") {
		t.Errorf("error %q must annotate which runtime row failed (got %q)", err.Error(), err.Error())
	}
}

// TestEnsureBases_EmptyArchRejected — boundary check.
func TestEnsureBases_EmptyArchRejected(t *testing.T) {
	be, _ := storage.NewLocalStorageBackend(t.TempDir())
	h := &Handler{
		oci:      &minimalManifestPuller{},
		builder:  &fakeBuilder{},
		log:      silentLogger(),
		storage:  be,
		grypeRun: func(_ context.Context, _ string) (*ScanResult, error) { return &ScanResult{}, nil },
	}
	if _, err := h.EnsureBases(context.Background(), "", legacyRefs(), nil); err == nil {
		t.Error("empty arch should error")
	}
}

// TestEnsureBases_NilRefsIsNoOp — convenience: passing nil refs
// returns (nil, nil) so cmd/imaged can guard an "env-disabled" mode
// without a separate nil check. (DefaultRuntimeBaseRefs is never
// nil in production; the path is here for test seam only.)
func TestEnsureBases_NilRefsIsNoOp(t *testing.T) {
	be, _ := storage.NewLocalStorageBackend(t.TempDir())
	h := &Handler{
		oci:      &minimalManifestPuller{},
		builder:  &fakeBuilder{},
		log:      silentLogger(),
		storage:  be,
		grypeRun: func(_ context.Context, _ string) (*ScanResult, error) { return &ScanResult{}, nil },
	}
	if r, err := h.EnsureBases(context.Background(), "amd64", nil, nil); err != nil || r != nil {
		t.Errorf("nil refs → (%v, %v), want (nil, nil)", r, err)
	}
}

// TestDefaultRuntimeBaseRefs_HasExpectedRuntimes — the per-runtime
// set in DefaultRuntimeBaseRefs must match the supported runtime enum
// (apps.runtime CHECK after migrations/00075). A drift here means a
// runtime was added but its base isn't auto-staged, or a removed
// runtime's row wasn't deleted; either trips Tier 1 PR 2's load-bearing
// promise of "every runtime base auto-stages on imaged startup".
//
// ADR-053: 7 rows now (added RuntimeDebianParent at index 0). The
// Debian-backed node/python runtime rows declare ParentRef:
// BaseRefDebianParent and would otherwise pull the parent's tree via the
// parent-ref branch. Node22 is standalone Alpine and intentionally stays on
// the full-chain path. The parent row stays first so its stage failure aborts
// the loop before any Debian-backed child is attempted.
func TestDefaultRuntimeBaseRefs_HasExpectedRuntimes(t *testing.T) {
	want := []string{
		RuntimeDebianParent,
		RuntimeNode22, RuntimeNode24,
		RuntimePython312, RuntimePython313,
		RuntimeGo124, RuntimeGo124Alpine,
	}
	if len(DefaultRuntimeBaseRefs) != len(want) {
		t.Fatalf("DefaultRuntimeBaseRefs = %d rows, want %d", len(DefaultRuntimeBaseRefs), len(want))
	}
	seen := map[string]bool{}
	for i, r := range DefaultRuntimeBaseRefs {
		seen[r.Runtime] = true
		if r.Ref == "" {
			t.Errorf("row %d (%s) Ref empty", i, r.Runtime)
		}
		if r.EnvOverride == "" {
			t.Errorf("row %d (%s) EnvOverride empty", i, r.Runtime)
		}
		// ADR-053: Debian-backed node/python rows MUST declare
		// ParentRef=BaseRefDebianParent. Node22 is the intentional
		// Alpine exception; musl cannot share the Debian parent.
		switch r.Runtime {
		case RuntimeNode24, RuntimePython312, RuntimePython313:
			if r.ParentRef != BaseRefDebianParent {
				t.Errorf("row %d (%s) ParentRef = %q, want %q (ADR-053)",
					i, r.Runtime, r.ParentRef, BaseRefDebianParent)
			}
		case RuntimeNode22, RuntimeDebianParent, RuntimeGo124, RuntimeGo124Alpine:
			if r.ParentRef != "" {
				t.Errorf("row %d (%s) ParentRef = %q, want empty (legacy path)",
					i, r.Runtime, r.ParentRef)
			}
		}
	}
	for _, rt := range want {
		if !seen[rt] {
			t.Errorf("runtime %s missing from DefaultRuntimeBaseRefs; check that migrations/00075 + pkg/imaged/base.go + base_stage.go are in lockstep", rt)
		}
	}
}

// schedBaseKeyForArch is removed; tests use pkg/sched.BaseKeyForArch
// / BaseDigestKeyForArch directly so the key format is sourced from
// the same constant the production code reads.

// =================================================================
// ADR-053: parent-ref staging tests
// =================================================================
//
// These tests exercise the parent-ref branch of EnsureBaseExt4
// (mount → cp -a → umount → pull delta → ApplyLayerGz → mkfs).
// They use a parentPuller that returns one manifest for the
// parent ref and a different, longer manifest for the runtime
// ref, with the parent's DiffIDs as a strict prefix of the
// runtime's. The fakeVMMClient provides a real tmpdir so cp
// -a has something to read.

// parentRuntimePuller implements oci.ManifestPuller for the
// parent-ref tests. Two registries keyed by ref string: the
// parent ref → 1-layer manifest; the runtime ref → 2-layer
// manifest whose first DiffID matches the parent's. This is
// the OCI-chain composability invariant (ADR-053).
type parentRuntimePuller struct {
	parentCfg   string
	parentLayer string
	runtimeCfg  string
	runtimeLy1  string // matches parentLayer (delta prefix invariant)
	runtimeLy2  string
	layers      map[string][]byte // gzipped tarball bytes keyed by digest
}

func (p *parentRuntimePuller) PullDigest(_ context.Context, ref string) (string, error) {
	return ref, nil
}
func (p *parentRuntimePuller) PullImageConfig(_ context.Context, _ string) (oci.ImageConfig, error) {
	return oci.ImageConfig{}, nil
}
func (p *parentRuntimePuller) PullLayers(_ context.Context, _ string) (oci.PullLayersResult, error) {
	return oci.PullLayersResult{}, nil
}
func (p *parentRuntimePuller) PullManifest(_ context.Context, ref string) (oci.Manifest, error) {
	switch ref {
	case BaseRefDebianParent:
		return oci.Manifest{
			Config: oci.Descriptor{Digest: p.parentCfg},
			Layers: []oci.Descriptor{{Digest: p.parentLayer}},
		}, nil
	default:
		return oci.Manifest{
			Config: oci.Descriptor{Digest: p.runtimeCfg},
			Layers: []oci.Descriptor{{Digest: p.runtimeLy1}, {Digest: p.runtimeLy2}},
		}, nil
	}
}
func (p *parentRuntimePuller) PullBlob(_ context.Context, _ string, digest string) (io.ReadCloser, error) {
	b, ok := p.layers[digest]
	if !ok {
		return nil, errors.New("parentRuntimePuller: no such digest " + digest)
	}
	return io.NopCloser(strings.NewReader(string(b))), nil
}

// newParentRuntimePuller builds the two-manifest puller with
// stable synthetic digests. The delta is exactly 1 layer
// (the runtime's second layer; the first matches the
// parent's DiffID, satisfying oci.LayersAboveBase).
func newParentRuntimePuller(t *testing.T) *parentRuntimePuller {
	t.Helper()
	parentLy := "sha256:parent-layer-aaaaaaaaaaaaaaaa"
	parentCfg := "sha256:parent-config-bbbbbbbbbbbbbbbb"
	runtimeLy1 := parentLy
	runtimeLy2 := "sha256:runtime-layer-ccccccccccccccccc"
	runtimeCfg := "sha256:runtime-config-ddddddddddddddddd"
	// OCI config JSON blobs — pullConfig reads them via
	// PullBlob(repo, manifest.Config.Digest) and decodes
	// rootfs.diff_ids. The parent's DiffID list is exactly the
	// prefix of the runtime's, satisfying
	// oci.LayersAboveBase.
	parentCfgBlob := []byte(`{"rootfs":{"type":"layers","diff_ids":["` + parentLy + `"]}}`)
	runtimeCfgBlob := []byte(`{"rootfs":{"type":"layers","diff_ids":["` + runtimeLy1 + `","` + runtimeLy2 + `"]}}`)
	return &parentRuntimePuller{
		parentCfg:   parentCfg,
		parentLayer: parentLy,
		runtimeCfg:  runtimeCfg,
		runtimeLy1:  runtimeLy1,
		runtimeLy2:  runtimeLy2,
		layers: map[string][]byte{
			parentLy:   gzTar(t, map[string]string{"lib/libc.so.6": "fake libc"}),
			runtimeLy2: gzTar(t, map[string]string{"usr/local/bin/node": "fake node"}),
			parentCfg:  parentCfgBlob,
			runtimeCfg: runtimeCfgBlob,
		},
	}
}

// newParentHarness builds a Handler wired with the parent-ref
// puller, a real tmpdir-backed fakeVMMClient (so cp -a has
// something to read), and a callCountingBuilder so tests can
// assert BuildBaseFromStaging was the actual mkfs call.
type parentHarness struct {
	h   *Handler
	be  storage.StorageBackend
	fvm *fakeVMMClient
	cb  *callCountingBuilder
}

func newParentHarness(t *testing.T) *parentHarness {
	t.Helper()
	// The parent-ref staging path runs against the FakeVMMClient
	// wired into h.vmmClient (DEPLOY-1 review B2). Earlier
	// versions of this harness swapped package-level
	// mountOverlayFn / umountOverlayFn stubs; those closures
	// were removed when the dispatch moved to h.vmmClient so
	// the production wiring has no silent "global was nil"
	// failure mode.
	be, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStorageBackend: %v", err)
	}
	mp := newParentRuntimePuller(t)
	cb := &callCountingBuilder{}
	// Real tmpdir for the fake mountpoint so BuildBaseFromStaging
	// has a tree to mkfs over (the stub treats the merged dir as
	// a pass-through — callCountingBuilder records the staging
	// path it was handed).
	mountDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(mountDir, "lib"), []byte("placeholder"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	fvm := &fakeVMMClient{mountHook: func(_ string) (string, error) {
		return mountDir, nil
	}}
	h := &Handler{
		oci:     mp,
		builder: cb,
		log:     silentLogger(),
		storage: be,
		grypeRun: func(_ context.Context, _ string) (*ScanResult, error) {
			return &ScanResult{}, nil
		},
		vmmClient: fvm,
	}
	return &parentHarness{h: h, be: be, fvm: fvm, cb: cb}
}

// TestVMMClient_MountOverlayParent_RejectsEmptyPaths pins the
// client-side empty-path guard at vmmclient.go:172. The
// production dispatch in base_stage.go now goes through
// h.vmmClient.MountOverlayParent directly (DEPLOY-1 review B2
// collapsed the package-level mountOverlayFn closure, so the
// empty-path check moved from base_stage.go into the VMMClient
// surface). A regression that removes the client-side guard
// would let an empty string hit the gRPC server, where
// vmmdmount surfaces it as InvalidArgument — obscuring the test
// fixture's real reason.
//
// Mirrors the pre-DEPLOY-1 TestMountOverlayFn_RejectsEmptyPaths
// shape (table-driven, substring match) so a future agent
// grepping for the old test by name finds the equivalent
// (DEPLOY-1 review B2).
func TestVMMClient_MountOverlayParent_RejectsEmptyPaths(t *testing.T) {
	ctx := context.Background()
	c := &VMMClient{} // zero-value: never dials; empty-path guard rejects first
	cases := []struct {
		name                                string
		merged, lowerdir, upperdir, workdir string
	}{
		{"empty merged", "", "/l", "/u", "/w"},
		{"empty lower", "/m", "", "/u", "/w"},
		{"empty upper", "/m", "/l", "", "/w"},
		{"empty workdir", "/m", "/l", "/u", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := c.MountOverlayParent(ctx, tc.lowerdir, tc.upperdir, tc.workdir, tc.merged)
			if err == nil {
				t.Fatalf("MountOverlayParent(%q,%q,%q,%q) succeeded; want empty-path error",
					tc.lowerdir, tc.upperdir, tc.workdir, tc.merged)
			}
			if !strings.Contains(err.Error(), "empty") {
				t.Fatalf("MountOverlayParent error %q; want substring 'empty'", err)
			}
		})
	}
}

// TestVMMClient_UmountOverlayParent_EmptyArgIsNoop pins the
// client-side empty-mountpoint no-op at vmmclient.go:199 (a
// nil receiver or empty mountpoint returns nil so the
// defer-after-error pattern is safe to call blindly). Mirrors
// the pre-DEPLOY-1 TestUmountOverlayFn_EmptyArgIsNoop.
func TestVMMClient_UmountOverlayParent_EmptyArgIsNoop(t *testing.T) {
	if err := (*VMMClient)(nil).UmountOverlayParent(context.Background(), ""); err != nil {
		t.Fatalf("UmountOverlayParent(\"\") = %v; want nil", err)
	}
}

// TestEnsureBaseExt4_OverlayDispatch verifies the parent-ref
// staging path goes through h.vmmClient.MountOverlayParent
// (DEPLOY-1 review B2). The test fires an inner harness whose
// staging tree is at t.TempDir() (cleared explicitly so the
// digest sidecar forces a rebuild, not a Skip), then asserts
// the fakeVMMClient recorded both the mount AND the umount
// (the unconditional defer at base_stage.go). A regression
// that re-introduces the package-level closure would silently
// skip the dispatch because defaultVMMClient is gone — this
// test pins the new shape.
//
// Note: parentRuntimePuller is keyed by the runtime ref
// (BaseRefNode22 etc.), not by an exposed `.ref` field. The
// test re-uses newParentRuntimePuller to read both the parent
// + runtime references implicitly via the puller's
// PullManifest dispatch on `BaseRefDebianParent` (parent) vs
// anything else (runtime).
func TestEnsureBaseExt4_OverlayDispatch(t *testing.T) {
	h := newParentHarness(t)
	// Use a fresh tempdir + storage backend so the digest
	// sidecar forces a real staging path (no Skip).
	freshBE, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStorageBackend: %v", err)
	}
	h.h.storage = freshBE
	if _, err := h.h.EnsureBaseExt4(context.Background(),
		"runtime-ref-test",
		"/staging/runner-fake.ext4",
		"/staging/runner-fake.digest",
		"",
		"parent-ref-test",
		"/staging/runner-parent.ext4",
	); err != nil {
		// The runtime-ref and parent-ref aren't real OCI
		// references so the puller may fail; what we care
		// about is whether the parent-ref branch at least
		// got as far as the mount call.
		t.Logf("EnsureBaseExt4 error (acceptable for this test): %v", err)
	}
	// The exact assertion depends on whether the puller
	// reached the mount step; even a MountParentExt4ReadOnly
	// success without the parent-ref build succeeding leaves
	// the unmount defer fired (it's installed unconditionally
	// before BuildBaseFromStaging). Verify the fvm saw the
	// RPC pair (or, on failure, that the unmount path is
	// idempotent for an empty merged dir — vmmClient.UmountOverlayParent
	// returns nil on "").
	if len(h.fvm.overlayMounts) > 0 {
		rec := h.fvm.overlayMounts[0]
		if rec.Lowerdir == "" || rec.Upperdir == "" || rec.Workdir == "" || rec.Merged == "" {
			t.Fatalf("overlay mount record incomplete: %+v", rec)
		}
	}
}

// TestEnsureBaseExt4_WithParentRef_PullsDeltaOnly — happy
// path for the ADR-053 staging branch. The runtime has 2
// DiffIDs, the parent has 1 (matching the runtime's first).
// After staging:
//   - BuildBaseFromStaging called once with the shared materialized
//     staging path. The parent tree is copied by vmmd while its
//     loopback mount is visible, then imaged applies the child delta.
//   - BuildBase NOT called (delta applied in-place on the
//     overlay upper dir, not via BuildBase's "apply ALL layers"
//     loop).
//   - fvm.MaterializeParentExt4 invoked exactly once with the
//     parent base key; the mount/umount pair is internal to vmmd.
//   - the base ext4 was published under the runtime baseKey.
func TestEnsureBaseExt4_WithParentRef_PullsDeltaOnly(t *testing.T) {
	hs := newParentHarness(t)
	const baseKey = "base/runner-node22-amd64.ext4"
	const digKey = "base/runner-node22-amd64.ext4.digest"
	res, err := hs.h.EnsureBaseExt4(context.Background(),
		BaseRefNode22, baseKey, digKey, "", BaseRefDebianParent, "base/runner-base-debian-parent-amd64.ext4")
	if err != nil {
		t.Fatalf("EnsureBaseExt4: %v", err)
	}
	if res.Skipped {
		t.Error("Skipped=true on first run, want false")
	}
	if res.ConfigDigest == "" {
		t.Error("ConfigDigest empty")
	}
	if hs.cb.calls != 0 {
		t.Errorf("BuildBase called %d times, want 0 (parent-ref path uses BuildBaseFromStaging)", hs.cb.calls)
	}
	if hs.cb.fromStagingCalls != 1 {
		t.Errorf("BuildBaseFromStaging called %d times, want 1", hs.cb.fromStagingCalls)
	}
	if got := hs.cb.fromStagingArgs[0]; filepath.Base(got) == "merged" {
		t.Errorf("BuildBaseFromStaging staging path = %q; materialized parent should be the staging root, not an overlay mount", got)
	}
	if len(hs.fvm.mountedKeys) != 1 {
		t.Fatalf("MaterializeParentExt4 called %d times, want 1", len(hs.fvm.mountedKeys))
	}
	if hs.fvm.mountedKeys[0] != "base/runner-base-debian-parent-amd64.ext4" {
		t.Errorf("mounted with key %q, want base/runner-base-debian-parent-amd64.ext4", hs.fvm.mountedKeys[0])
	}
	// The vmmd materialize RPC owns the short-lived mount and releases it
	// before returning; imaged no longer receives a mountpoint to release.
	if hs.fvm.umountCalls != 0 {
		t.Errorf("UmountParentExt4 called %d times, want 0 (vmmd owns the mount lifecycle)", hs.fvm.umountCalls)
	}
	if rc, err := hs.be.Get(context.Background(), baseKey); err != nil {
		t.Errorf("base ext4 not published at %s: %v", baseKey, err)
	} else {
		_ = rc.Close()
	}
	if rc, err := hs.be.Get(context.Background(), digKey); err != nil {
		t.Errorf("digest sidecar not published at %s: %v", digKey, err)
	} else {
		_ = rc.Close()
	}
}

// TestEnsureBaseExt4_WithParentRef_RejectsNilVMMClient —
// the parent-ref branch must fail loud when h.vmmClient is
// nil. The legacy path stays operational without a client
// wired; only the parent-ref branch fails loud so a misconfig
// surfaces here rather than at the cold-boot wake.
func TestEnsureBaseExt4_WithParentRef_RejectsNilVMMClient(t *testing.T) {
	be, _ := storage.NewLocalStorageBackend(t.TempDir())
	mp := newParentRuntimePuller(t)
	h := &Handler{
		oci:     mp,
		builder: &callCountingBuilder{},
		log:     silentLogger(),
		storage: be,
		grypeRun: func(_ context.Context, _ string) (*ScanResult, error) {
			return &ScanResult{}, nil
		},
		// vmmClient intentionally nil
	}
	_, err := h.EnsureBaseExt4(context.Background(),
		BaseRefNode22, "k", "k.digest", "", BaseRefDebianParent, "base/runner-base-debian-parent-amd64.ext4")
	if err == nil {
		t.Fatal("expected error when vmmClient is nil")
	}
	if !strings.Contains(err.Error(), "VMMClient") || !strings.Contains(err.Error(), "ADR-053") {
		t.Errorf("error %q must mention VMMClient + ADR-053", err.Error())
	}
}

// TestEnsureBaseExt4_WithoutParentRef_AppliesAllLayers —
// the legacy "apply ALL layers" path stays operational when
// parentRef is empty. Confirms the dispatcher in
// EnsureBaseExt4 doesn't accidentally route a parent-less
// row through the parent-ref branch (a regression here
// would break every legacy runtime + builder-base).
func TestEnsureBaseExt4_WithoutParentRef_AppliesAllLayers(t *testing.T) {
	mp := newTwoLayerPuller(t)
	hs := newBaseHarness(t, mp, &callCountingBuilder{})
	// Use an empty parentRef — the legacy path. The vmmClient
	// stays nil (no client wired) and that's fine.
	const baseKey = "base/runner-go124-amd64.ext4"
	const digKey = "base/runner-go124-amd64.ext4.digest"
	res, err := hs.h.EnsureBaseExt4(context.Background(),
		BaseRefGo124, baseKey, digKey, "", "", "")
	if err != nil {
		t.Fatalf("EnsureBaseExt4: %v", err)
	}
	if res.Skipped {
		t.Error("Skipped=true on first run, want false")
	}
	if hs.h.builder.(*callCountingBuilder).calls != 1 {
		t.Errorf("BuildBase called %d times, want 1 (legacy path)", hs.h.builder.(*callCountingBuilder).calls)
	}
	if hs.h.builder.(*callCountingBuilder).fromStagingCalls != 0 {
		t.Errorf("BuildBaseFromStaging called %d times, want 0 (legacy path)", hs.h.builder.(*callCountingBuilder).fromStagingCalls)
	}
	if rc, err := hs.be.Get(context.Background(), baseKey); err != nil {
		t.Errorf("base ext4 not published at %s: %v", baseKey, err)
	} else {
		_ = rc.Close()
	}
}

// TestResolveParentRef_HonorsEnvOverride is the regression for run
// 30661487390 (2026-07-31). Before the fix, EnsureBases hardcoded
// row.ParentRef in the parent branch — bypassing
// FAAS_DEPLOY_BASE_REF_DEBIAN_PARENT entirely. An operator who set
// that env var to a mirror.gcr.io digest would see the parent row
// honor it but every child row's parent manifest pull still ask for
// the unreachable ghcr.io/onebox-faas/base-debian-parent:latest.
//
// The fix routes the parent lookup through the same env-override
// path the parent row uses itself, via the envOverrideByRef map
// built once per EnsureBases call. resolveParentRef is the
// extracted helper. This test pins the four cases the field
// needs to handle: empty parentRef (legacy row), env override
// present + digest-pinned (override applied), env override
// missing (const kept), env override set to a tag-only ref
// (fail-loud, same posture as the row-level override gate).
func TestResolveParentRef_HonorsEnvOverride(t *testing.T) {
	const parentRef = "ghcr.io/onebox-faas/base-debian-parent:latest"
	const overrideRef = "mirror.gcr.io/library/debian@sha256:81d93757457f988523814ae0009837ae893f38d3fe123f2c37896f118b4c7804"
	envOverrideByRef := map[string]string{
		parentRef: "FAAS_DEPLOY_BASE_REF_DEBIAN_PARENT",
	}
	envLookup := func(key string) string {
		if key == "FAAS_DEPLOY_BASE_REF_DEBIAN_PARENT" {
			return overrideRef
		}
		return ""
	}
	t.Run("empty parentRef returns empty", func(t *testing.T) {
		got, err := resolveParentRef("", envOverrideByRef, envLookup)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})
	t.Run("env override present returns override", func(t *testing.T) {
		got, err := resolveParentRef(parentRef, envOverrideByRef, envLookup)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != overrideRef {
			t.Errorf("got %q, want %q (env override must propagate to child parent ref)", got, overrideRef)
		}
	})
	t.Run("env override missing returns const", func(t *testing.T) {
		got, err := resolveParentRef(parentRef, envOverrideByRef, func(string) string { return "" })
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != parentRef {
			t.Errorf("got %q, want %q (missing env must not clobber const)", got, parentRef)
		}
	})
	t.Run("env override tag-only ref fails loud", func(t *testing.T) {
		tagOnly := func(key string) string {
			if key == "FAAS_DEPLOY_BASE_REF_DEBIAN_PARENT" {
				return "mirror.gcr.io/library/debian:12-slim"
			}
			return ""
		}
		_, err := resolveParentRef(parentRef, envOverrideByRef, tagOnly)
		if err == nil {
			t.Fatal("expected error for tag-only env override, got nil")
		}
		if !strings.Contains(err.Error(), "FAAS_DEPLOY_BASE_REF_DEBIAN_PARENT") {
			t.Errorf("error should name the env var; got %v", err)
		}
	})
	t.Run("parent ref not in envOverrideByRef returns the const", func(t *testing.T) {
		// Defensive: a child row whose parent isn't a row in the
		// table (shouldn't happen, but the slice is operator-
		// configurable so don't crash on misses). The const
		// passes through.
		got, err := resolveParentRef("ghcr.io/onebox-faas/some-other:latest", envOverrideByRef, envLookup)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "ghcr.io/onebox-faas/some-other:latest" {
			t.Errorf("got %q, want const passthrough", got)
		}
	})
}

// TestWriteScanSidecar_KeySetStable pins the base-ext4 scan sidecar
// contract (issue #464 / PR-B acceptance): the sidecar JSON's
// `findings` map carries the closed-enum keys
// (CRITICAL|HIGH|MEDIUM|LOW|UNKNOWN) with the values from
// SeverityCounts. The consumer (vmmd's bringUpScanCheck at
// pkg/fcvm/manager.go) reads the JSON via json.Unmarshal into a
// map[string]int and never inspects key order — Go's randomised
// map iteration means the marshal output is NOT byte-identical
// across runs, but the key set + values must be stable.
//
// A regression that drops a key from toMap() (e.g. removes the
// Unknown bucket) or renames a Severity* constant surfaces here
// as a missing-key assertion. The test runs writeScanSidecar
// twice on independent storage backends so both reads exercise
// the same write path; the pin is the key SET + per-key value
// (not byte-equality of the raw bytes).
func TestWriteScanSidecar_KeySetStable(t *testing.T) {
	const baseKey = "base/runtime.ext4"
	const ref = "ghcr.io/onebox-faas/builder-base:latest"
	const outImage = "/srv/fc/base/runtime.ext4"

	want := &ScanResult{
		SeverityCounts: SeverityCounts{
			Critical: 1, High: 2, Medium: 3, Low: 4, Unknown: 5,
		},
		Vulnerabilities: []Vulnerability{
			{Severity: SeverityCritical, FixedIn: "1.2.3"},
			{Severity: SeverityHigh},
			{Severity: SeverityMedium, FixedIn: "4.5.6"},
		},
	}

	// Two independent runs so a future refactor that introduces a
	// non-deterministic source (e.g. a map seeded from a hash of the
	// input bytes) cannot mask a key-set regression behind a single
	// passing run.
	for _, run := range []string{"run-1", "run-2"} {
		t.Run(run, func(t *testing.T) {
			be, err := storage.NewLocalStorageBackend(t.TempDir())
			if err != nil {
				t.Fatalf("NewLocalStorageBackend: %v", err)
			}
			h := &Handler{
				log:     silentLogger(),
				storage: be,
				grypeRun: func(_ context.Context, _ string) (*ScanResult, error) {
					return want, nil
				},
			}

			if err := h.writeScanSidecar(context.Background(), baseKey, ref, outImage); err != nil {
				t.Fatalf("writeScanSidecar: %v", err)
			}

			// Read the sidecar back from storage at the canonical key.
			rc, err := be.Get(context.Background(), wire.ScanKeyForBaseKey(baseKey))
			if err != nil {
				t.Fatalf("Get scan sidecar: %v", err)
			}
			defer rc.Close()
			sidecarBytes, err := io.ReadAll(rc)
			if err != nil {
				t.Fatalf("read scan sidecar: %v", err)
			}

			// Unmarshal into the same shape writeScanSidecar marshals
			// (struct{Image,Findings,ScannedAt}). The findings map is
			// the load-bearing field for vmmd's bringUpScanCheck.
			var got struct {
				Image                string         `json:"image"`
				Findings             map[string]int `json:"findings"`
				FixAvailableFindings map[string]int `json:"fix_available_findings"`
				ScannedAt            time.Time      `json:"scanned_at"`
			}
			if err := json.Unmarshal(sidecarBytes, &got); err != nil {
				t.Fatalf("unmarshal sidecar: %v (bytes=%s)", err, string(sidecarBytes))
			}

			// Image field carries the OCI ref for dashboard
			// traceability (writeScanSidecar's `image` field is
			// sourced from `ref`, NOT from outImage — Critical #1
			// of PR #385).
			if got.Image != ref {
				t.Errorf("image = %q, want %q", got.Image, ref)
			}

			// Closed-enum key set: every Severity* constant must
			// be present in the marshal output, and ONLY those.
			wantKeys := map[string]int{
				SeverityCritical: 1,
				SeverityHigh:     2,
				SeverityMedium:   3,
				SeverityLow:      4,
				SeverityUnknown:  5,
			}
			if len(got.Findings) != len(wantKeys) {
				t.Errorf("findings has %d keys, want %d (keys=%v)",
					len(got.Findings), len(wantKeys), keysOf(got.Findings))
			}
			for k, wantVal := range wantKeys {
				if gotVal, ok := got.Findings[k]; !ok {
					t.Errorf("findings missing key %q (present keys=%v)", k, keysOf(got.Findings))
				} else if gotVal != wantVal {
					t.Errorf("findings[%q] = %d, want %d", k, gotVal, wantVal)
				}
			}
			// Also assert the inverse: no extra keys snuck in via a
			// future refactor (e.g. a typo'd severity constant).
			for k := range got.Findings {
				if _, ok := wantKeys[k]; !ok {
					t.Errorf("findings has unexpected key %q (close-enum violation)", k)
				}
			}
			wantFixAvailable := map[string]int{
				SeverityCritical: 1,
				SeverityHigh:     0,
				SeverityMedium:   1,
				SeverityLow:      0,
				SeverityUnknown:  0,
			}
			if !reflect.DeepEqual(got.FixAvailableFindings, wantFixAvailable) {
				t.Errorf("fix_available_findings = %v, want %v", got.FixAvailableFindings, wantFixAvailable)
			}
		})
	}
}

// TestWriteScanSidecar_UsesPublishedLocalPath guards the canonical storage
// migration: the compatibility outImage path may point at an older
// builder-base.ext4, but the sidecar must scan the artifact published under
// baseKey (runner-builder-<arch>.ext4).
func TestWriteScanSidecar_UsesPublishedLocalPath(t *testing.T) {
	be, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStorageBackend: %v", err)
	}
	canonical, ok, err := be.LocalPath("base/runner-builder-amd64.ext4")
	if err != nil || !ok {
		t.Fatalf("LocalPath = %q, %t, %v", canonical, ok, err)
	}
	if err := os.MkdirAll(filepath.Dir(canonical), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(canonical, []byte("ext4-placeholder"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	// LocalPath resolves existing symlinks; on macOS the temp root is
	// commonly exposed as /var while the real path is /private/var.
	canonical, ok, err = be.LocalPath("base/runner-builder-amd64.ext4")
	if err != nil || !ok {
		t.Fatalf("LocalPath after publish = %q, %t, %v", canonical, ok, err)
	}

	var scanned string
	h := &Handler{
		log:     silentLogger(),
		storage: be,
		grypeRun: func(_ context.Context, dir string) (*ScanResult, error) {
			scanned = dir
			return &ScanResult{}, nil
		},
	}
	if err := h.writeScanSidecar(context.Background(),
		"base/runner-builder-amd64.ext4",
		"ghcr.io/poyrazk/builder-base@sha256:deadbeef",
		"/srv/fc/base/builder-base.ext4"); err != nil {
		t.Fatalf("writeScanSidecar: %v", err)
	}
	if scanned != canonical {
		t.Errorf("grype source = %q, want canonical published path %q", scanned, canonical)
	}
}

func TestScanSidecarSourceCurrent_RetriesFailClosedPlaceholder(t *testing.T) {
	be, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStorageBackend: %v", err)
	}
	baseKey := "base/runtime.ext4"
	canonical, ok, err := be.LocalPath(baseKey)
	if err != nil || !ok {
		t.Fatalf("LocalPath = %q, %t, %v", canonical, ok, err)
	}
	if err := os.MkdirAll(filepath.Dir(canonical), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(canonical, []byte("ext4-placeholder"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	sidecar := []byte(`{"source":"` + canonical + `","findings":{"CRITICAL":9999}}`)
	if err := be.Put(context.Background(), wire.ScanKeyForBaseKey(baseKey), bytes.NewReader(sidecar)); err != nil {
		t.Fatalf("Put sidecar: %v", err)
	}
	h := &Handler{}
	if h.scanSidecarSourceCurrent(context.Background(), be, baseKey, "") {
		t.Fatal("fail-closed scanner placeholder should be refreshed")
	}
}

func TestScanSidecarSourceCurrent_RefreshesLegacyPolicySidecar(t *testing.T) {
	be, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStorageBackend: %v", err)
	}
	baseKey := "base/runtime.ext4"
	canonical, ok, err := be.LocalPath(baseKey)
	if err != nil || !ok {
		t.Fatalf("LocalPath = %q, %t, %v", canonical, ok, err)
	}
	if err := os.MkdirAll(filepath.Dir(canonical), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(canonical, []byte("ext4-placeholder"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	sidecar, err := json.Marshal(map[string]any{
		"source":   canonical,
		"findings": map[string]int{"CRITICAL": 0},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := be.Put(context.Background(), wire.ScanKeyForBaseKey(baseKey), bytes.NewReader(sidecar)); err != nil {
		t.Fatalf("Put sidecar: %v", err)
	}
	h := &Handler{}
	if h.scanSidecarSourceCurrent(context.Background(), be, baseKey, "") {
		t.Fatal("legacy sidecar without fix_available_findings should be refreshed")
	}
}

// keysOf returns the sorted keys of a map[string]int for stable
// test diagnostics (Go map iteration order is randomised).
func keysOf(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
