//go:build linux

package fcvm

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"golang.org/x/sys/unix"
)

// adr: 224 — /proc/<pid>/maps lines are matched by inode and device.
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
	} {
		if _, ok := parseMapsLine(bad); ok {
			t.Errorf("parseMapsLine(%q) accepted a malformed or anonymous mapping", bad)
		}
	}
}

// adr: 224 — only present or swapped pagemap entries count as touched.
func TestTouchedPages(t *testing.T) {
	const page = 4096
	entries := []uint64{0, pagemapPresent | 42, 0, pagemapSwapped, pagemapPresent}
	buf := new(bytes.Buffer)
	for _, e := range entries {
		_ = binary.Write(buf, binary.LittleEndian, e)
	}
	got, err := touchedPages(bytes.NewReader(buf.Bytes()), 0, uint64(len(entries)*page), page)
	if err != nil {
		t.Fatal(err)
	}
	if want := []int64{1, 3, 4}; !reflect.DeepEqual(got, want) {
		t.Fatalf("touched pages %v, want %v", got, want)
	}
}

// touchSink keeps the test's page reads from being optimised away.
var touchSink byte

// adr: 224 — end to end against a real MAP_PRIVATE file mapping (the shape
// Firecracker uses for guest memory): exactly the pages read or written are
// reported, as file offsets.
func TestTouchedFileRangesOwnMapping(t *testing.T) {
	page := os.Getpagesize()
	path := filepath.Join(t.TempDir(), "mem")
	if err := os.WriteFile(path, make([]byte, 16*page), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	b, err := unix.Mmap(int(f.Fd()), 0, 16*page, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_PRIVATE)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = unix.Munmap(b) }()
	touchSink += b[2*page]      // read fault
	b[3*page] = 1               // write fault (private copy)
	touchSink += b[10*page+100] // read fault
	got, err := touchedFileRanges(os.Getpid(), path)
	if err != nil {
		t.Fatal(err)
	}
	p := int64(page)
	want := []fileRange{{2 * p, 2 * p}, {10 * p, p}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("touched ranges %v, want %v", got, want)
	}
}
