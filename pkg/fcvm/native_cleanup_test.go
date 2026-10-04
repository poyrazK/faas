//go:build linux || darwin

// adr: 532 — native ownership and uncertain retirement must remain fenced.
package fcvm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/onebox-faas/faas/pkg/netns"
)

func TestNativeCleanupMountProofPreservesBoundariesAndRejectsIncompleteTable(t *testing.T) {
	table := []byte("31 22 0:1 / /srv/jail/old/root rw - tmpfs tmpfs rw\n32 22 0:1 / /srv/jail/old/root/a\\040b rw - tmpfs tmpfs rw\n33 22 0:1 / /srv/jail/older/root rw - tmpfs tmpfs rw\n")
	mounts, err := nativeMountsBelow(table, "/srv/jail/old")
	if err != nil || !reflect.DeepEqual(mounts, []string{"/srv/jail/old/root", "/srv/jail/old/root/a b"}) {
		t.Fatalf("mounts=%v err=%v", mounts, err)
	}
	for _, bad := range []string{"", "31 22 0:1 / /srv/jail/old rw", "31 22 0:1 / /srv/jail/old/a\\999 rw - tmpfs tmpfs rw"} {
		if _, err := nativeMountsBelow([]byte(bad), "/srv/jail/old"); err == nil {
			t.Fatalf("invalid table granted absence: %q", bad)
		}
	}
}

func TestNativeCleanupBindFailuresRetainMetadataAndRestoreModesOnRetry(t *testing.T) {
	for _, failure := range []string{"unmount", "source_mode", "mount_survives"} {
		t.Run(failure, func(t *testing.T) {
			_, v, _ := nativeManagerFixture(t)
			root := filepath.Join(v.chrootBase, v.fcName, "old", "root")
			if err := os.MkdirAll(root, 0o750); err != nil {
				t.Fatal(err)
			}
			source := filepath.Join(t.TempDir(), "source")
			if err := os.WriteFile(source, []byte("image"), 0o644); err != nil {
				t.Fatal(err)
			}
			mount := filepath.Join(root, "image")
			if err := os.WriteFile(mount, nil, 0o644); err != nil {
				t.Fatal(err)
			}
			handle, err := os.Open(source)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = handle.Close() })
			info, err := handle.Stat()
			if err != nil {
				t.Fatal(err)
			}
			identity, err := resourceFileID(info)
			if err != nil {
				t.Fatal(err)
			}
			key := bindSourceKey{path: source, file: identity}
			v.bindMounts["old"] = []ephemeralBind{{source: source, mountpoint: mount, file: identity, tracked: true}}
			v.bindSourceModes[key] = bindSourceMode{refs: 1, mode: 0o600, file: identity, handle: handle}
			mounted := true
			v.nativeRecovery.mounts = func(string) ([]string, error) {
				if mounted {
					return []string{mount}, nil
				}
				return nil, nil
			}
			cause := errors.New("unmount failed")
			v.nativeRecovery.unmount = func(context.Context, string) error {
				if failure == "unmount" {
					return cause
				}
				if failure != "mount_survives" {
					mounted = false
				}
				return nil
			}
			if failure == "source_mode" {
				entry := v.bindSourceModes[key]
				entry.handle = nil
				v.bindSourceModes[key] = entry
			}
			if err := v.unmountNativeBinds(t.Context(), "old", filepath.Dir(root)); err == nil {
				t.Fatal("uncertain bind cleanup acknowledged")
			}
			if len(v.bindMounts["old"]) != 1 || v.bindSourceModes[key].refs != 1 {
				t.Fatal("failure discarded recovery metadata")
			}
			if failure == "source_mode" {
				entry := v.bindSourceModes[key]
				entry.handle = handle
				v.bindSourceModes[key] = entry
			}
			v.nativeRecovery.unmount = func(context.Context, string) error { mounted = false; return nil }
			if err := v.unmountNativeBinds(t.Context(), "old", filepath.Dir(root)); err != nil {
				t.Fatal(err)
			}
			info, err = os.Stat(source)
			if err != nil || info.Mode().Perm() != 0o600 || len(v.bindMounts["old"]) != 0 || len(v.bindSourceModes) != 0 {
				t.Fatal("retry did not finish owned cleanup")
			}
		})
	}
}

