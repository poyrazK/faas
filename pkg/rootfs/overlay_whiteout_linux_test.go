//go:build linux

package rootfs

import (
	"archive/tar"
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/overlaymetadata"
	"github.com/onebox-faas/faas/pkg/wire"
	"golang.org/x/sys/unix"
)

func TestApplyLayerWithOverlayWhiteoutsCreatesUpperMarker(t *testing.T) {
	oldMknod := overlayMknod
	defer func() { overlayMknod = oldMknod }()
	var gotPath string
	overlayMknod = func(path string, mode uint32, dev int) error {
		gotPath = path
		if mode != unix.S_IFCHR|0o600 || dev != int(unix.Mkdev(0, 0)) {
			t.Fatal("whiteout is not an inaccessible 0/0 character device")
		}
		return os.WriteFile(path, nil, 0o600)
	}

	dst := t.TempDir()
	victim := filepath.Join(dst, "base-only")
	if err := os.WriteFile(victim, []byte("upper copy"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ApplyLayerWithOverlayWhiteouts(dst, tar.NewReader(singleTar(t, &tar.Header{Name: ".wh.base-only"}))); err != nil {
		t.Fatal(err)
	}
	wantMarker := victim
	if gotPath != wantMarker {
		t.Fatalf("mknod path = %q, want %q", gotPath, wantMarker)
	}
	if _, err := os.Stat(wantMarker); err != nil {
		t.Fatalf("whiteout marker missing: %v", err)
	}
	if err := ApplyLayerWithOverlayWhiteouts(dst, tar.NewReader(singleTar(t, &tar.Header{
		Name: "base-only", Mode: 0o644,
	}))); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(dst, ".wh.base-only")); !os.IsNotExist(err) {
		t.Fatalf("OCI archive marker was materialized: %v", err)
	}
	if info, err := os.Lstat(victim); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("replacement is not a regular file: %v", err)
	}
}

