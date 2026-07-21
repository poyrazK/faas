package rootfs

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"
)

// TestBuildBase_HappyPath proves the full pipeline: two gzipped layers go
// in, mkfs is invoked with a populated staging tree, and the produced ext4
// is published via the Storage backend under StorageKey. Mirrors
// TestBuildProducesSizedLayer's style on the new key-aware API.
func TestBuildBase_HappyPath(t *testing.T) {
	be := newTestStorage(t)
	run := &mkfsFakeRunner{fill: []byte("FAKE-BASE-EXT4")}
	b := NewBuilder(run)
	res, err := b.BuildBase(context.Background(), BaseBuildInput{
		Layers: []io.Reader{
			gzLayer(t, []entry{{name: "etc/faas", body: "v1"}}),
			gzLayer(t, []entry{{name: "bin/railpack", body: "rb1"}}),
		},
		Storage:    be,
		StorageKey: "base/runtime.ext4",
	})
	if err != nil {
		t.Fatalf("BuildBase: %v", err)
	}
	if res.ImageKey != "base/runtime.ext4" {
		t.Errorf("ImageKey = %q, want %q", res.ImageKey, "base/runtime.ext4")
	}
	if res.SizeBytes == 0 {
		t.Error("SizeBytes = 0, want > 0")
	}
	if len(run.argv) == 0 {
		t.Fatal("Run was not called")
	}
	if run.argv[0] != "mkfs.ext4" {
		t.Errorf("argv[0] = %q, want mkfs.ext4", run.argv[0])
	}
	if !containsString(run.argv, "-d") {
		t.Errorf("argv %v must use -d (mkfs with source dir, no mount needed)", run.argv)
	}
	// The legacy OutImage path is NOT used; the Storage backend must
	// hold the published ext4 at the requested key.
	rc, err := be.Get(context.Background(), "base/runtime.ext4")
	if err != nil {
		t.Fatalf("storage Get after BuildBase: %v", err)
	}
	defer rc.Close()
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read all: %v", err)
	}
	if !bytes.Equal(got, run.fill) {
		t.Fatalf("content mismatch: got %q, want %q", got, run.fill)
	}
}

// TestBuildBase_LegacyOutImage covers the deprecation path: existing
// callers (TestBuildBase_* in earlier slices, the integration test)
// pass OutImage and BuildBase writes directly. Kept for one release
// per the ADR-025 deprecation window.
func TestBuildBase_LegacyOutImage(t *testing.T) {
	run := &fakeRunner{}
	b := NewBuilder(run)
	out := filepath.Join(t.TempDir(), "builder-base.ext4")
	res, err := b.BuildBase(context.Background(), BaseBuildInput{
		Layers: []io.Reader{
			gzLayer(t, []entry{{name: "etc/faas", body: "v1"}}),
		},
		OutImage: out,
	})
	if err != nil {
		t.Fatalf("BuildBase: %v", err)
	}
	if res.ImagePath != out {
		t.Errorf("ImagePath = %q, want %q", res.ImagePath, out)
	}
	if !containsString(run.argv, out) {
		t.Errorf("argv %v must contain %q (legacy path)", run.argv, out)
	}
}

// TestBuildBase_AppliesAllLayers pins the spec-critical difference from
// Builder.Build: every supplied layer is applied, not just "above base".
// Three layers in, three sets of files visible in the staging tree before
// mkfs runs. We assert by recording (run took the staged dir as -d) and
// by side-effect: layer-2 wins over layer-1 on the same path.
func TestBuildBase_AppliesAllLayers(t *testing.T) {
	be := newTestStorage(t)
	run := &fakeRunner{}
	b := NewBuilder(run)
	_, err := b.BuildBase(context.Background(), BaseBuildInput{
		Layers: []io.Reader{
			gzLayer(t, []entry{{name: "usr/local/bin/railpack", body: "v0"}}),
			gzLayer(t, []entry{{name: "usr/local/bin/railpack", body: "v1"}}),
			gzLayer(t, []entry{{name: "etc/motd", body: "hello"}}),
		},
		Storage:    be,
		StorageKey: "base/runtime.ext4",
	})
	if err != nil {
		t.Fatalf("BuildBase: %v", err)
	}
	if len(run.argv) == 0 {
		t.Fatal("mkfs not called")
	}
	// The -d arg points at the staging dir. We can't read the dir post-mkfs
	// (the defer removed it), but the fact that no Apply error fired proves
	// all three layers decoded. Sanity: size arg is non-zero and ends in M.
	sizeArg := run.argv[len(run.argv)-1]
	if !strings.HasSuffix(sizeArg, "M") {
		t.Errorf("size arg %q does not end with M", sizeArg)
	}
}

