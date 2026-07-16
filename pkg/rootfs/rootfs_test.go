package rootfs

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// --- tar helpers -----------------------------------------------------------

type entry struct {
	name     string
	body     string
	typeflag byte
	linkname string
}

func gzLayer(t *testing.T, entries []entry) io.Reader {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for _, e := range entries {
		flag := e.typeflag
		if flag == 0 {
			flag = tar.TypeReg
		}
		hdr := &tar.Header{Name: e.name, Mode: 0o644, Typeflag: flag, Linkname: e.linkname}
		if flag == tar.TypeReg {
			hdr.Size = int64(len(e.body))
		}
		if flag == tar.TypeDir {
			hdr.Mode = 0o755
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if flag == tar.TypeReg {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf
}

// --- layer application -----------------------------------------------------

func TestApplyLayerBasic(t *testing.T) {
	dst := t.TempDir()
	layer := gzLayer(t, []entry{
		{name: "app/", typeflag: tar.TypeDir},
		{name: "app/index.js", body: "console.log('hi')"},
		{name: "app/link", typeflag: tar.TypeSymlink, linkname: "index.js"},
	})
	if err := ApplyLayerGz(dst, layer); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dst, "app", "index.js"))
	if err != nil || string(got) != "console.log('hi')" {
		t.Fatalf("file content = %q err=%v", got, err)
	}
	if fi, err := os.Lstat(filepath.Join(dst, "app", "link")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("symlink not created: %v", err)
	}
}

func TestApplyLayerStacking(t *testing.T) {
	dst := t.TempDir()
	if err := ApplyLayerGz(dst, gzLayer(t, []entry{{name: "f", body: "v1"}})); err != nil {
		t.Fatal(err)
	}
	if err := ApplyLayerGz(dst, gzLayer(t, []entry{{name: "f", body: "v2"}})); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(dst, "f"))
	if string(got) != "v2" {
		t.Errorf("upper layer should win: got %q", got)
	}
}

func TestApplyLayerWhiteout(t *testing.T) {
	dst := t.TempDir()
	if err := ApplyLayerGz(dst, gzLayer(t, []entry{{name: "a"}, {name: "b"}})); err != nil {
		t.Fatal(err)
	}
	// Upper layer whiteouts "a".
	if err := ApplyLayerGz(dst, gzLayer(t, []entry{{name: ".wh.a"}})); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dst, "a")); !os.IsNotExist(err) {
		t.Errorf("whiteout did not remove 'a': %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "b")); err != nil {
		t.Errorf("whiteout wrongly removed 'b': %v", err)
	}
}

func TestApplyLayerOpaqueWhiteout(t *testing.T) {
	dst := t.TempDir()
	if err := ApplyLayerGz(dst, gzLayer(t, []entry{
		{name: "d/", typeflag: tar.TypeDir}, {name: "d/x"}, {name: "d/y"},
	})); err != nil {
		t.Fatal(err)
	}
	if err := ApplyLayerGz(dst, gzLayer(t, []entry{
		{name: "d/.wh..wh..opq"}, {name: "d/z", body: "new"},
	})); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dst, "d", "x")); !os.IsNotExist(err) {
		t.Error("opaque whiteout should have cleared d/x")
	}
	if _, err := os.Stat(filepath.Join(dst, "d", "z")); err != nil {
		t.Errorf("d/z from same layer should survive: %v", err)
	}
}

func TestApplyLayerRejectsPathEscape(t *testing.T) {
	dst := t.TempDir()
	for _, name := range []string{"../evil", "a/../../evil", "/abs/evil"} {
		err := ApplyLayerGz(dst, gzLayer(t, []entry{{name: name, body: "x"}}))
		if err == nil {
			t.Errorf("path %q should be rejected as escaping staging root", name)
		}
	}
	// Nothing should have been written outside dst.
	if _, err := os.Stat(filepath.Join(filepath.Dir(dst), "evil")); !os.IsNotExist(err) {
		t.Error("an escaping entry wrote outside the staging root")
	}
}

// --- sizing / caps ---------------------------------------------------------

func TestPaddedSizeMB(t *testing.T) {
	if got := PaddedSizeMB(0); got != MinLayerMB {
		t.Errorf("empty content -> %d, want floor %d", got, MinLayerMB)
	}
	if got := PaddedSizeMB(100 * mib); got < 104 { // 100 + 10% slack
		t.Errorf("100MB content -> %d MB, want >= 104", got)
	}
	// Monotonic: more content never shrinks the image.
	prev := 0
	for c := int64(0); c <= 500*mib; c += 50 * mib {
		s := PaddedSizeMB(c)
		if s < prev {
			t.Fatalf("size not monotonic at %d bytes: %d < %d", c, s, prev)
		}
		prev = s
	}
}

