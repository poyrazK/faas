//go:build linux

package storage

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func writePages(t *testing.T, path string, parts ...[]byte) []byte {
	t.Helper()
	var b []byte
	for _, p := range parts {
		b = append(b, p...)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return b
}

func pageRange(b []byte, from, to int) []byte { return b[from*pageSize : to*pageSize] }

func pageHashes(b []byte) []uint64 {
	h := make([]uint64, len(b)/pageSize)
	for i := range h {
		if pg := page(b, i); !bytes.Equal(pg, zeroPage) {
			h[i] = hashPage(pg)
		}
	}
	return h
}

func sharedExtentBytes(t *testing.T, path string) int64 {
	t.Helper()
	extents, err := fileExtents(path)
	if err != nil {
		t.Fatal(err)
	}
	var n int64
	for _, e := range extents {
		if e.shared {
			n += e.length
		}
	}
	return n
}

// peerFixture writes a processed memory file "a" and a new memory file "b"
// holding: an unknown page, a 30-page run of a starting at a's page 5 (its
// anchor is a's page 16, so the run must be extended backwards to be found
// whole), a zero page, a 4-page run of a (too short) and another unknown page.
func peerFixture(t *testing.T, dir string) (aPath, bPath string, b []byte) {
	t.Helper()
	a := randomPages(t, 48)
	aPath = filepath.Join(dir, "a.mem")
	writePages(t, aPath, a)
	other := randomPages(t, 2)
	bPath = filepath.Join(dir, "b.mem")
	b = writePages(t, bPath, page(other, 0), pageRange(a, 5, 35), make([]byte, pageSize), pageRange(a, 40, 44), page(other, 1))
	return aPath, bPath, b
}

// The anchors find a run that starts between them, and only runs of the
// minimum length are planned. Planning reads files only, so it runs on any
// filesystem.
func TestPeerIndexPlansRunsFoundThroughAnchors(t *testing.T) {
	dir := t.TempDir()
	aPath, _, b := peerFixture(t, dir)
	p := newPeerIndex()
	if err := p.addFile(t.Context(), aPath); err != nil {
		t.Fatal(err)
	}
	if got := len(p.idx.locs); got != 3 {
		t.Fatalf("indexed %d anchors, want 3 (pages 0, 16 and 32)", got)
	}
	if err := p.addFile(t.Context(), aPath); err != nil || len(p.idx.files) != 1 {
		t.Fatalf("second addFile: files=%d err=%v, want the file indexed once", len(p.idx.files), err)
	}
	s := pageSharer{ctx: t.Context(), open: map[*blockIndex]map[int32]*os.File{}}
	defer s.closeAll()
	src := newPeerPages(&s, p.idx)
	hashes := pageHashes(b)
	runs := p.plan(hashes, make([]bool, len(hashes)), -1, src)
	if len(runs) != 1 {
		t.Fatalf("planned %d runs, want 1: %+v", len(runs), runs)
	}
	want := shareRun{idx: p.idx, file: 0, srcOff: 5 * pageSize, dstOff: pageSize, length: 30 * pageSize}
	if runs[0] != want {
		t.Fatalf("run = %+v, want %+v", runs[0], want)
	}
	// A page another pass already shared ends the run there.
	taken := make([]bool, len(hashes))
	taken[10] = true
	runs = p.plan(hashes, taken, -1, src)
	if len(runs) != 1 || runs[0].dstOff != 11*pageSize || runs[0].length != 20*pageSize {
		t.Fatalf("runs around a shared page = %+v, want one 20-page run from page 11", runs)
	}
	// A file never matches its own anchors.
	if runs := p.plan(hashes, make([]bool, len(hashes)), 0, src); len(runs) != 0 {
		t.Fatalf("planned %d runs against the file itself", len(runs))
	}
}

// The pass shares the planned run with the earlier memory file, leaves every
// byte of both files as it was, and indexes the new file for later passes.
func TestPeerIndexSharesRunsBetweenMemoryFiles(t *testing.T) {
	dir := t.TempDir()
	aPath, bPath, want := peerFixture(t, dir)
	aBytes, err := os.ReadFile(aPath)
	if err != nil {
		t.Fatal(err)
	}
	p := newPeerIndex()
	if shared, err := p.share(t.Context(), aPath); err != nil || shared != 0 {
		t.Fatalf("first file shared %d bytes (err=%v), want 0", shared, err)
	}
	shared, err := p.share(t.Context(), bPath)
	for path, b := range map[string][]byte{aPath: aBytes, bPath: want} {
		if got, readErr := os.ReadFile(path); readErr != nil || !bytes.Equal(got, b) {
			t.Fatalf("%s bytes changed (err=%v)", path, readErr)
		}
	}
	if errors.Is(err, errDedupeUnsupported) {
		t.Skipf("%s cannot share blocks; planning and byte identity still verified", dir)
	}
	if err != nil {
		t.Fatal(err)
	}
	if shared != 30*pageSize {
		t.Fatalf("shared %d bytes, want %d", shared, 30*pageSize)
	}
	if got := sharedExtentBytes(t, bPath); got != 30*pageSize {
		t.Fatalf("FIEMAP reports %d shared bytes in b, want %d", got, 30*pageSize)
	}
	if len(p.idx.files) != 2 {
		t.Fatalf("index holds %d files, want both", len(p.idx.files))
	}
}

func cachePut(t *testing.T, cache StorageBackend, key string, b []byte) {
	t.Helper()
	if err := cache.Put(t.Context(), key, bytes.NewReader(b)); err != nil {
		t.Fatal(err)
	}
}

// requireBlockSharing skips the test where dir's filesystem cannot share
// blocks (ext4, tmpfs): a pass there finds nothing to report.
func requireBlockSharing(t *testing.T, dir string) {
	t.Helper()
	b := randomPages(t, 1)
	srcPath, dstPath := filepath.Join(dir, "probe.src"), filepath.Join(dir, "probe.dst")
	writePages(t, srcPath, b)
	writePages(t, dstPath, b)
	defer func() { _ = os.Remove(srcPath); _ = os.Remove(dstPath) }()
	src, err := os.Open(srcPath) //nolint:forbidigo // test fixture under t.TempDir.
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = src.Close() }()
	dst, err := os.OpenFile(dstPath, os.O_RDWR, 0) //nolint:forbidigo // test fixture under t.TempDir.
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = dst.Close() }()
	req := unix.FileDedupeRange{Src_length: pageSize, Info: []unix.FileDedupeRangeInfo{{Dest_fd: int64(dst.Fd())}}}
	if err := unix.IoctlFileDedupeRange(int(src.Fd()), &req); err != nil {
		t.Skipf("%s cannot share blocks: %v", dir, err)
	}
}

