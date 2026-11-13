//go:build metal && linux

// adr: 510 — amendment 2: on a real host filesystem, vmmd's recorded digest
// survives a restart (a new Manager) and never follows a replacement renamed
// over the image.
package fcvm

import (
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"golang.org/x/sys/unix"
)

func TestMetalRecordedBackingDigestSurvivesRestart(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("trusted xattrs need CAP_SYS_ADMIN; run as root (make test-metal)")
	}
	dir := t.TempDir()
	base := filepath.Join(dir, "base.ext4")
	writeFile(t, base, "base-layout-a")
	probe := []byte("probe")
	if err := unix.Setxattr(base, backingDigestXattr, probe, 0); errors.Is(err, unix.ENOTSUP) {
		t.Skipf("%s has no trusted xattrs", dir)
	} else if err != nil {
		t.Fatal(err)
	}
	if err := unix.Removexattr(base, backingDigestXattr); err != nil {
		t.Fatal(err)
	}

	newManager := func() (*Manager, *atomic.Int32) {
		m := NewManager(&fakeRunner{}, &fakeVMM{}, Paths{Kernel: filepath.Join(dir, "vmlinux")}, testFCVersion, nil, nil)
		var hashes atomic.Int32
		m.beforeBackingHash = func(string) { hashes.Add(1) }
		return m, &hashes
	}
	first, firstHashes := newManager()
	digest, err := first.fileDigest(base)
	if err != nil || firstHashes.Load() != 1 {
		t.Fatalf("first identification = %q %v after %d reads, want one read", digest, err, firstHashes.Load())
	}
	buf := make([]byte, 160)
	n, err := unix.Getxattr(base, backingDigestXattr, buf)
	if err != nil {
		t.Fatalf("digest not recorded: %v", err)
	}
	if got, ok := parseRecordedBackingDigest(string(buf[:n]), int64(len("base-layout-a"))); !ok || got != digest {
		t.Fatalf("recorded %q, want %q", buf[:n], digest)
	}

	restarted, restartedHashes := newManager()
	if again, err := restarted.fileDigest(base); err != nil || again != digest || restartedHashes.Load() != 0 {
		t.Fatalf("after restart = %q %v after %d reads, want recorded %q without a read", again, err, restartedHashes.Load(), digest)
	}

	// Same size, new content, renamed into place: the new inode carries no
	// record, so it is read and identified as different.
	replaceFile(t, base, "base-layout-b")
	replaced, err := restarted.fileDigest(base)
	if err != nil || replaced == digest || restartedHashes.Load() != 1 {
		t.Fatalf("after replacement = %q %v after %d reads, want a new digest from one read", replaced, err, restartedHashes.Load())
	}
}
