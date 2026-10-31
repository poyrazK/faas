//go:build linux

package fcvm

// adr: 20261017 vmmd restart adoption

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

// restagedBindFixture is an adopted guest whose base drive's cache source
// was re-staged after it booted (production rc.306): the cache path now names
// a refreshed file, while the guest's jail still binds the recorded inode.
type restagedBindFixture struct {
	v         *JailerVMM
	source    string // the cache path, now naming the refreshed file
	guestRoot string // stands in for /proc/<pid>/root
	recorded  resourceFileIdentity
	bind      resourceAsset
}

func newRestagedBindFixture(t *testing.T) *restagedBindFixture {
	t.Helper()
	f := &restagedBindFixture{v: NewJailerVMM(t.TempDir(), 0), guestRoot: t.TempDir()}
	f.source = filepath.Join(t.TempDir(), "base.ext4")
	if err := os.WriteFile(f.source, []byte("recorded image"), 0o600); err != nil {
		t.Fatal(err)
	}
	var err error
	if f.recorded, err = statIdentity(f.source); err != nil {
		t.Fatal(err)
	}
	// The guest's bind mount keeps the recorded inode at its target.
	if err := os.Link(f.source, filepath.Join(f.guestRoot, "base.ext4")); err != nil {
		t.Fatal(err)
	}
	// The cache publishes a refreshed copy over the path, as a pre-stage does.
	refreshed := f.source + ".tmp"
	if err := os.WriteFile(refreshed, []byte("refreshed image"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(refreshed, f.source); err != nil {
		t.Fatal(err)
	}
	saved := adoptedGuestRoot
	t.Cleanup(func() { adoptedGuestRoot = saved })
	adoptedGuestRoot = func(int) string { return f.guestRoot }
	ns := &resourceMountIdentity{BootID: idOther, Namespace: 4026531840, MountID: 9}
	target := resourceFileIdentity{Device: 1, Inode: 2}
	recorded := f.recorded
	f.bind = resourceAsset{Kind: "bind", Path: filepath.Join(f.v.chrootRoot(idLive), "base.ext4"), Source: f.source,
		SourceFile: &recorded, OriginalMode: 0o600, Mount: ns, Target: &target}
	return f
}

func TestAdoptGuestBindsTakesARestagedSourceFromTheGuest(t *testing.T) {
	f := newRestagedBindFixture(t)
	if err := f.v.adoptGuestBinds(idLive, 4242, []resourceAsset{f.bind}); err != nil {
		t.Fatalf("adopt with a re-staged source: %v", err)
	}
	st := f.v.bindSourceModes[bindSourceKey{path: f.source, file: f.recorded}]
	if st.refs != 1 || st.handle == nil || !st.viaGuest {
		t.Fatalf("bind source state = %+v", st)
	}
	info, err := st.handle.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if id, err := resourceFileID(info); err != nil || id != f.recorded {
		t.Fatalf("pinned %+v, want the recorded inode %+v", id, f.recorded)
	}
	// Teardown restores the recorded inode's mode through the pinned handle,
	// never the refreshed file's.
	if err := os.Chmod(filepath.Join(f.guestRoot, "base.ext4"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := f.v.releaseBindSource(f.source, f.recorded); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Join(f.guestRoot, "base.ext4")); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("recorded inode mode = %v, %v; want 0600 restored", info.Mode().Perm(), err)
	}
	if info, err := os.Stat(f.source); err != nil || info.Mode().Perm() != 0o640 {
		t.Fatalf("refreshed file mode = %v, %v; want it untouched (0640)", info.Mode().Perm(), err)
	}
}

func TestAdoptGuestBindsRefusesWhatIsNotTheRecordedFile(t *testing.T) {
	for name, spoil := range map[string]func(t *testing.T, f *restagedBindFixture){
		"the guest binds another file": func(t *testing.T, f *restagedBindFixture) {
			p := filepath.Join(f.guestRoot, "base.ext4")
			// Keep the recorded inode allocated, or the new file can reuse
			// its number, which (device, inode) cannot tell apart.
			if err := os.Link(p, filepath.Join(f.guestRoot, "kept")); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(p); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte("other"), 0o600); err != nil {
				t.Fatal(err)
			}
		},
		"a symlink at the target": func(t *testing.T, f *restagedBindFixture) {
			p := filepath.Join(f.guestRoot, "base.ext4")
			if err := os.Rename(p, filepath.Join(f.guestRoot, "real")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("real", p); err != nil {
				t.Fatal(err)
			}
		},
		"a target outside the jail": func(_ *testing.T, f *restagedBindFixture) {
			f.bind.Path = filepath.Join(f.guestRoot, "..", "base.ext4")
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newRestagedBindFixture(t)
			spoil(t, f)
			err := f.v.adoptGuestBinds(idLive, 4242, []resourceAsset{f.bind})
			if err == nil || !strings.Contains(err.Error(), "source was replaced") {
				t.Fatalf("adopt = %v, want a refusal naming the replaced source", err)
			}
			f.v.dropAdoptedBinds(idLive)
			if len(f.v.bindSourceModes) != 0 {
				t.Fatalf("refusal left references: %v", f.v.bindSourceModes)
			}
		})
	}
}

// In production the guest-view handle comes through the jail's read-only
// bind, so restoring the mode through it fails (metal 2026-10-30: "chmod
// /proc/self/fd/15: read-only file system", and the adopted guest could never
// park). That failure must not hold the teardown; for any other handle it
// still does.
func TestReleaseBindSourceToleratesAGuestViewHandle(t *testing.T) {
	for _, viaGuest := range []bool{true, false} {
		dir := t.TempDir()
		src := filepath.Join(dir, "base.ext4")
		if err := os.WriteFile(src, []byte("image"), 0o600); err != nil {
			t.Fatal(err)
		}
		id, err := statIdentity(src)
		if err != nil {
			t.Fatal(err)
		}
		// An O_PATH handle cannot change the mode, as a handle through a
		// read-only mount cannot.
		fd, err := unix.Open(src, unix.O_PATH|unix.O_CLOEXEC, 0)
		if err != nil {
			t.Fatal(err)
		}
		v := NewJailerVMM(dir, 0)
		key := bindSourceKey{path: src, file: id}
		v.bindSourceModes[key] = bindSourceMode{mode: 0o600, refs: 1, file: id, handle: os.NewFile(uintptr(fd), src), viaGuest: viaGuest}
		err = v.releaseBindSource(src, id)
		_, kept := v.bindSourceModes[key]
		if viaGuest && (err != nil || kept) {
			t.Fatalf("guest-view release = %v, kept %v; want it done", err, kept)
		}
		if !viaGuest && (err == nil || !kept) {
			t.Fatalf("host release = %v, kept %v; want the failed restoration to keep the reference", err, kept)
		}
		if kept {
			_ = v.bindSourceModes[key].handle.Close()
		}
	}
}

// faas-vmmd.service's RestrictSUIDSGID= answers openat2 with ENOSYS, so the
// guest-view open walks the jail one component at a time and must refuse
// exactly what RESOLVE_BENEATH | RESOLVE_NO_SYMLINKS would.
func TestOpenBeneathNoFollowStaysInTheJail(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "a", "b"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a", "b", "f"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("a/b", filepath.Join(root, "l")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("f", filepath.Join(root, "a", "b", "s")); err != nil {
		t.Fatal(err)
	}
	dir, err := unix.Open(root, unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = unix.Close(dir) }()
	for _, tc := range []struct {
		rel  string
		mode uint32 // 0: refused
	}{
		{"a/b/f", unix.S_IFREG},
		{"a/../a/b/f", unix.S_IFREG},
		{"a/b/s", unix.S_IFLNK}, // the link itself, never its target
		{"l/f", 0},
		{"../f", 0},
		{"a/../../f", 0},
		{"/a/b/f", 0},
		{".", 0},
	} {
		fd, err := openBeneathNoFollow(dir, tc.rel)
		if tc.mode == 0 {
			if err == nil {
				_ = unix.Close(fd)
				t.Errorf("%q opened, want it refused", tc.rel)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: %v", tc.rel, err)
			continue
		}
		var st unix.Stat_t
		if err := unix.Fstat(fd, &st); err != nil || st.Mode&unix.S_IFMT != tc.mode {
			t.Errorf("%q opened mode %o, %v; want %o", tc.rel, st.Mode&unix.S_IFMT, err, tc.mode)
		}
		_ = unix.Close(fd)
	}
}
