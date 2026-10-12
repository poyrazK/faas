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
// block size; daxCapable reads nothing else.
func ext4Image(size int64, blockSize uint32) []byte {
	b := make([]byte, size)
	logBlock := uint32(0)
	for 1024<<logBlock < blockSize {
		logBlock++
	}
	binary.LittleEndian.PutUint32(b[1024+24:], logBlock)
	binary.LittleEndian.PutUint16(b[1024+56:], 0xEF53)
	return b
}

func pmemFixture(t *testing.T, size int64) (v *JailerVMM, base string, clones *int) {
	return pmemFixtureBlocks(t, size, 4096)
}

func pmemFixtureBlocks(t *testing.T, size int64, blockSize uint32) (v *JailerVMM, base string, clones *int) {
	t.Helper()
	dir := t.TempDir()
	base = filepath.Join(dir, "base.ext4")
	if err := os.WriteFile(base, ext4Image(size, blockSize), 0o644); err != nil {
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
		"clone unsupported": {failing, Lease{AccountID: "a"}, failingBase},
	} {
		got := tc.v.withPmemBase(tc.lease, coldBootCfg(tc.base))
		if got.Pmem != nil || len(got.Drives) != 2 || !strings.Contains(got.BootSource.BootArgs, blkRootArgs) {
			t.Errorf("%s: config moved to pmem: %+v", name, got)
		}
	}
}