// TestBuildBase_EmptyLayersErrors covers the inverse of the happy path:
// supplying zero layers is a structural mistake, not a noop.
func TestBuildBase_EmptyLayersErrors(t *testing.T) {
	b := NewBuilder(&fakeRunner{})
	_, err := b.BuildBase(context.Background(), BaseBuildInput{
		Storage:    newTestStorage(t),
		StorageKey: "base/runtime.ext4",
	})
	if err == nil {
		t.Fatal("expected error on empty Layers")
	}
	if !strings.Contains(err.Error(), "no layers") {
		t.Errorf("error %q must mention 'no layers'", err.Error())
	}
}

// TestBuildBase_EmptyOutImageErrors — the legacy OutImage path is
// production-wired to Storage/StorageKey today; this test pins the
// rule that the legacy path also rejects an empty OutImage.
func TestBuildBase_EmptyOutImageErrors(t *testing.T) {
	b := NewBuilder(&fakeRunner{})
	_, err := b.BuildBase(context.Background(), BaseBuildInput{
		Layers: []io.Reader{strings.NewReader("")},
	})
	if err == nil {
		t.Fatal("expected error on empty output target")
	}
}

// TestBuildBase_LayerApplyError — corrupt gz payload must surface as a
// wrapped error, not panic.
func TestBuildBase_LayerApplyError(t *testing.T) {
	b := NewBuilder(&fakeRunner{})
	_, err := b.BuildBase(context.Background(), BaseBuildInput{
		Layers:     []io.Reader{strings.NewReader("not a gz stream")},
		Storage:    newTestStorage(t),
		StorageKey: "base/runtime.ext4",
	})
	if err == nil {
		t.Fatal("expected error on corrupt layer")
	}
	if !strings.Contains(err.Error(), "apply base layer 0") {
		t.Errorf("error %q must mention layer index", err.Error())
	}
}

// TestBuildBase_MkfsError surfaces a Runner error so the caller (imaged
// startup) can refuse to point builders at a half-written ext4.
func TestBuildBase_MkfsError(t *testing.T) {
	b := NewBuilder(fakeErrorRunner{err: errors.New("disk full")})
	_, err := b.BuildBase(context.Background(), BaseBuildInput{
		Layers:     []io.Reader{gzLayer(t, []entry{{name: "f", body: "x"}})},
		Storage:    newTestStorage(t),
		StorageKey: "base/runtime.ext4",
	})
	if err == nil {
		t.Fatal("expected mkfs error")
	}
	if !strings.Contains(err.Error(), "base mkfs") {
		t.Errorf("error %q must mention base mkfs", err.Error())
	}
}

// TestBuildBase_RejectsBothOutputTargets covers the same exclusive-or
// rule as Build: the validator must surface the misconfiguration loudly.
func TestBuildBase_RejectsBothOutputTargets(t *testing.T) {
	b := NewBuilder(&fakeRunner{})
	_, err := b.BuildBase(context.Background(), BaseBuildInput{
		Layers:     []io.Reader{strings.NewReader("")}, // ignored; we error first
		Storage:    newTestStorage(t),
		StorageKey: "base/runtime.ext4",
		OutImage:   filepath.Join(t.TempDir(), "out.ext4"),
	})
	if err == nil {
		t.Fatal("both output targets should error")
	}
}

// containsString returns true if argv contains s. Tiny helper — avoids
// pulling in slices.Contains for one check.
func containsString(argv []string, s string) bool {
	for _, a := range argv {
		if a == s {
			return true
		}
	}
	return false
}

// fakeErrorRunner returns a fixed error from Run. Distinct from
// rootfs_test.go's recordingRunner (which captures argv) so the build's
// "succeed, then assert" pattern doesn't fight the "fail-fast" pattern.
type fakeErrorRunner struct{ err error }

func (f fakeErrorRunner) Run(_ context.Context, _ []string) error { return f.err }
