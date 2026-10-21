// adr: 908 — an app VM's base moves to pmem only as its own account's copy,
// and anything unexpected keeps virtio-blk.
package fcvm

import (
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/rootfs"
)

func TestPmemBaseSupported(t *testing.T) {
	for version, want := range map[string]bool{
		"1.7.0": false, "1.15.2": false, "1.16.0": true, "1.17.0": true, "": false, "x": false,
	} {
		if got := PmemBaseSupported(version); got != want {
			t.Errorf("PmemBaseSupported(%q) = %v, want %v", version, got, want)
		}
	}
}

func copyClone(clones *int) func(dst, src string) error {
	return func(dst, src string) error {
		*clones++
		in, err := os.Open(src) //nolint:forbidigo // test fixture under t.TempDir.
		if err != nil {
			return err
		}
		defer func() { _ = in.Close() }()
		out, err := os.Create(dst) //nolint:forbidigo // test fixture under t.TempDir.
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			_ = out.Close()
			return err
		}
		return out.Close()
	}
}

// ext4Image returns size bytes holding an ext4 superblock with the given
// block size and volume label; daxCapable reads nothing else.
func ext4Image(size int64, blockSize uint32, label string) []byte {
	b := make([]byte, size)
	logBlock := uint32(0)
	for 1024<<logBlock < blockSize {
		logBlock++
	}
	binary.LittleEndian.PutUint32(b[1024+24:], logBlock)
	binary.LittleEndian.PutUint16(b[1024+56:], 0xEF53)
	copy(b[1024+120:1024+136], label)
	return b
}

func pmemFixture(t *testing.T, size int64) (v *JailerVMM, base string, clones *int) {
	return pmemFixtureImage(t, ext4Image(size, 4096, PmemBaseLabel))
}

func pmemFixtureBlocks(t *testing.T, size int64, blockSize uint32) (v *JailerVMM, base string, clones *int) {
	return pmemFixtureImage(t, ext4Image(size, blockSize, PmemBaseLabel))
}

func pmemFixtureImage(t *testing.T, image []byte) (v *JailerVMM, base string, clones *int) {
	t.Helper()
	dir := t.TempDir()
	base = filepath.Join(dir, "base.ext4")
	if err := os.WriteFile(base, image, 0o644); err != nil {
		t.Fatal(err)
	}
	clones = new(int)
	v = (&JailerVMM{}).WithPmemBase(filepath.Join(dir, "pmem"))
	v.pmemBase.clone = copyClone(clones)
	return v, base, clones
}

func coldBootCfg(base string) VMConfig {
	return BuildColdBootConfig(ColdBootSpec{KernelKey: "k", BaseKey: base, LayerKey: "layer.ext4", VcpuCount: 2, MemSizeMiB: 256, Tap: "tap0"}, 1)
}

func TestWithPmemBaseUsesPerAccountCopies(t *testing.T) {
	v, base, clones := pmemFixture(t, 4<<20)
	a := v.withPmemBase(Lease{Instance: "a1", AccountID: "acct-a"}, coldBootCfg(base))
	if len(a.Pmem) != 1 || !a.Pmem[0].RootDevice || !a.Pmem[0].ReadOnly {
		t.Fatalf("pmem = %+v, want one read-only root device", a.Pmem)
	}
	if len(a.Drives) != 1 || a.Drives[0].DriveID != DriveLayer {
		t.Fatalf("drives = %+v, want only the app layer", a.Drives)
	}
	if !strings.Contains(a.BootSource.BootArgs, pmemRootArgs) || strings.Contains(a.BootSource.BootArgs, blkRootArgs) {
		t.Fatalf("boot args = %q", a.BootSource.BootArgs)
	}
	if a.Pmem[0].PathOnHost == base {
		t.Fatal("pmem points at the node-wide base, not an account copy")
	}
	again := v.withPmemBase(Lease{Instance: "a2", AccountID: "acct-a"}, coldBootCfg(base))
	b := v.withPmemBase(Lease{Instance: "b1", AccountID: "acct-b"}, coldBootCfg(base))
	if again.Pmem[0].PathOnHost != a.Pmem[0].PathOnHost {
		t.Fatal("one account got two copies")
	}
	if b.Pmem[0].PathOnHost == a.Pmem[0].PathOnHost {
		t.Fatal("two accounts share one copy")
	}
	if *clones != 2 {
		t.Fatalf("clones = %d, want one per account", *clones)
	}
}

func TestWithPmemBaseKeepsBlk(t *testing.T) {
	v, base, _ := pmemFixture(t, 4<<20)
	odd, oddBase, _ := pmemFixture(t, 3<<20)
	small, smallBase, _ := pmemFixtureBlocks(t, 4<<20, 1024)
	unlabelled, unlabelledBase, _ := pmemFixtureImage(t, ext4Image(4<<20, 4096, "applayer"))
	failing, failingBase, _ := pmemFixture(t, 4<<20)
	failing.pmemBase.clone = func(string, string) error { return errors.New("no reflink") }
	for name, tc := range map[string]struct {
		v     *JailerVMM
		lease Lease
		base  string
	}{
		"disabled":          {&JailerVMM{}, Lease{AccountID: "a"}, base},
		"no account":        {v, Lease{}, base},
		"builder":           {v, Lease{AccountID: "a", IsBuilder: true}, base},
		"networkless":       {v, Lease{AccountID: "a", Networkless: true}, base},
		"unaligned base":    {odd, Lease{AccountID: "a"}, oddBase},
		"1 KiB blocks":      {small, Lease{AccountID: "a"}, smallBase},
		"no pmem label":     {unlabelled, Lease{AccountID: "a"}, unlabelledBase},
		"clone unsupported": {failing, Lease{AccountID: "a"}, failingBase},
	} {
		got := tc.v.withPmemBase(tc.lease, coldBootCfg(tc.base))
		if got.Pmem != nil || len(got.Drives) != 2 || !strings.Contains(got.BootSource.BootArgs, blkRootArgs) {
			t.Errorf("%s: config moved to pmem: %+v", name, got)
		}
	}
}

