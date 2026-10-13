//go:build linux && metal

package fcvm

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"golang.org/x/sys/unix"
)

func TestMetalNativeImagePreparationAcrossBindMount(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root and an isolated mount namespace")
	}
	if os.Getenv("GREGALE_NATIVE_IMAGE_BIND_MOUNT_CHILD") != "1" {
		cmd := exec.CommandContext(t.Context(), "unshare", "--mount", "--propagation", "private", os.Args[0],
			"-test.run=^TestMetalNativeImagePreparationAcrossBindMount$", "-test.v")
		cmd.Env = append(os.Environ(), "GREGALE_NATIVE_IMAGE_BIND_MOUNT_CHILD=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("isolated image mount test: %v\n%s", err, out)
		}
		return
	}

	base := t.TempDir()
	sourceDir := filepath.Join(base, "source")
	boundDir := filepath.Join(base, "bound")
	for _, dir := range []string{sourceDir, boundDir} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	source := filepath.Join(sourceDir, "base.img")
	if err := os.WriteFile(source, []byte("image"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mount(sourceDir, boundDir, "", unix.MS_BIND, ""); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := unix.Unmount(boundDir, unix.MNT_DETACH); err != nil {
			t.Error(err)
		}
	})

	owner := nativeLaunchRecord{Version: 1, Generation: uuid.NewString(), KernelBootID: uuid.NewString(),
		Lease: leaseForSlot("bind-mount-image", 0)}
	root := filepath.Join(boundDir, "firecracker", owner.Lease.Instance, "root")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	input, err := os.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	plainRoot, err := os.Open(filepath.Join(sourceDir, "firecracker", owner.Lease.Instance, "root"))
	if err != nil {
		t.Fatal(err)
	}
	if !nativeImagesShareMount(input, plainRoot) {
		t.Fatal("same-mount source and jail could not use a hard link")
	}
	plainRoot.Close()

	prepared, err := (linuxNativeImageSources{base: boundDir}).Prepare(owner, root, source, "base", true)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	if prepared.PreferLink() {
		t.Fatal("bind-mounted jail selected a hard link across mount IDs")
	}
	if err := unix.Linkat(unix.AT_FDCWD, nativeImageFDPath(input), unix.AT_FDCWD,
		filepath.Join(root, "unexpected-link"), unix.AT_SYMLINK_FOLLOW); !errors.Is(err, unix.EXDEV) {
		t.Fatalf("expected kernel cross-mount link refusal, got %v", err)
	}
}
