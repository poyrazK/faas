//go:build linux

package rootfs

import (
	"archive/tar"
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/onebox-faas/faas/pkg/overlaymetadata"
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
			err := applyLayerGzForBuild(root, gzLayer(t, []entry{{name: ".wh..wh..opq"}}), false, &opaque)
			if denied {
				if !errors.Is(err, syscall.EPERM) || opaque {
					t.Fatal("unsupported opacity acquired authority", err)
				}
				return
			}
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
		if err := applyLayerGzForBuild(root, layer, true, &opaque); err != nil {
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

func TestApplyOverlayOpaqueRefusesDifferentXattrNamespace(t *testing.T) {
	old := overlaySetxattr
	t.Cleanup(func() { overlaySetxattr = old })
	var calls []string
	overlaySetxattr = func(path, name string, value []byte, flags int) error {
		calls = append(calls, name)
		return syscall.EPERM
	}
	if err := applyOverlayOpaque(t.TempDir()); !errors.Is(err, syscall.EPERM) {
		t.Fatalf("unusable opaque metadata accepted: %v", err)
	}
	if len(calls) != 1 || calls[0] != "trusted.overlay.opaque" {
		t.Fatal("fell back to metadata not consumed by guest-init", calls)
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
