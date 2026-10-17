//go:build metal && linux && amd64

package vmmdmount

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

const ownedMountParentNamespace = "GREGALE_TEST_OWNED_MOUNT_PARENT_NAMESPACE"
const ownedMountParentPID = "GREGALE_TEST_OWNED_MOUNT_PARENT_PID"

func TestMetalApplicationStandardOwnedParentMount(t *testing.T) {
	if os.Getenv("FAAS_RUN_APPLICATION_STANDARD_OWNED_MOUNT_TESTS") != "1" {
		t.Skip("explicit dedicated-host acceptance opt-in required")
	}
	if os.Geteuid() != 0 {
		t.Fatal("requires the dedicated native owner")
	}
	if _, err := os.Stat("/etc/faas/builder-acceptance-host"); err != nil {
		t.Fatalf("dedicated acceptance host marker: %v", err)
	}
	if info, err := os.Stat("/dev/kvm"); err != nil || info.Mode()&os.ModeCharDevice == 0 {
		t.Fatalf("native KVM host required: %v", err)
	}
	current, err := os.Readlink("/proc/self/ns/mnt")
	if err != nil {
		t.Fatal(err)
	}
	if expected := os.Getenv(ownedMountParentNamespace); expected != "" {
		parent, err := os.Readlink(fmt.Sprintf("/proc/%d/ns/mnt", os.Getppid()))
		if err != nil || parent != expected || parent == current || os.Getenv(ownedMountParentPID) != strconv.Itoa(os.Getppid()) {
			t.Fatalf("private namespace handshake refused: %v", err)
		}
		exerciseOwnedParentMount(t)
		return
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "unshare", "--mount", "--propagation", "private", "--", executable,
		"-test.run=^TestMetalApplicationStandardOwnedParentMount$", "-test.v", "-test.timeout=110s")
	cmd.Env = append(os.Environ(), ownedMountParentNamespace+"="+current, ownedMountParentPID+"="+strconv.Itoa(os.Getpid()))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("private native acceptance: %v\n%s", err, out)
	}
}

func ownedMountNativeCommand(t *testing.T, command string, args ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, command, args...).CombinedOutput(); err != nil {
		t.Fatalf("native fixture command %s: %v\n%s", command, err, out)
	}
}

func ownedMountImageDigest(t *testing.T, path string) [32]byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return sha256.Sum256(data)
}

func exerciseOwnedParentMount(t *testing.T) {
	// Bind only inside the verified child namespace; the shared host directory
	// and any daemon mounts remain outside this fixture's ownership.
	resolved, err := filepath.EvalSymlinks(MountRoot)
	if err != nil || resolved != MountRoot {
		t.Fatalf("bootstrap native mount root first: %v", err)
	}
	privateRoot := t.TempDir()
	ownedMountNativeCommand(t, "mount", "--bind", privateRoot, MountRoot)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if out, err := exec.CommandContext(ctx, "umount", MountRoot).CombinedOutput(); err != nil {
			t.Errorf("release private mount root: %v: %s", err, out)
		}
	})
	fixture := t.TempDir()
	if err := os.Mkdir(filepath.Join(fixture, "etc"), 0o755); err != nil {
		t.Fatal(err)
	}
	payload := []byte("native-owned-parent-payload")
	if err := os.WriteFile(filepath.Join(fixture, "etc", "parent"), payload, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/parent", filepath.Join(fixture, "absolute-link")); err != nil {
		t.Fatal(err)
	}
	image, err := os.CreateTemp(MountRoot, ParentMountPrefix+"src-")
	if err != nil {
		t.Fatal(err)
	}
	if err := errors.Join(image.Truncate(16<<20), image.Close()); err != nil {
		t.Fatal(err)
	}
	ownedMountNativeCommand(t, "mkfs.ext4", "-q", "-F", "-d", fixture, image.Name())
	expected := ownedMountImageDigest(t, image.Name())
	r := NewRegistry(1)
	lease, err := r.ReserveMount(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := lease.Release(ctx); err != nil {
			t.Errorf("native lease cleanup: %v", err)
		}
		if n := r.SweepAll(ctx, nil); n != 0 {
			t.Errorf("native lease required recovery of %d mounts", n)
		}
	})
	mountpoint, err := MountExt4ReadOnly(t.Context(), image.Name())
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.Attach(mountpoint, MountKindParentExt4, "base/native-fixture.ext4", image.Name()); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Umount(t.Context(), mountpoint); !errors.Is(err, ErrMountBusy) {
		t.Fatalf("external release admitted: %v", err)
	}
	if _, err := r.ReserveMount(t.Context()); !errors.Is(err, ErrMountCapacity) {
		t.Fatalf("native capacity overcommit: %v", err)
	}
	if n := r.SweepAll(t.Context(), nil); n != 0 {
		t.Fatalf("swept active native consumer: %d", n)
	}
	target := t.TempDir()
	if err := copyParentTree(t.Context(), mountpoint, target); err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(filepath.Join(target, "etc", "parent"))
	if err != nil || string(actual) != string(payload) {
		t.Fatalf("copied another view: %v", err)
	}
	if link, err := os.Readlink(filepath.Join(target, "absolute-link")); err != nil || link != "/etc/parent" {
		t.Fatalf("link changed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(mountpoint, "forbidden-write"), payload, 0o600); err == nil {
		t.Fatal("native parent mount is writable")
	}
	if actual := ownedMountImageDigest(t, image.Name()); actual != expected {
		t.Fatal("mount or copy modified measured ext4 bytes")
	}
	if err := lease.Release(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{mountpoint, image.Name()} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("owned mount resource remains: %v", err)
		}
	}
}
