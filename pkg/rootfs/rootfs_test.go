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
	if got := PaddedSizeMB(100 * mib); got < 110 { // 100 + perAppSlackPct% slack
		t.Errorf("100MB content -> %d MB, want >= 110", got)
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

func TestBasePaddedSizeMB(t *testing.T) {
	// Empty content still gets a floor — same as PaddedSizeMB.
	if got := BasePaddedSizeMB(0, 0); got != MinLayerMB {
		t.Errorf("empty content -> %d, want floor %d", got, MinLayerMB)
	}
	// Tree with NO small files: once percentage slack dominates both absolute
	// floors, BasePaddedSizeMB matches PaddedSizeMB (smallFileSlackPct
	// contributes 0 when smallRatio=0).
	for c := int64(100 * mib); c <= 500*mib; c += 50 * mib {
		legacy := PaddedSizeMB(c)
		new := BasePaddedSizeMB(c, 0)
		if legacy != new {
			t.Errorf("all-big-file tree: BasePaddedSizeMB(%d, 0) = %d, want %d (matches PaddedSizeMB)",
				c, new, legacy)
		}
	}
	// Tree with all-small files (smallRatio=1) — at the 76 MB
	// apparent-shape observed on the EX44 (run 30656504195,
	// 2026-07-31, base-debian-parent staging), BasePaddedSizeMB
	// must overshoot the empirical mkfs.ext4 -d floor (109 MB).
	// Empirical bisect at e2fsprogs 1.47.0 showed 109 MB passes and
	// 108 MB fails with "Could not allocate block in ext2 filesystem
	// while writing file 'Dakar'". 1 MB headroom protects against a
	// future mkfs rev tightening the bound.
	const debianApparentBytes = 75_518_000 // du -sb of debian:12-slim on Lima
	const empiricalMinMB = 109
	got := BasePaddedSizeMB(debianApparentBytes, 1)
	if got <= empiricalMinMB {
		t.Errorf("BasePaddedSizeMB(76 MB, all-small) = %d MB, want > %d MB (empirical mkfs -d floor at run 30656504195)",
			got, empiricalMinMB)
	}
	// Edge cases on smallRatio: clamped to [0, 1].
	if got := BasePaddedSizeMB(100*mib, -0.5); got != PaddedSizeMB(100*mib) {
		t.Errorf("negative smallRatio should clamp to 0; got %d", got)
	}
	if got := BasePaddedSizeMB(100*mib, 2); got != BasePaddedSizeMB(100*mib, 1) {
		t.Errorf("smallRatio > 1 should clamp to 1; got %d", got)
	}
	// Monotonic in both arguments.
	prevContent := 0
	for c := int64(0); c <= 500*mib; c += 50 * mib {
		s := BasePaddedSizeMB(c, 0.8)
		if s < prevContent {
			t.Fatalf("not monotonic at c=%d: %d < %d", c, s, prevContent)
		}
		prevContent = s
	}
}

func TestCheckCapForStagingAccountsForSmallFiles(t *testing.T) {
	limits, ok := api.LimitsFor(api.PlanFree)
	if !ok {
		t.Fatal("free limits missing")
	}
	stats := SmallFileStats{ContentBytes: 76 * mib, SmallRatio: 1}
	sizeMB, err := CheckCapForStaging(limits, stats)
	if err != nil {
		t.Fatalf("CheckCapForStaging: %v", err)
	}
	if sizeMB <= PaddedSizeMB(stats.ContentBytes) {
		t.Fatalf("small-file-aware size %d MB did not exceed legacy size %d MB", sizeMB, PaddedSizeMB(stats.ContentBytes))
	}
}

func TestCheckCapForStagingKeepsWritableAppHeadroom(t *testing.T) {
	limits, ok := api.LimitsFor(api.PlanFree)
	if !ok {
		t.Fatal("free limits missing")
	}
	// This is the apparent size of the Go layer from the live ENOSPC
	// regression. The former 10%/4 MiB estimate produced a 30 MiB ext4 with
	// only 52 free blocks, which could not create /work during guest boot.
	const goFunctionBytes = 25_564_510
	sizeMB, err := CheckCapForStaging(limits, SmallFileStats{ContentBytes: goFunctionBytes})
	if err != nil {
		t.Fatal(err)
	}
	if sizeMB != 34 {
		t.Fatalf("Go function app image = %d MiB, want 34 MiB with runtime headroom", sizeMB)
	}
}

func TestInspectStaging(t *testing.T) {
	root := t.TempDir()
	must := func(p string, body []byte) {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	must("a/big1", bytes.Repeat([]byte{0}, 8192))    // 8 KB — at threshold
	must("a/big2", bytes.Repeat([]byte{0}, 16*1024)) // 16 KB — big
	must("a/small1", bytes.Repeat([]byte{0}, 100))   // 100 B — small
	must("a/small2", []byte("hi"))                   // 2 B — small
	if err := os.MkdirAll(filepath.Join(root, "a/empty-dir"), 0o755); err != nil {
		t.Fatal(err)
	}

	stats, err := InspectStaging(root)
	if err != nil {
		t.Fatalf("InspectStaging: %v", err)
	}
	// 8192 + 16*1024 + 100 + 2 = 24_678 bytes apparent.
	if want := int64(24_678); stats.ContentBytes != want {
		t.Errorf("ContentBytes = %d, want %d", stats.ContentBytes, want)
	}
	// 4 regular files: 2 below threshold (100, 2), 2 at-or-above (8192,
	// 16384). smallRatio = 2/4 = 0.5.
	if want := 0.5; stats.SmallRatio != want {
		t.Errorf("SmallRatio = %f, want %f", stats.SmallRatio, want)
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

// mkfsFakeRunner mimics mkfs.ext4's behaviour for tests that need the
// Storage-Put path to observe non-empty bytes. It writes a fixed
// payload to the path mkfs argv's "outImage" arg points at, so the
// downstream Storage.Put copies real bytes through.
//
// Production wiring uses wire.ExecRunner{}.Run with the real mkfs
// binary; this fake exists so unit tests don't require e2fsprogs.
type mkfsFakeRunner struct {
	fill []byte
	argv []string
}

func (f *mkfsFakeRunner) Run(_ context.Context, argv []string) error {
	f.argv = argv
	// Mkfs argv is {mkfs.ext4, -F, -L, label, -d, staging, outImage, sizeM}.
	// outImage is the second-to-last arg.
	out := argv[len(argv)-2]
	return os.WriteFile(out, f.fill, 0o644)
}

func TestBuildProducesSizedLayer(t *testing.T) {
	// A fake guest-init binary on disk.
	gi := filepath.Join(t.TempDir(), "guest-init")
	if err := os.WriteFile(gi, bytes.Repeat([]byte{0}, 1024), 0o755); err != nil {
		t.Fatal(err)
	}
	run := &mkfsFakeRunner{fill: []byte("FAKE-EXT4")}
	b := NewBuilder(run)
	be := newTestStorage(t)

	res, err := b.Build(context.Background(), BuildInput{
		Layers: []io.Reader{gzLayer(t, []entry{
			{name: "app/", typeflag: tar.TypeDir},
			{name: "app/server.js", body: "require('http')"},
		})},
		Manifest:      api.AppManifest{Entrypoint: []string{"node", "app/server.js"}},
		GuestInitPath: gi,
		Plan:          api.PlanFree,
		Storage:       be,
		StorageKey:    "apps/slug/dep.ext4",
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if res.SizeMB < MinLayerMB {
		t.Errorf("size %d below floor", res.SizeMB)
	}
	if run.argv[0] != "mkfs.ext4" {
		t.Errorf("expected mkfs.ext4, got %v", run.argv)
	}
	// The Storage path mkfs-es into a tmp file, NOT directly to OutImage;
	// the published key is what callers observe. Storage must hold the
	// produced ext4 at the requested key.
	if _, err := be.Get(context.Background(), "apps/slug/dep.ext4"); err != nil {
		t.Fatalf("storage Get after build: %v", err)
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
		Storage:       newTestStorage(t),
		StorageKey:    "apps/slug/dep.ext4",
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
		manifest, err := os.ReadFile(filepath.Join(staging, "upper", "etc", "faas", "app.json"))
		if err != nil {
			t.Errorf("app.json not injected: %v", err)
		}
		if !bytes.Contains(manifest, []byte(`"node"`)) {
			t.Errorf("manifest missing entrypoint: %s", manifest)
		}
		init, err := os.ReadFile(filepath.Join(staging, "upper", "sbin", "init"))
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
		Storage:       newTestStorage(t),
		StorageKey:    "apps/slug/dep.ext4",
	})
	if err != nil {
		t.Fatal(err)
	}
}

type runnerFunc func(context.Context, []string) error

func (f runnerFunc) Run(ctx context.Context, argv []string) error { return f(ctx, argv) }
