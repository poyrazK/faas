//go:build linux && metal

// adr: 474
package fcvm

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/fcvm/leakcheck"
	"golang.org/x/sys/unix"
)

// Native resource acceptance exercises the real bind operation with the
// fixture's legacy owner; production staging supplies its caller's owner.
func (v *JailerVMM) bindImage(root, src, name, instance string, addPerms os.FileMode, readOnly bool) (string, error) {
	ctx, cancel := v.driveStagingContext()
	defer cancel()
	owner, err := v.nativeDriveStagingOwner(ctx, instance)
	if err != nil {
		return "", err
	}
	return v.bindImageForOwner(ctx, owner, root, src, name, instance, addPerms, readOnly)
}

func metalAssetFixture(t *testing.T) (*JailerVMM, *ResourceJournal, string, string) {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("requires root on dedicated Linux KVM")
	}
	if _, err := os.Stat("/dev/kvm"); err != nil {
		t.Skip("requires Linux KVM")
	}
	v, j := assetFixture(t)
	if err := j.begin(journalTestLease(idOther, 1)); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.WriteFile(source, []byte("owned image"), 0o600); err != nil {
		t.Fatal(err)
	}
	syncDir := j.directorySync
	t.Cleanup(func() {
		j.directorySync = syncDir
		for _, id := range []string{idOther, idLive} {
			if err := v.unmountBindMounts(id); err != nil {
				t.Errorf("fixture bind cleanup: %v", err)
			}
		}
		leakcheck.AssertZero(t)
	})
	return v, j, source, root
}

func TestMetalResourceAssetsBindLifecycle(t *testing.T) {
	v, j, source, root := metalAssetFixture(t)
	for _, id := range []string{idLive, idOther} {
		jail := filepath.Join(root, id)
		if err := os.Mkdir(jail, 0o700); err != nil {
			t.Fatal(err)
		}
		if _, err := v.bindImage(jail, source, "image", id, 0o044, true); err != nil {
			t.Fatal(err)
		}
		r, _, err := j.lookup(id)
		if err != nil || len(r.Assets) != 1 {
			t.Fatalf("bind checkpoint: %+v %v", r, err)
		}
		a := r.Assets[0]
		if a.Target == nil || a.File == nil || a.Mount == nil || a.Mount.MountID == 0 || a.Mount.Namespace == 0 || a.OriginalMode != 0o600 || !a.ReadOnly {
			t.Fatalf("incomplete bind provenance: %+v", a)
		}
		if err := os.WriteFile(a.Path, []byte("mutation"), 0o600); !errors.Is(err, unix.EROFS) {
			t.Fatalf("read-only policy missing: %v", err)
		}
	}
	// The first source reference is released once, even if journal retirement
	// fails after physical unmount/removal. Retry must preserve the second owner.
	syncDir := j.directorySync
	injected := errors.New("retirement fsync fixture")
	j.directorySync = func() error { return injected }
	if err := v.unmountBindMounts(idLive); !errors.Is(err, injected) {
		t.Fatalf("uncertain retirement: %v", err)
	}
	if len(v.bindMounts[idLive]) != 1 || !v.bindMounts[idLive][0].released || v.bindSourceRefs(source) != 1 {
		t.Fatal("uncertain retirement lost released-reference state")
	}
	j.directorySync = syncDir
	if err := v.unmountBindMounts(idLive); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(source)
	if info.Mode().Perm() != 0o644 || v.bindSourceRefs(source) != 1 {
		t.Fatal("retry restored permissions under the remaining owner")
	}
	if err := v.unmountBindMounts(idOther); err != nil {
		t.Fatal(err)
	}
	info, _ = os.Stat(source)
	if info.Mode().Perm() != 0o600 || len(v.bindSourceModes) != 0 {
		t.Fatal("last bind did not restore original mode")
	}
	for _, id := range []string{idLive, idOther} {
		r, _, _ := j.lookup(id)
		if len(r.Assets) != 0 {
			t.Fatal("removed bind remained in journal")
		}
	}
}

