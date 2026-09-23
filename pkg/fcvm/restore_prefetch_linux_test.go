//go:build linux

package fcvm

import (
	"bytes"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// adr: 226 — /proc/<pid>/maps lines are matched by inode and device.
func TestParseMapsLine(t *testing.T) {
	v, ok := parseMapsLine("7f0000000000-7f0040000000 rw-p 00001000 fd:02 131 /mem")
	if !ok {
		t.Fatal("valid line rejected")
	}
	want := mapsVMA{start: 0x7f0000000000, end: 0x7f0040000000, offset: 0x1000, major: 0xfd, minor: 2, inode: 131}
	if v != want {
		t.Fatalf("parsed %+v, want %+v", v, want)
	}
	for _, bad := range []string{
		"", "7f00-7e00 rw-p 0 fd:02 131 /mem", "7f00-7f10 rw-p 0 fd:02 0",
		"zz-7f10 rw-p 0 fd:02 5 /x", "7f00-7f10 rw-p 0 fd02 5 /x",
		"ffffffffffffff00-ffffffffffffff10 rw-p 0 fd:02 5 /x",
	} {
		if _, ok := parseMapsLine(bad); ok {
			t.Errorf("parseMapsLine(%q) accepted a malformed or anonymous mapping", bad)
		}
	}
}

// adr: 226 — only present or swapped pagemap entries count as touched.
func TestTouchedPages(t *testing.T) {
	const page = 4096
	entries := []uint64{0, pagemapPresent | 42, 0, pagemapSwapped, pagemapPresent}
	buf := new(bytes.Buffer)
	for _, e := range entries {
		_ = binary.Write(buf, binary.LittleEndian, e)
	}
	got, err := touchedPages(bytes.NewReader(buf.Bytes()), 0, int64(len(entries)*page), page)
	if err != nil {
		t.Fatal(err)
	}
	if want := []int64{1, 3, 4}; !reflect.DeepEqual(got, want) {
		t.Fatalf("touched pages %v, want %v", got, want)
	}
}

// touchSink keeps the test's page reads from being optimised away.
var touchSink byte

// evictFromPageCache writes back and drops path's cached pages. On tmpfs the
// drop is a no-op, which only makes the assertions below looser, never wrong.
func evictFromPageCache(t *testing.T, path string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if err := f.Sync(); err != nil {
		t.Fatal(err)
	}
	if err := unix.Fadvise(int(f.Fd()), 0, 0, unix.FADV_DONTNEED); err != nil {
		t.Fatal(err)
	}
}

// adr: 226 — end to end against a real MAP_PRIVATE file mapping (the shape
// Firecracker uses for guest memory): every page read or written is reported
// as a file range, and nothing outside the kernel's fault-around window of a
// touched page is.
func TestTouchedFileRangesOwnMapping(t *testing.T) {
	const window = 64 << 10 // fault_around_bytes default
	page := int64(os.Getpagesize())
	size := int64(4 << 20)
	path := filepath.Join(t.TempDir(), "mem")
	if err := os.WriteFile(path, make([]byte, size), 0o600); err != nil {
		t.Fatal(err)
	}
	evictFromPageCache(t, path)
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	b, err := unix.Mmap(int(f.Fd()), 0, int(size), unix.PROT_READ|unix.PROT_WRITE, unix.MAP_PRIVATE)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = unix.Munmap(b) }()
	touched := []int64{0, 1<<20 + 3*page, 3<<20 + page}
	touchSink += b[touched[0]]     // read fault
	b[touched[1]] = 1              // write fault (private copy)
	touchSink += b[touched[2]+100] // read fault
	got, err := touchedFileRanges(os.Getpid(), path)
	if err != nil {
		t.Fatal(err)
	}
	covered := func(off int64) bool {
		for _, r := range got {
			if off >= r.Off && off < r.Off+r.Len {
				return true
			}
		}
		return false
	}
	for _, off := range touched {
		if !covered(off) {
			t.Errorf("touched page at %d missing from %v", off, got)
		}
	}
	for _, r := range got {
		near := false
		for _, off := range touched {
			w := off / window * window
			if r.Off >= w && r.Off+r.Len <= w+window {
				near = true
			}
		}
		if !near {
			t.Errorf("range %+v lies outside every touched page's fault-around window", r)
		}
	}
}

// adr: 226 — a prefetch must cover every recorded byte, not just the head of
// each range: the kernel truncates one FADV_WILLNEED to the readahead window.
func TestAdviseWillNeedCoversWholeRanges(t *testing.T) {
	const size = 16 << 20
	path := filepath.Join(t.TempDir(), "mem")
	if err := os.WriteFile(path, bytes.Repeat([]byte{1}, size), 0o600); err != nil {
		t.Fatal(err)
	}
	evictFromPageCache(t, path)
	ranges := []fileRange{{0, 4 << 20}, {8 << 20, 6 << 20}}
	if err := adviseWillNeed(path, ranges); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	b, err := unix.Mmap(int(f.Fd()), 0, size, unix.PROT_READ, unix.MAP_SHARED)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = unix.Munmap(b) }()
	page := os.Getpagesize()
	deadline := time.Now().Add(10 * time.Second)
	for {
		missing := 0
		for _, r := range ranges {
			vec := make([]byte, int(r.Len)/page)
			region := b[r.Off : r.Off+r.Len]
			if _, _, errno := unix.Syscall(unix.SYS_MINCORE, uintptr(unsafe.Pointer(&region[0])), uintptr(len(region)), uintptr(unsafe.Pointer(&vec[0]))); errno != 0 {
				t.Fatal(errno)
			}
			for _, v := range vec {
				if v&1 == 0 {
					missing++
				}
			}
		}
		if missing == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d advised pages never reached the page cache", missing)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// adr: 226 — recording reads the live process's page table off the wake
// path and stores the family's working set; the test process stands in for
// Firecracker by mapping the mem file itself.
func TestRecordRestoreWorkingSet(t *testing.T) {
	page := os.Getpagesize()
	path := filepath.Join(t.TempDir(), "mem")
	if err := os.WriteFile(path, make([]byte, 64*page), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	b, err := unix.Mmap(int(f.Fd()), 0, 64*page, unix.PROT_READ, unix.MAP_PRIVATE)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = unix.Munmap(b) }()
	touchSink += b[40*page]
	self, err := os.FindProcess(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	v := NewJailerVMM(t.TempDir(), 0)
	v.proc["self"] = &exec.Cmd{Process: self}
	v.recs["self"] = &instanceRecord{}
	const key = "snap/dep-rec/captures/c1/v2/mem"
	v.recordRestoreWorkingSet("self", key, path)
	v.recordRestoreWorkingSet("gone", key, path) // no live process: ignored
	deadline := time.Now().Add(5 * time.Second)
	for {
		if set, ok := v.restorePrefetch.get("snap/dep-rec"); ok {
			found := false
			for _, r := range set.ranges {
				if int64(40*page) >= r.Off && int64(40*page) < r.Off+r.Len {
					found = true
				}
			}
			if !found || set.bytes <= 0 {
				t.Fatalf("recorded set %+v does not cover the touched page", set)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("working set was never recorded")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