func TestNativeCleanupRestoresPinnedSourceAfterCacheReplacement(t *testing.T) {
	_, v, _ := nativeManagerFixture(t)
	root := filepath.Join(v.chrootBase, v.fcName, "old", "root")
	if err := os.MkdirAll(root, 0o750); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "source")
	if err := os.WriteFile(source, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	handle, err := os.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	info, err := handle.Stat()
	if err != nil {
		t.Fatal(err)
	}
	identity, err := resourceFileID(info)
	if err != nil {
		t.Fatal(err)
	}
	mount := filepath.Join(root, "image")
	if err := os.WriteFile(mount, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	v.bindMounts["old"] = []ephemeralBind{{source: source, mountpoint: mount, file: identity, tracked: true}}
	v.bindSourceModes[bindSourceKey{path: source, file: identity}] = bindSourceMode{refs: 1, mode: 0o600, file: identity, handle: handle}
	held := source + ".held"
	if err := os.Rename(source, held); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("refreshed"), 0o640); err != nil {
		t.Fatal(err)
	}
	mounted := true
	v.nativeRecovery.mounts = func(string) ([]string, error) {
		if mounted {
			return []string{mount}, nil
		}
		return nil, nil
	}
	v.nativeRecovery.unmount = func(context.Context, string) error { mounted = false; return nil }
	if err := v.unmountNativeBinds(t.Context(), "old", filepath.Dir(root)); err != nil {
		t.Fatal(err)
	}
	for path, mode := range map[string]os.FileMode{held: 0o600, source: 0o640} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != mode {
			t.Fatalf("source mode after cleanup: path=%s want=%v err=%v", path, mode, err)
		}
	}
	if len(v.bindMounts["old"]) != 0 || len(v.bindSourceModes) != 0 {
		t.Fatal("owned bind references survived cleanup")
	}
}

func TestNativeCleanupUnknownMountOrOldVersionChrootCannotBeAcknowledged(t *testing.T) {
	_, v, _ := nativeManagerFixture(t)
	l := prepareRecoveredNative(t, v, 0, "old-instance")
	local, err := v.nativeRecovery.journal.read(l.Instance)
	if err != nil {
		t.Fatal(err)
	}
	v.nativeRecovery.remember(local)
	root := filepath.Join(v.chrootBase, "firecracker-v1.6.0", l.Instance, "root")
	if err := os.MkdirAll(root, 0o750); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root, "image")
	if err := os.WriteFile(marker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := v.nativeResourcesRemoved(l, netns.Config{}); err == nil {
		t.Fatal("old binary version escaped resource proof")
	}
	v.nativeRecovery.mounts = func(path string) ([]string, error) {
		if path == filepath.Dir(root) {
			return []string{marker}, nil
		}
		return nil, nil
	}
	if err := v.Kill(t.Context(), l); err == nil {
		t.Fatal("unproven restart bind removed")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("unproven bind root deleted")
	}
	v.nativeRecovery.mounts = func(string) ([]string, error) { return nil, nil }
	if err := v.Kill(t.Context(), l); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("confirmed old-version jail survived")
	}
}

func TestNativeCleanupCgroupRemovalNeverUnlinksControlFiles(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "scope")
	child := filepath.Join(path, "worker")
	if err := os.MkdirAll(child, 0o750); err != nil {
		t.Fatal(err)
	}
	control := filepath.Join(path, "cgroup.procs")
	if err := os.WriteFile(control, []byte("42"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := removeNativeCgroup(path); err == nil {
		t.Fatal("ordinary control file was unlinked")
	}
	if _, err := os.Stat(control); err != nil {
		t.Fatal("control file removed")
	}
	if err := os.Remove(control); err != nil {
		t.Fatal(err)
	}
	if err := removeNativeCgroup(path); err != nil {
		t.Fatal(err)
	}
}