func newTestCache(t *testing.T, dir string) *LocalCacheBackend {
	t.Helper()
	parent, err := NewLocalStorageBackend(filepath.Join(dir, "parent"))
	if err != nil {
		t.Fatal(err)
	}
	cache, err := NewLocalCacheBackend(parent, filepath.Join(dir, "cache"), 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	return cache
}

// Pages the image pass shared stay shared with the image; the peer pass shares
// only what the image pass left. Both counts are reported.
func TestShareSnapshotMemorySharesWithImagesThenPeers(t *testing.T) {
	dir := t.TempDir()
	requireBlockSharing(t, dir)
	cache := newTestCache(t, dir)
	layer := randomPages(t, 2*minRunPages)
	heap := randomPages(t, minRunPages)
	cachePut(t, cache, "apps/app/dep.ext4", layer)
	// Both memory files hold the same 16 layer blocks and the same 16 heap
	// pages, which no image holds.
	memory := append(append([]byte{}, pageRange(layer, 0, minRunPages)...), heap...)
	cachePut(t, cache, "snap/dep/captures/a/v2/mem", memory)
	cachePut(t, cache, "snap/dep/captures/b/v2/mem", memory)
	x := NewMemoryShareIndex()
	first, err := x.ShareSnapshotMemory(t.Context(), cache, "snap/dep/captures/a/v2/mem", []string{"apps/app/dep.ext4"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := x.ShareSnapshotMemory(t.Context(), cache, "snap/dep/captures/b/v2/mem", []string{"apps/app/dep.ext4"})
	if err != nil {
		t.Fatal(err)
	}
	if first.SharedBytes != memoryShareMinRun || first.PeerSharedBytes != 0 {
		t.Fatalf("first = %+v, want the layer run shared and no peer", first)
	}
	if second.SharedBytes != memoryShareMinRun || second.PeerSharedBytes != memoryShareMinRun {
		t.Fatalf("second = %+v, want the layer run with the layer and the heap run with the first file", second)
	}
	path, _, _ := cache.LocalPath("snap/dep/captures/b/v2/mem")
	if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, memory) {
		t.Fatalf("memory bytes changed (err=%v)", err)
	}
	if level := memorySharedLevel(path); level != memorySharedWithPeers {
		t.Fatalf("marker level %d, want %d", level, memorySharedWithPeers)
	}
}

// After a restart, a file processed with peers is only indexed, a file
// processed before the amendment gets the peer pass, and a new file shares
// with both.
func TestShareSnapshotMemoryResumesAfterRestart(t *testing.T) {
	dir := t.TempDir()
	requireBlockSharing(t, dir)
	cache := newTestCache(t, dir)
	runA, runB := randomPages(t, minRunPages), randomPages(t, minRunPages)
	cachePut(t, cache, "snap/d/captures/a/v2/mem", runA)
	cachePut(t, cache, "snap/d/captures/b/v2/mem", append(append([]byte{}, runA...), runB...))
	cachePut(t, cache, "snap/d/captures/c/v2/mem", append(append([]byte{}, runB...), runA...))
	if _, err := NewMemoryShareIndex().ShareSnapshotMemory(t.Context(), cache, "snap/d/captures/a/v2/mem", nil); err != nil {
		t.Fatal(err)
	}
	bPath, _, _ := cache.LocalPath("snap/d/captures/b/v2/mem")
	setMemorySharedMarker(bPath, memorySharedWithImages) // processed before the amendment

	x := NewMemoryShareIndex() // a restarted daemon
	a, err := x.ShareSnapshotMemory(t.Context(), cache, "snap/d/captures/a/v2/mem", nil)
	if err != nil || !a.Skipped || len(x.peers.idx.files) != 1 {
		t.Fatalf("a = %+v, %v (indexed files %d); want skipped and indexed", a, err, len(x.peers.idx.files))
	}
	b, err := x.ShareSnapshotMemory(t.Context(), cache, "snap/d/captures/b/v2/mem", nil)
	if err != nil || b.Skipped || b.SharedBytes != 0 || b.PeerSharedBytes != memoryShareMinRun {
		t.Fatalf("b = %+v, %v; want only the peer pass, sharing a's run", b, err)
	}
	c, err := x.ShareSnapshotMemory(t.Context(), cache, "snap/d/captures/c/v2/mem", nil)
	if err != nil || c.PeerSharedBytes != 2*memoryShareMinRun {
		t.Fatalf("c = %+v, %v; want both runs shared", c, err)
	}
}

// With peer sharing off, a pass is the original image pass and marks level 1.
func TestShareSnapshotMemoryWithoutPeers(t *testing.T) {
	dir := t.TempDir()
	cache := newTestCache(t, dir)
	run := randomPages(t, minRunPages)
	cachePut(t, cache, "snap/d/captures/a/v2/mem", run)
	cachePut(t, cache, "snap/d/captures/b/v2/mem", run)
	x := NewMemoryShareIndex()
	x.SetPeerSharing(false)
	for _, key := range []string{"snap/d/captures/a/v2/mem", "snap/d/captures/b/v2/mem"} {
		got, err := x.ShareSnapshotMemory(t.Context(), cache, key, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got.Skipped {
			t.Skip("filesystem cannot share blocks")
		}
		if got.PeerSharedBytes != 0 {
			t.Fatalf("%s = %+v, want no peer sharing", key, got)
		}
		path, _, _ := cache.LocalPath(key)
		if level := memorySharedLevel(path); level != memorySharedWithImages {
			t.Fatalf("%s marker level %d, want %d", key, level, memorySharedWithImages)
		}
	}
}

// prune forgets a file the cache evicted, along with its anchors, and keeps
// the anchors of live files pointing at their files.
func TestPeerIndexPrunesEvictedFiles(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a.mem"), filepath.Join(dir, "b.mem")
	writePages(t, a, randomPages(t, 2*minRunPages))
	bBytes := writePages(t, b, randomPages(t, minRunPages))
	p := newPeerIndex()
	for _, path := range []string{a, b} {
		if err := p.addFile(t.Context(), path); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Remove(a); err != nil {
		t.Fatal(err)
	}
	p.prune() // within baseIndexMaxAge: nothing checked yet
	if len(p.idx.files) != 2 {
		t.Fatalf("pruned early: %d files", len(p.idx.files))
	}
	p.checked = time.Now().Add(-baseIndexMaxAge)
	p.prune()
	if len(p.idx.files) != 1 || p.idx.files[0].path != b || len(p.idx.locs) != 1 {
		t.Fatalf("after prune: files=%+v anchors=%d, want only b's one anchor", p.idx.files, len(p.idx.locs))
	}
	if loc := p.idx.locs[hashPage(page(bBytes, 0))]; loc.file != 0 || loc.off != 0 {
		t.Fatalf("b's anchor = %+v, want slot 0 offset 0", loc)
	}
	if err := p.addFile(t.Context(), b); err != nil || len(p.idx.files) != 1 {
		t.Fatalf("b indexed twice after prune: files=%d err=%v", len(p.idx.files), err)
	}
}