func TestCheckCapEnforcesPlanLimit(t *testing.T) {
	free := api.MustLimitsFor(api.PlanFree) // 256 MB cap
	if _, err := CheckCap(free, 10*mib); err != nil {
		t.Errorf("small app under Free cap should pass: %v", err)
	}
	_, err := CheckCap(free, 400*mib)
	if err == nil {
		t.Fatal("400 MB app should exceed Free 256 MB cap")
	}
	var prob *api.Problem
	if !errors.As(err, &prob) || prob.Code != api.CodeAppLayerTooBig {
		t.Errorf("expected app_layer_too_large problem, got %v", err)
	}
}

// --- full build ------------------------------------------------------------

type fakeRunner struct{ argv []string }

func (f *fakeRunner) Run(_ context.Context, argv []string) error { f.argv = argv; return nil }

func TestBuildProducesSizedLayer(t *testing.T) {
	// A fake guest-init binary on disk.
	gi := filepath.Join(t.TempDir(), "guest-init")
	if err := os.WriteFile(gi, bytes.Repeat([]byte{0}, 1024), 0o755); err != nil {
		t.Fatal(err)
	}
	run := &fakeRunner{}
	b := NewBuilder(run)

	out := filepath.Join(t.TempDir(), "layer.ext4")
	res, err := b.Build(context.Background(), BuildInput{
		Layers: []io.Reader{gzLayer(t, []entry{
			{name: "app/", typeflag: tar.TypeDir},
			{name: "app/server.js", body: "require('http')"},
		})},
		Manifest:      api.AppManifest{Entrypoint: []string{"node", "app/server.js"}},
		GuestInitPath: gi,
		Plan:          api.PlanFree,
		OutImage:      out,
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if res.SizeMB < MinLayerMB {
		t.Errorf("size %d below floor", res.SizeMB)
	}
	// mkfs was invoked with the right output + size.
	line := ""
	for _, a := range run.argv {
		line += a + " "
	}
	if run.argv[0] != "mkfs.ext4" {
		t.Errorf("expected mkfs.ext4, got %v", run.argv)
	}
	if run.argv[len(run.argv)-2] != out {
		t.Errorf("mkfs output arg = %q, want %q", run.argv[len(run.argv)-2], out)
	}
}

func TestBuildRejectsOversizeApp(t *testing.T) {
	gi := filepath.Join(t.TempDir(), "guest-init")
	if err := os.WriteFile(gi, []byte{0}, 0o755); err != nil {
		t.Fatal(err)
	}
	// A layer bigger than the Free 256 MB cap.
	big := make([]byte, 300*mib)
	run := &fakeRunner{}
	b := NewBuilder(run)
	_, err := b.Build(context.Background(), BuildInput{
		Layers:        []io.Reader{gzLayer(t, []entry{{name: "blob", body: string(big)}})},
		Manifest:      api.AppManifest{Entrypoint: []string{"x"}},
		GuestInitPath: gi,
		Plan:          api.PlanFree,
		OutImage:      filepath.Join(t.TempDir(), "layer.ext4"),
	})
	if err == nil {
		t.Fatal("oversize app should fail the build")
	}
	if run.argv != nil {
		t.Error("mkfs must not run when the cap check fails")
	}
}

func TestBuildInjectsManifestAndInit(t *testing.T) {
	// Verify injection by intercepting the staging dir via a runner that reads
	// the mkfs `-d <dir>` argument.
	gi := filepath.Join(t.TempDir(), "guest-init")
	if err := os.WriteFile(gi, []byte("INIT"), 0o755); err != nil {
		t.Fatal(err)
	}

	var staging string
	capture := runnerFunc(func(_ context.Context, argv []string) error {
		for i, a := range argv {
			if a == "-d" && i+1 < len(argv) {
				staging = argv[i+1]
			}
		}
		// Read injected files before Build's deferred cleanup removes them.
		manifest, err := os.ReadFile(filepath.Join(staging, "etc", "faas", "app.json"))
		if err != nil {
			t.Errorf("app.json not injected: %v", err)
		}
		if !bytes.Contains(manifest, []byte(`"node"`)) {
			t.Errorf("manifest missing entrypoint: %s", manifest)
		}
		init, err := os.ReadFile(filepath.Join(staging, "sbin", "init"))
		if err != nil || string(init) != "INIT" {
			t.Errorf("guest-init not injected as /sbin/init: %v", err)
		}
		return nil
	})

	b := NewBuilder(capture)
	_, err := b.Build(context.Background(), BuildInput{
		Layers:        []io.Reader{gzLayer(t, []entry{{name: "x", body: "y"}})},
		Manifest:      api.AppManifest{Entrypoint: []string{"node", "x"}},
		GuestInitPath: gi,
		Plan:          api.PlanHobby,
		OutImage:      filepath.Join(t.TempDir(), "layer.ext4"),
	})
	if err != nil {
		t.Fatal(err)
	}
}

type runnerFunc func(context.Context, []string) error

func (f runnerFunc) Run(ctx context.Context, argv []string) error { return f(ctx, argv) }
