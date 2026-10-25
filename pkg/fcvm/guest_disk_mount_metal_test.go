//go:build linux && metal

package fcvm

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Every host mount of a guest-written disk (pre-boot staging, the job
// manifest drive, builder export) takes guestDiskMountArgs. The guest
// controls that disk's superblock, so on a real kernel the mount must
// override an error behaviour the guest set to panic, keep the mount inert,
// and refuse a disk the guest reformatted to another filesystem.
func TestMetalGuestDiskMountOptionsOverrideTheGuestSuperblock(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("loop mounts need root")
	}
	for _, tool := range []string{"mkfs.ext4", "tune2fs", "mkfs.vfat", "mount", "umount"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not installed", tool)
		}
	}
	run := func(name string, args ...string) string {
		t.Helper()
		out, err := exec.Command(name, args...).CombinedOutput()
		if err != nil {
			t.Fatalf("%s %v: %v\n%s", name, args, err, out)
		}
		return string(out)
	}
	dir := t.TempDir()
	image := filepath.Join(dir, "drive1.ext4")
	run("truncate", "-s", "16M", image)
	run("mkfs.ext4", "-q", "-F", image)
	run("tune2fs", "-e", "panic", image)
	if sb := run("tune2fs", "-l", image); !strings.Contains(sb, "Panic") {
		t.Fatalf("superblock does not ask to panic on error:\n%s", sb)
	}

	for _, mode := range []string{"rw", "ro", "ro,noload"} {
		mountpoint := filepath.Join(dir, "mnt-"+strings.ReplaceAll(mode, ",", "-"))
		if err := os.Mkdir(mountpoint, 0o700); err != nil {
			t.Fatal(err)
		}
		run("mount", guestDiskMountArgs(mode, image, mountpoint)...)
		fstype, mountOpts, superOpts := mountInfo(t, mountpoint)
		run("umount", mountpoint)
		if fstype != "ext4" {
			t.Errorf("%s: mounted as %q, want ext4", mode, fstype)
		}
		for _, want := range []string{"nodev", "nosuid", "noexec"} {
			if !slices.Contains(mountOpts, want) {
				t.Errorf("%s: mount options %v lack %s", mode, mountOpts, want)
			}
		}
		if strings.HasPrefix(mode, "ro") && !slices.Contains(mountOpts, "ro") {
			t.Errorf("%s: mount options %v are not read-only", mode, mountOpts)
		}
		// ext4 lists errors= only when it differs from the superblock's
		// behaviour, so this also shows the guest's panic was overridden.
		if !slices.Contains(superOpts, "errors=remount-ro") {
			t.Errorf("%s: superblock options %v lack errors=remount-ro", mode, superOpts)
		}
	}

	// A guest that reformatted its disk is not parsed as another filesystem.
	vfat := filepath.Join(dir, "drive1.vfat")
	run("truncate", "-s", "16M", vfat)
	run("mkfs.vfat", vfat)
	mountpoint := filepath.Join(dir, "mnt-vfat")
	if err := os.Mkdir(mountpoint, 0o700); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("mount", guestDiskMountArgs("rw", vfat, mountpoint)...).CombinedOutput(); err == nil {
		_ = exec.Command("umount", mountpoint).Run()
		t.Fatalf("a vfat disk mounted as ext4: %s", out)
	}
}

// mountInfo reads mountpoint's filesystem type and its per-mount and
// superblock options from /proc/self/mountinfo.
func mountInfo(t *testing.T, mountpoint string) (string, []string, []string) {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(mountpoint)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		sep := slices.Index(fields, "-")
		if len(fields) < 6 || sep < 0 || len(fields) < sep+4 || fields[4] != resolved {
			continue
		}
		return fields[sep+1], strings.Split(fields[5], ","), strings.Split(fields[sep+3], ",")
	}
	t.Fatalf("%s is not mounted", resolved)
	return "", nil, nil
}