func TestBuilderRootOpacityRequiresSuccessfulGuestMetadata(t *testing.T) {
	for _, denied := range []bool{false, true} {
		t.Run(map[bool]string{false: "supported", true: "denied"}[denied], func(t *testing.T) {
			old := overlaySetxattr
			t.Cleanup(func() { overlaySetxattr = old })
			var calls []string
			overlaySetxattr = func(path, name string, value []byte, flags int) error {
				if name != overlaymetadata.RootOpaqueXattr || string(value) != "y" || flags != 0 {
					t.Fatal("changed guest metadata namespace")
				}
				calls = append(calls, path)
				if denied {
					return syscall.EPERM
				}
				return nil
			}
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "old"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
			opaque := false
			err := applyLayerGzForBuild(root, gzLayer(t, []entry{{name: ".wh..wh..opq"}}), false, &opaque, nil)
			if err != nil || !opaque {
				t.Fatal("root opacity was not recorded", err)
			}
			if _, err := os.Stat(filepath.Join(root, "old")); !os.IsNotExist(err) {
				t.Fatal("root opacity retained old content", err)
			}
			if err := os.WriteFile(filepath.Join(root, "new"), []byte("new layer"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := stageAppUpper(root, opaque); err != nil {
				t.Fatal(err)
			}
			if len(calls) != 2 || calls[0] != root || filepath.Dir(calls[1]) != root {
				t.Fatal("wrapper lost trusted root opacity", calls)
			}
			if data, err := os.ReadFile(filepath.Join(root, "upper", "new")); err != nil || string(data) != "new layer" {
				t.Fatal("wrapper cleared replacement content", err)
			}
			// A refused trusted xattr leaves the deferred marker for the
			// post-mkfs debugfs pass; no other namespace is tried.
			_, markerErr := os.Stat(filepath.Join(root, "upper", deferredOpaqueMarker))
			if denied != (markerErr == nil) {
				t.Fatal("deferred marker presence does not match the xattr result", denied, markerErr)
			}
		})
	}
}

func TestSidecarLayerWhiteoutsDoNotRequireNativeOverlayPrivileges(t *testing.T) {
	oldMknod, oldSetxattr := overlayMknod, overlaySetxattr
	t.Cleanup(func() { overlayMknod, overlaySetxattr = oldMknod, oldSetxattr })
	overlayMknod = func(string, uint32, int) error { t.Fatal("sidecar requested a device node"); return syscall.EPERM }
	overlaySetxattr = func(string, string, []byte, int) error {
		t.Fatal("sidecar requested trusted overlay metadata")
		return syscall.EPERM
	}
	root := t.TempDir()
	opaque := false
	for _, layer := range []io.Reader{
		gzLayer(t, []entry{{name: "removed", body: "first layer"}, {name: "opaque/lower", body: "first layer"}}),
		gzLayer(t, []entry{{name: ".wh.removed"}, {name: "opaque/.wh..wh..opq"}, {name: "opaque/current", body: "replacement"}}),
		gzLayer(t, []entry{{name: ".wh..wh..opq"}, {name: "current", body: "final layer"}}),
	} {
		if err := applyLayerGzForBuild(root, layer, true, &opaque, nil); err != nil {
			t.Fatal(err)
		}
	}
	if opaque {
		t.Fatal("independent sidecar acquired shared-base opacity")
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 || entries[0].Name() != "current" {
		t.Fatal("sidecar did not materialize root deletion", entries, err)
	}
}

func TestApplyOverlayOpaqueClearsAndMarksDirectory(t *testing.T) {
	oldSetxattr := overlaySetxattr
	defer func() { overlaySetxattr = oldSetxattr }()
	var names []string
	overlaySetxattr = func(path, name string, value []byte, flags int) error {
		names = append(names, name)
		return nil
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "old"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := applyOverlayOpaque(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "old")); !os.IsNotExist(err) {
		t.Fatalf("opaque directory retained old child: %v", err)
	}
	if len(names) != 1 || names[0] != "trusted.overlay.opaque" {
		t.Fatalf("xattr calls = %v, want trusted.overlay.opaque", names)
	}
}

func singleTar(t *testing.T, hdr *tar.Header) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf
}

// boa multi-service deploy (2026-11-21): imaged has no CAP_SYS_ADMIN, so the
// trusted xattr is refused. The directory is cleared and marked for the
// post-mkfs debugfs pass; no other xattr namespace is ever written, and any
// other setxattr failure still fails the build.
func TestApplyOverlayOpaqueDefersRefusedTrustedXattr(t *testing.T) {
	old := overlaySetxattr
	t.Cleanup(func() { overlaySetxattr = old })
	var calls []string
	result := error(syscall.EPERM)
	overlaySetxattr = func(path, name string, value []byte, flags int) error {
		calls = append(calls, name)
		return result
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "old"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := applyOverlayOpaque(dir); err != nil {
		t.Fatalf("refused trusted xattr failed the build: %v", err)
	}
	if len(calls) != 1 || calls[0] != "trusted.overlay.opaque" {
		t.Fatal("fell back to metadata not consumed by guest-init", calls)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != deferredOpaqueMarker {
		t.Fatal("opaque directory not cleared and marked", entries, err)
	}
	result = syscall.EIO
	if err := applyOverlayOpaque(t.TempDir()); !errors.Is(err, syscall.EIO) {
		t.Fatalf("unexpected setxattr failure accepted: %v", err)
	}
}

// The real tools, when the host has them: an EPERM-deferred opaque directory
// ends up with trusted.overlay.opaque=y in the ext4 image and no marker file.
func TestDeferredOverlayOpacityWritesTrustedXattrIntoImage(t *testing.T) {
	for _, tool := range []string{"mkfs.ext4", "debugfs"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not installed", tool)
		}
	}
	old := overlaySetxattr
	t.Cleanup(func() { overlaySetxattr = old })
	overlaySetxattr = func(string, string, []byte, int) error { return syscall.EPERM }
	staging := t.TempDir()
	if err := os.MkdirAll(filepath.Join(staging, "upper", "etc", "font dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := applyOverlayOpaque(filepath.Join(staging, "upper", "etc", "font dir")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staging, "upper", "etc", "font dir", "fonts.conf"), []byte("<fontconfig/>"), 0o644); err != nil {
		t.Fatal(err)
	}
	image := filepath.Join(t.TempDir(), "app.ext4")
	b := NewBuilder(wire.ExecRunner{})
	if _, err := b.runAppMkfs(t.Context(), staging, image, MinLayerMB, api.MustLimitsFor(api.PlanPro)); err != nil {
		t.Fatal(err)
	}
	out, err := exec.CommandContext(t.Context(), "debugfs", "-R", `ea_get "/upper/etc/font dir" trusted.overlay.opaque`, image).CombinedOutput()
	if err != nil || !strings.Contains(string(out), "y") || strings.Contains(string(out), "not found") {
		t.Fatalf("opaque xattr missing from image: %v %s", err, out)
	}
	out, err = exec.CommandContext(t.Context(), "debugfs", "-R", `ls "/upper/etc/font dir"`, image).CombinedOutput()
	if err != nil || strings.Contains(string(out), deferredOpaqueMarker) || !strings.Contains(string(out), "fonts.conf") {
		t.Fatalf("image directory listing wrong: %v %s", err, out)
	}
}

func TestReplaceOverlayWhiteoutPreservesOrdinaryEntries(t *testing.T) {
	root := t.TempDir()
	for _, kind := range []string{"file", "directory", "symlink"} {
		name := filepath.Join(root, kind)
		var err error
		switch kind {
		case "file":
			err = os.WriteFile(name, []byte("retained"), 0o600)
		case "directory":
			err = os.Mkdir(name, 0o700)
		case "symlink":
			err = os.Symlink("file", name)
		}
		if err != nil {
			t.Fatal(err)
		}
		before, err := os.Lstat(name)
		if err != nil {
			t.Fatal(err)
		}
		if err := replaceOverlayWhiteout(name, true); err != nil {
			t.Fatal(err)
		}
		after, err := os.Lstat(name)
		if err != nil || !os.SameFile(before, after) {
			t.Fatal("ordinary inode replaced", kind, err)
		}
	}
}