func TestMetalResourceAssetsRestoreOriginalInodeAfterSourceReplacement(t *testing.T) {
	v, _, source, root := metalAssetFixture(t)
	jail := filepath.Join(root, "jail")
	if err := os.Mkdir(jail, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := v.bindImage(jail, source, "image", idLive, 0o044, true); err != nil {
		t.Fatal(err)
	}

	displaced := source + ".displaced"
	if err := os.Rename(source, displaced); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("replacement image"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := v.unmountBindMounts(idLive); err != nil {
		t.Fatal(err)
	}
	oldInfo, err := os.Stat(displaced)
	if err != nil {
		t.Fatal(err)
	}
	if got := oldInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("original inode mode = %#o, want %#o", got, 0o600)
	}
	newInfo, err := os.Stat(source)
	if err != nil {
		t.Fatal(err)
	}
	if got := newInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("replacement inode mode = %#o, want %#o", got, 0o600)
	}
	if body, err := os.ReadFile(source); err != nil || string(body) != "replacement image" {
		t.Fatalf("replacement source changed: body=%q err=%v", body, err)
	}
}

func TestMetalResourceAssetsRefuseForeignMountAndTarget(t *testing.T) {
	v, _, source, root := metalAssetFixture(t)
	alias := filepath.Join(root, "jail-alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	if _, err := v.bindImage(alias, source, "image", idLive, 0o044, true); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "image")
	foreign := filepath.Join(root, "foreign")
	if err := os.WriteFile(foreign, []byte("foreign image"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mount(foreign, target, "", unix.MS_BIND, ""); err != nil {
		t.Fatal(err)
	}
	foreignMounted := true
	t.Cleanup(func() {
		if foreignMounted {
			_ = unix.Unmount(target, 0)
		}
	})
	if err := v.unmountBindMounts(idLive); err == nil {
		t.Fatal("foreign stacked mount was removed")
	}
	if body, _ := os.ReadFile(target); string(body) != "foreign image" {
		t.Fatal("foreign mount contents changed")
	}
	if err := unix.Unmount(target, 0); err != nil {
		t.Fatal(err)
	}
	foreignMounted = false
	// Remove our original mount as the test fixture, then replace its uncovered
	// placeholder. The live owner must retain cleanup until that inode returns.
	if err := unix.Unmount(target, 0); err != nil {
		t.Fatal(err)
	}
	held := target + ".held"
	if err := os.Rename(target, held); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("foreign target"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := v.unmountBindMounts(idLive); err == nil {
		t.Fatal("foreign target file was removed")
	}
	if body, _ := os.ReadFile(target); string(body) != "foreign target" {
		t.Fatal("foreign target contents changed")
	}
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(held, target); err != nil {
		t.Fatal(err)
	}
	if err := v.unmountBindMounts(idLive); err != nil {
		t.Fatal(err)
	}
}

func TestMetalResourceAssetsFailedBindCheckpoint(t *testing.T) {
	for _, phase := range []string{"intent", "target", "mount"} {
		t.Run(phase, func(t *testing.T) {
			v, j, source, root := metalAssetFixture(t)
			syncDir := j.directorySync
			injected := errors.New("bind checkpoint fsync fixture")
			j.directorySync = func() error {
				for _, a := range j.records[idLive].Assets {
					if (phase == "intent" && a.Target == nil) || (phase == "target" && a.Target != nil && a.Mount == nil) || (phase == "mount" && a.Mount != nil) {
						return injected
					}
				}
				return syncDir()
			}
			if _, err := v.bindImage(root, source, "image", idLive, 0o044, true); !errors.Is(err, injected) {
				t.Fatalf("failed checkpoint acknowledged: %v", err)
			}
			mount, err := resourceMountAt(filepath.Join(root, "image"))
			if err != nil || (mount != nil) != (phase == "mount") {
				t.Fatalf("unexpected partial mount: %+v %v", mount, err)
			}
			j.directorySync = syncDir
			if err := v.unmountBindMounts(idLive); err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(source)
			if err != nil || info.Mode().Perm() != 0o600 || len(v.bindMounts[idLive]) != 0 || len(v.bindSourceModes) != 0 {
				t.Fatal("partial bind lost source cleanup")
			}
		})
	}
}
