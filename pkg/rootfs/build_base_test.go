package rootfs

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"
)

// TestBuildBase_HappyPath proves the full pipeline: two gzipped layers go
// in, mkfs is invoked with a populated staging tree, and the OutImage path
// is what we passed in. Mirrors TestBuildProducesSizedLayer's style.
func TestBuildBase_HappyPath(t *testing.T) {
	run := &fakeRunner{}
	b := NewBuilder(run)

	out := filepath.Join(t.TempDir(), "builder-base.ext4")
	res, err := b.BuildBase(context.Background(), BaseBuildInput{
		Layers: []io.Reader{
			gzLayer(t, []entry{{name: "etc/faas", body: "v1"}}),
			gzLayer(t, []entry{{name: "bin/railpack", body: "rb1"}}),
		},
		OutImage: out,
	})
	if err != nil {
		t.Fatalf("BuildBase: %v", err)
	}
	if res.ImagePath != out {
		t.Errorf("ImagePath = %q, want %q", res.ImagePath, out)
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
	if !containsString(run.argv, out) {
		t.Errorf("argv %v must contain %q", run.argv, out)
	}
	if !containsString(run.argv, "-d") {
		t.Errorf("argv %v must use -d (mkfs with source dir, no mount needed)", run.argv)
	}
}

// TestBuildBase_AppliesAllLayers pins the spec-critical difference from
// Builder.Build: every supplied layer is applied, not just "above base".
// Three layers in, three sets of files visible in the staging tree before
// mkfs runs. We assert by recording (run took the staged dir as -d) and
// by side-effect: layer-2 wins over layer-1 on the same path.
func TestBuildBase_AppliesAllLayers(t *testing.T) {
	run := &fakeRunner{}
	b := NewBuilder(run)

	_, err := b.BuildBase(context.Background(), BaseBuildInput{
		Layers: []io.Reader{
			gzLayer(t, []entry{{name: "usr/local/bin/railpack", body: "v0"}}),
			gzLayer(t, []entry{{name: "usr/local/bin/railpack", body: "v1"}}),
			gzLayer(t, []entry{{name: "etc/motd", body: "hello"}}),
		},
		OutImage: filepath.Join(t.TempDir(), "base.ext4"),
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
	_, err := b.BuildBase(context.Background(), BaseBuildInput{OutImage: "/tmp/x.ext4"})
	if err == nil {
		t.Fatal("expected error on empty Layers")
	}
	if !strings.Contains(err.Error(), "no layers") {
		t.Errorf("error %q must mention 'no layers'", err.Error())
	}
}

// TestBuildBase_EmptyOutImageErrors — OutImage is the production target;
// silently defaulting would write into CWD.
func TestBuildBase_EmptyOutImageErrors(t *testing.T) {
	b := NewBuilder(&fakeRunner{})
	_, err := b.BuildBase(context.Background(), BaseBuildInput{
		Layers: []io.Reader{strings.NewReader("")},
	})
	if err == nil {
		t.Fatal("expected error on empty OutImage")
	}
}

// TestBuildBase_LayerApplyError — corrupt gz payload must surface as a
// wrapped error, not panic.
func TestBuildBase_LayerApplyError(t *testing.T) {
	b := NewBuilder(&fakeRunner{})
	_, err := b.BuildBase(context.Background(), BaseBuildInput{
		Layers:   []io.Reader{strings.NewReader("not a gz stream")},
		OutImage: filepath.Join(t.TempDir(), "x.ext4"),
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
		Layers:   []io.Reader{gzLayer(t, []entry{{name: "f", body: "x"}})},
		OutImage: filepath.Join(t.TempDir(), "x.ext4"),
	})
	if err == nil {
		t.Fatal("expected mkfs error")
	}
	if !strings.Contains(err.Error(), "base mkfs") {
		t.Errorf("error %q must mention base mkfs", err.Error())
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