func TestPmemBaseLabelMatchesRootfs(t *testing.T) {
	if PmemBaseLabel != rootfs.BasePmemLabel {
		t.Fatalf("fcvm label %q, rootfs stamps %q", PmemBaseLabel, rootfs.BasePmemLabel)
	}
}

func TestMountPointOf(t *testing.T) {
	info := filepath.Join(t.TempDir(), "mountinfo")
	table := `22 1 8:1 / / rw - ext4 /dev/sda1 rw
30 22 8:16 / /srv/fc rw - xfs /dev/sdb rw
31 22 8:16 /cache /var/lib/faas/cache rw - xfs /dev/sdb rw
32 30 0:40 / /srv/fc/jail rw - tmpfs tmpfs rw
33 22 8:17 / /mnt/with\040space rw - xfs /dev/sdc rw
`
	if err := os.WriteFile(info, []byte(table), 0o644); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{
		"/var/lib/faas/cache/ab/base": "/var/lib/faas/cache", // bind mount, not /srv/fc
		"/srv/fc/base/node22.ext4":    "/srv/fc",
		"/srv/fc/jail/x":              "/srv/fc/jail",
		"/srv/fcx/base":               "/",
		"/mnt/with space/base":        "/mnt/with space",
	} {
		got, err := mountPointOf(info, path)
		if err != nil || got != want {
			t.Errorf("mountPointOf(%q) = %q, %v; want %q", path, got, err, want)
		}
	}
}

// adr: 908 — by default a copy lives on its base's own mount, a replaced base
// gets a new copy while a touched one keeps its copy, and copies nobody used
// for a week are swept.
func TestPmemBaseAutoDirVersionsAndSweep(t *testing.T) {
	mount, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	info := filepath.Join(t.TempDir(), "mountinfo")
	if err := os.WriteFile(info, []byte("22 1 8:1 / / rw - ext4 /dev/sda1 rw\n30 22 8:16 / "+mount+" rw - xfs /dev/sdb rw\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(mount, "cache", "ab", "base")
	if err := os.MkdirAll(filepath.Dir(base), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(base, ext4Image(4<<20, 4096, PmemBaseLabel), 0o644); err != nil {
		t.Fatal(err)
	}
	clones := 0
	v := (&JailerVMM{}).WithPmemBase(PmemBaseDirAuto)
	v.pmemBase.clone = copyClone(&clones)
	v.pmemBase.mountinfo = info
	first, err := v.pmemBase.accountCopy("acct", base)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(first, filepath.Join(mount, pmemAutoDirName)+"/") {
		t.Fatalf("copy %s is not under %s", first, filepath.Join(mount, pmemAutoDirName))
	}
	// The node cache bumps a base's mtime on every read (its LRU signal).
	// That is not a new base: the account keeps its copy and its warm page
	// cache instead of cloning on every wake.
	later := time.Now().Add(time.Minute)
	if err := os.Chtimes(base, later, later); err != nil {
		t.Fatal(err)
	}
	touched, err := v.pmemBase.accountCopy("acct", base)
	if err != nil || touched != first || clones != 1 {
		t.Fatalf("after an LRU touch: copy %s (first %s), clones %d, err %v", touched, first, clones, err)
	}
	// A replaced base (writers rename a new file into place) gets a new copy.
	next := base + ".next"
	if err := os.WriteFile(next, ext4Image(4<<20, 4096, PmemBaseLabel), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(next, base); err != nil {
		t.Fatal(err)
	}
	second, err := v.pmemBase.accountCopy("acct", base)
	if err != nil || second == first || clones != 2 {
		t.Fatalf("after the base was replaced: copy %s (first %s), clones %d, err %v", second, first, clones, err)
	}
	// A week later, the first copy is swept and the one in use stays.
	idle := time.Now().Add(-pmemCopyMaxIdle - time.Hour)
	if err := os.Chtimes(first, idle, idle); err != nil {
		t.Fatal(err)
	}
	v.pmemBase.swept = map[string]time.Time{}
	if _, err := v.pmemBase.accountCopy("acct", base); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(first); !os.IsNotExist(err) {
		t.Errorf("idle copy %s survived the sweep: %v", first, err)
	}
	if _, err := os.Stat(second); err != nil {
		t.Errorf("copy in use was swept: %v", err)
	}
}

func TestWithPmemBaseOff(t *testing.T) {
	for _, dir := range []string{"", "off"} {
		if (&JailerVMM{}).WithPmemBase(dir).pmemBase != nil {
			t.Errorf("WithPmemBase(%q) enabled pmem", dir)
		}
	}
}
