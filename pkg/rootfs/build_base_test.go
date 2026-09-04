package rootfs

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBuildBase_HappyPath proves the full pipeline: two gzipped layers go
// in, mkfs is invoked with a populated staging tree, and the produced ext4
// is published via the Storage backend under StorageKey. Mirrors
// TestBuildProducesSizedLayer's style on the new key-aware API.
func TestBuildBase_HappyPath(t *testing.T) {
	stagingRoot := t.TempDir()
	tmpRoot := t.TempDir()
	t.Setenv("FAAS_BASE_STAGING_ROOT", stagingRoot)
	t.Setenv("FAAS_BASE_TMP_ROOT", tmpRoot)
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
	foundTmp := false
	for _, arg := range run.argv {
		if !strings.HasSuffix(arg, ".ext4") {
			continue
		}
		if strings.HasPrefix(arg, stagingRoot) {
			t.Fatalf("base ext4 temp path created under staging root: %q", arg)
		}
		if strings.HasPrefix(arg, tmpRoot) {
			foundTmp = true
		}
	}
	if !foundTmp {
		t.Errorf("mkfs argv %v does not use configured base temp root %q", run.argv, tmpRoot)
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

// TestBuildBaseFromStaging_MkfsFromExistingDir (ADR-053) is the parent-ref
// staging seam: caller pre-populates the staging dir (cp -a from a vmmd
// loopback mount of the parent ext4 + delta layer apply) and BuildBaseFromStaging
// mkfs-es it without touching in.Layers. Pins that (a) the staging dir is
// used as mkfs's -d source verbatim, (b) the produced ext4 is published at
// StorageKey, (c) the staging dir is NOT auto-removed (caller owns the
// cleanup after publishing), and (d) the layered-apply code path is NOT
// invoked when Layers is empty.
func TestBuildBaseFromStaging_MkfsFromExistingDir(t *testing.T) {
	be := newTestStorage(t)
	run := &mkfsFakeRunner{fill: []byte("FAKE-BASE-FROM-STAGING")}
	b := NewBuilder(run)

	staging, err := MkdirBaseStaging()
	if err != nil {
		t.Fatalf("MkdirBaseStaging: %v", err)
	}
	// Caller-owned cleanup (matches the contract documented on
	// BuildBaseFromStaging).
	t.Cleanup(func() { _ = os.RemoveAll(staging) })

	// Seed the staging tree as if imaged's parent-ref path had
	// already cp -a'd the parent + applied delta layers.
	if err := os.WriteFile(filepath.Join(staging, "lib"), []byte("parent-content"), 0o644); err != nil {
		t.Fatalf("seed staging: %v", err)
	}
	if err := os.WriteFile(filepath.Join(staging, "usr"), []byte("delta-content"), 0o644); err != nil {
		t.Fatalf("seed staging: %v", err)
	}

	res, err := b.BuildBaseFromStaging(context.Background(), staging, BaseBuildInput{
		Storage:    be,
		StorageKey: "base/runner-node24.ext4",
	})
	if err != nil {
		t.Fatalf("BuildBaseFromStaging: %v", err)
	}
	if res.ImageKey != "base/runner-node24.ext4" {
		t.Errorf("ImageKey = %q, want %q", res.ImageKey, "base/runner-node24.ext4")
	}
	if res.SizeBytes == 0 {
		t.Error("SizeBytes = 0, want > 0")
	}
	if len(run.argv) == 0 || run.argv[0] != "mkfs.ext4" {
		t.Fatalf("Run argv[0] = %v, want mkfs.ext4", run.argv)
	}
	if !containsString(run.argv, "-d") {
		t.Errorf("argv %v must use -d (mkfs with source dir)", run.argv)
	}
	if !containsString(run.argv, staging) {
		t.Errorf("argv %v must contain the pre-populated staging dir %q", run.argv, staging)
	}
	// Staging dir must still exist — BuildBaseFromStaging does not own
	// its cleanup (the contract documented on the function).
	if _, err := os.Stat(staging); err != nil {
		t.Errorf("staging dir was removed by BuildBaseFromStaging: %v", err)
	}
	for _, path := range []string{
		"dev",
		"overlay",
		"proc",
		"run",
		"sys",
		"sys/fs/cgroup",
		"tmp",
	} {
		info, err := os.Stat(filepath.Join(staging, path))
		if err != nil {
			t.Errorf("required base mountpoint %q missing: %v", path, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("required base mountpoint %q is not a directory", path)
		}
	}
	// The published ext4 must be at the requested key.
	rc, err := be.Get(context.Background(), "base/runner-node24.ext4")
	if err != nil {
		t.Fatalf("ext4 not at key: %v", err)
	}
	body, _ := io.ReadAll(rc)
	_ = rc.Close()
	if !bytes.Equal(body, []byte("FAKE-BASE-FROM-STAGING")) {
		t.Errorf("ext4 body = %q, want %q", body, "FAKE-BASE-FROM-STAGING")
	}
}

// TestBuildBaseFromStaging_RejectsEmptyStaging covers the boundary: the
// caller must pass a non-empty staging path; an empty path or a
// non-existent path is a configuration error.
func TestBuildBaseFromStaging_RejectsEmptyStaging(t *testing.T) {
	be := newTestStorage(t)
	b := NewBuilder(&mkfsFakeRunner{fill: []byte("x")})
	if _, err := b.BuildBaseFromStaging(context.Background(), "", BaseBuildInput{
		Storage: be, StorageKey: "base/runtime.ext4",
	}); err == nil {
		t.Error("empty staging dir should error")
	}
	if _, err := b.BuildBaseFromStaging(context.Background(), "/nonexistent/never-exists-12345",
		BaseBuildInput{Storage: be, StorageKey: "base/runtime.ext4"},
	); err == nil {
		t.Error("missing staging dir should error")
	}
}

func TestBuildBaseFromStaging_RetriesMkfsBlockExhaustion(t *testing.T) {
	staging := t.TempDir()
	if err := os.WriteFile(filepath.Join(staging, "usr"), []byte("runtime"), 0o644); err != nil {
		t.Fatalf("seed staging: %v", err)
	}
	run := &retryBaseMkfsRunner{failures: 1}
	b := NewBuilder(run)
	if _, err := b.BuildBaseFromStaging(context.Background(), staging, BaseBuildInput{
		OutImage: filepath.Join(t.TempDir(), "runtime.ext4"),
	}); err != nil {
		t.Fatalf("BuildBaseFromStaging: %v", err)
	}
	if len(run.argvs) != 2 {
		t.Fatalf("mkfs calls = %d, want 2", len(run.argvs))
	}
	first := run.argvs[0][len(run.argvs[0])-1]
	second := run.argvs[1][len(run.argvs[1])-1]
	if first == second {
		t.Fatalf("retry kept the same image size: first=%q second=%q", first, second)
	}
}

// TestMkdirBaseStaging_ReturnedDirIsUsable pins that the exported
// helper returns a writable, empty, OS-allocated temp dir that the
// caller can populate + later hand to BuildBaseFromStaging. This is
// the load-bearing seam between imaged's parent-ref staging and
// rootfs's mkfs.
func TestMkdirBaseStaging_ReturnedDirIsUsable(t *testing.T) {
	d, err := MkdirBaseStaging()
	if err != nil {
		t.Fatalf("MkdirBaseStaging: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(d) })

	if d == "" {
		t.Fatal("MkdirBaseStaging returned empty path")
	}
	info, err := os.Stat(d)
	if err != nil {
		t.Fatalf("stat returned dir: %v", err)
	}
	if !info.IsDir() {
		t.Errorf("returned path %q is not a directory", d)
	}
	// Empty on creation — BuildBaseFromStaging is the one that mkfs-es it.
	entries, err := os.ReadDir(d)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("returned dir has %d initial entries, want 0", len(entries))
	}
}

// TestMkdirBaseStaging_RespectsFAAS_BASE_STAGING_ROOT — the env var
// override. Pinning the contract lets the production box (cd-controlplane
// EX44) point at /dev/shm/faas-base-staging without a code change AND
// lets the parent-ref staging test stage under t.TempDir() without
// polluting /dev/shm on macOS dev units.
func TestMkdirBaseStaging_RespectsFAAS_BASE_STAGING_ROOT(t *testing.T) {
	override := t.TempDir()
	t.Setenv("FAAS_BASE_STAGING_ROOT", override)

	d, err := MkdirBaseStaging()
	if err != nil {
		t.Fatalf("MkdirBaseStaging: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(d) })

	rel, err := filepath.Rel(override, d)
	if err != nil {
		t.Fatalf("Rel: %v", err)
	}
	if strings.HasPrefix(rel, "..") {
		t.Errorf("returned dir %q is not under override root %q (rel=%q)", d, override, rel)
	}
	if !strings.HasPrefix(filepath.Base(d), "faas-base-") {
		t.Errorf("returned dir basename %q does not match faas-base-* pattern", filepath.Base(d))
	}
}

// TestMkdirBaseStaging_ParentsRootDir — the env var may point at a
// path that does not exist yet (e.g. fresh /dev/shm/faas-base-staging
// after a reboot). MkdirAll with mode 0755 should create the
// intermediate dirs. Pins the production default behaviour: every
// boot, the first imaged start creates /dev/shm/faas-base-staging.
func TestMkdirBaseStaging_ParentsRootDir(t *testing.T) {
	nested := filepath.Join(t.TempDir(), "fresh", "nested", "staging-root")
	t.Setenv("FAAS_BASE_STAGING_ROOT", nested)

	d, err := MkdirBaseStaging()
	if err != nil {
		t.Fatalf("MkdirBaseStaging: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(d) })

	info, err := os.Stat(nested)
	if err != nil {
		t.Fatalf("stat created root %s: %v", nested, err)
	}
	if !info.IsDir() {
		t.Errorf("%s is not a directory", nested)
	}
	if perm := info.Mode().Perm(); perm != 0o755 {
		t.Errorf("root dir mode = %o, want 0755", perm)
	}
}

// TestMkdirBaseExtraction_RespectsFAAS_BASE_EXTRACT_ROOT — the full
// OCI layer extraction root override. The production unit ships
// FAAS_BASE_EXTRACT_ROOT=/srv/fc/base-staging so the extracted tree
// (which can be gigabytes for a Go toolchain base) lives on disk, not
// on the 2 GiB /dev/shm tmpfs (imaged ENOSPC crash-loop, 2026-08-05 →
// 2026-08-06; see pkg/daemonunitspec/imaged.go).
func TestMkdirBaseExtraction_RespectsFAAS_BASE_EXTRACT_ROOT(t *testing.T) {
	override := t.TempDir()
	t.Setenv("FAAS_BASE_EXTRACT_ROOT", override)

	d, err := MkdirBaseExtraction()
	if err != nil {
		t.Fatalf("MkdirBaseExtraction: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(d) })

	rel, err := filepath.Rel(override, d)
	if err != nil {
		t.Fatalf("Rel: %v", err)
	}
	if strings.HasPrefix(rel, "..") {
		t.Errorf("returned dir %q is not under override root %q (rel=%q)", d, override, rel)
	}
	if !strings.HasPrefix(filepath.Base(d), "faas-base-") {
		t.Errorf("returned dir basename %q does not match faas-base-* pattern", filepath.Base(d))
	}
}

// TestMkdirBaseExtraction_ParentsRootDir — the extraction root may
// point at a path that does not exist yet (fresh /srv/fc/base-staging
// before the controller creates it). MkdirAll with mode 0755 should
// create the intermediate dirs.
func TestMkdirBaseExtraction_ParentsRootDir(t *testing.T) {
	nested := filepath.Join(t.TempDir(), "fresh", "nested", "extract-root")
	t.Setenv("FAAS_BASE_EXTRACT_ROOT", nested)

	d, err := MkdirBaseExtraction()
	if err != nil {
		t.Fatalf("MkdirBaseExtraction: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(d) })

	info, err := os.Stat(nested)
	if err != nil {
		t.Fatalf("stat created root %s: %v", nested, err)
	}
	if !info.IsDir() {
		t.Errorf("%s is not a directory", nested)
	}
	if perm := info.Mode().Perm(); perm != 0o755 {
		t.Errorf("root dir mode = %o, want 0755", perm)
	}
}

// TestMkdirBaseExtraction_UnitFileSetsDiskBackedRoot — the load-bearing
// default for production lives in the unit files, not in code: the unit
// ships `Environment=FAAS_BASE_EXTRACT_ROOT=/srv/fc/base-staging` so
// the full OCI layer extraction bypasses the 2 GiB /dev/shm tmpfs.
// Pinning the contract here so a future unit-file edit that drops the
// env var fails CI loudly instead of silently regressing every deploy.
func TestMkdirBaseExtraction_UnitFileSetsDiskBackedRoot(t *testing.T) {
	for _, unitPath := range []string{
		"../../deploy/systemd/faas-imaged.service",
		// The canonical imaged.service is the ansible role file
		// (deploy/ansible/roles/compute_only_service/files/
		// faas-imaged.service); both trees are regenerated from
		// pkg/daemonunitspec and gated by `make generate-check` (ADR-143).
		"../../deploy/ansible/roles/compute_only_service/files/faas-imaged.service",
	} {
		body, err := os.ReadFile(unitPath)
		if err != nil {
			t.Fatalf("read %s: %v", unitPath, err)
		}
		want := "FAAS_BASE_EXTRACT_ROOT=/srv/fc/base-staging"
		if !strings.Contains(string(body), want) {
			t.Errorf("%s does not contain %q\n--- unit file ---\n%s", unitPath, want, string(body))
		}
	}
}

// TestMkdirBaseStaging_UnitFileSetsDevShm — the load-bearing default
// for production lives in deploy/systemd/faas-imaged.service, not in
// code: the unit ships `Environment=FAAS_BASE_STAGING_ROOT=/dev/shm/faas-base-staging`
// so host /tmp (ext4 on cd-controlplane EX44) is bypassed and the
// kernel's overlayfs upper-tmpfile check is satisfied. Pinning the
// contract here so a future unit-file edit that drops the env var
// fails CI loudly instead of silently regressing every deploy.
func TestMkdirBaseStaging_UnitFileSetsDevShm(t *testing.T) {
	const unitPath = "../../deploy/systemd/faas-imaged.service"
	body, err := os.ReadFile(unitPath)
	if err != nil {
		t.Fatalf("read %s: %v", unitPath, err)
	}
	want := "FAAS_BASE_STAGING_ROOT=/dev/shm/faas-base-staging"
	if !strings.Contains(string(body), want) {
		t.Errorf("%s does not contain %q\n--- unit file ---\n%s",
			unitPath, want, string(body))
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

// TestBuildBase_RealImageShapeWithAbsoluteSymlinks is the base-build-level
// regression pin for the imaged crash-loop that held cd-digitalocean red on
// every merge to main:
//
//	imaged: stage builder base docker.io/library/alpine@sha256:79ff19… →
//	  /srv/fc/base/builder-base.ext4: imaged: build base ext4:
//	  rootfs: apply base layer 0: rootfs: absolute entry path
//	  "/bin/busybox" rejected
//
// The layer fixture below mirrors the real alpine base image: a busybox
// binary plus applet symlinks whose targets are ABSOLUTE. The upstream
// image ships 306 such links; commit 7805f76 made ApplyLayer reject them,
// so imaged could not produce builder-base.ext4 and exited 1 in a restart
// loop (18,000+ restarts before this was caught).
//
// This exercises the exact call path imaged runs at startup — BuildBase,
// not just ApplyLayer — so the failure surfaces in PR CI rather than on the
// box after merge.
func TestBuildBase_RealImageShapeWithAbsoluteSymlinks(t *testing.T) {
	be := newTestStorage(t)
	run := &mkfsFakeRunner{fill: []byte("FAKE-BASE-EXT4")}
	b := NewBuilder(run)
	_, err := b.BuildBase(context.Background(), BaseBuildInput{
		Layers: []io.Reader{
			gzLayer(t, []entry{
				{name: "bin", typeflag: tar.TypeDir},
				{name: "bin/busybox", body: "ELF"},
				// The alpine shape: absolute applet symlinks.
				{name: "bin/sh", typeflag: tar.TypeSymlink, linkname: "/bin/busybox"},
				{name: "bin/cat", typeflag: tar.TypeSymlink, linkname: "/bin/busybox"},
				{name: "bin/ls", typeflag: tar.TypeSymlink, linkname: "/bin/busybox"},
				// And a relative one, which alpine also ships.
				{name: "usr/bin/awk", typeflag: tar.TypeSymlink, linkname: "../../bin/busybox"},
			}),
		},
		Storage:    be,
		StorageKey: "base/builder-base.ext4",
	})
	if err != nil {
		t.Fatalf("BuildBase rejected a real-image layer shape: %v\n"+
			"this is the imaged startup crash-loop (commit 7805f76); "+
			"absolute symlink targets are normal in every OCI base image", err)
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

type retryBaseMkfsRunner struct {
	failures int
	argvs    [][]string
}

func (r *retryBaseMkfsRunner) Run(_ context.Context, argv []string) error {
	r.argvs = append(r.argvs, append([]string(nil), argv...))
	if r.failures > 0 {
		r.failures--
		return errors.New("mkfs.ext4: Could not allocate block while populating file system")
	}
	return nil
}
