//go:build linux

package storage

// Sharing between snapshot memory files — ADR-956 amendment 2026-10-31.
//
// After the image pass, a memory file still holds pages that other memory
// files on the node hold too: kernel text and data, the struct page array,
// runtime heaps. Measured on production compute-2 (27 cached memory files,
// 3.3 GB non-zero) runs of at least memoryShareMinRun shared another 659 MB,
// in ~44 runs per file. The same FIDEDUPERANGE rule applies: the kernel
// compares the bytes first, so each file keeps reading its own bytes.

import (
	"bytes"
	"context"
	"errors"
	"hash/maphash"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

const (
	// memoryShareMinRunPages is memoryShareMinRun in pages: the anchor
	// stride and the shortest run the peer pass shares.
	memoryShareMinRunPages = memoryShareMinRun / pageSize
	// peerChunkPages is how many pages of a source file are read and hashed
	// at a time while a run is extended. One anchor-sized chunk keeps a hit
	// that turns out shorter than a run to two small reads.
	peerChunkPages = memoryShareMinRunPages
	// peerChunkCacheLimit bounds the hashed source chunks one pass keeps.
	peerChunkCacheLimit = 256
)

var zeroPage = make([]byte, pageSize)

// peerIndex holds, for every memory file processed on this node, the hash of
// each non-zero page at a memoryShareMinRun-aligned offset. Any run of
// memoryShareMinRunPages consecutive pages of a file contains one such page,
// so these anchors find every run the pass may share while the index stays
// 1/16 the size of a page index: ~2,400 entries for a file with 150 MB of
// non-zero memory. A hit is extended page by page against the anchor's file.
// Files are recorded by inode and reopened when sharing, like image files.
type peerIndex struct {
	idx     *blockIndex
	slots   map[string]int32 // file identity -> idx.files slot
	checked time.Time
}

func newPeerIndex() *peerIndex {
	return &peerIndex{
		idx:     &blockIndex{locs: map[uint64]blockLoc{}},
		slots:   map[string]int32{},
		checked: time.Now(),
	}
}

// hashPage is the page hash of the peer index; 0 marks a zero page or hole.
func hashPage(page []byte) uint64 {
	if h := maphash.Bytes(pageSeed, page); h != 0 {
		return h
	}
	return 1
}

// prune drops the files the cache evicted or replaced, at most once per
// baseIndexMaxAge. A hash whose anchor was in a dropped file leaves the index
// until the next memory file holding it is processed.
func (p *peerIndex) prune() {
	if p == nil || time.Since(p.checked) < baseIndexMaxAge {
		return
	}
	p.checked = time.Now()
	remap := make([]int32, len(p.idx.files))
	live := make([]imageFile, 0, len(p.idx.files))
	for i, f := range p.idx.files {
		var st unix.Stat_t
		if unix.Stat(f.path, &st) != nil || uint64(st.Dev) != f.dev || st.Ino != f.ino { //nolint:unconvert // Dev's width differs by architecture.
			remap[i] = -1
			continue
		}
		remap[i] = int32(len(live))
		live = append(live, f)
	}
	if len(live) == len(p.idx.files) {
		return
	}
	for h, loc := range p.idx.locs {
		if to := remap[loc.file]; to < 0 {
			delete(p.idx.locs, h)
		} else {
			p.idx.locs[h] = blockLoc{file: to, off: loc.off}
		}
	}
	p.idx.files = live
	p.slots = make(map[string]int32, len(live))
	for i, f := range live {
		p.slots[identityString(f.path, f.dev, f.ino)] = int32(i)
	}
}

// register records f as an indexed file. It reports false when the file is
// already indexed.
func (p *peerIndex) register(f *os.File, path string) (int32, bool, error) {
	var st unix.Stat_t
	if err := unix.Fstat(int(f.Fd()), &st); err != nil {
		return 0, false, err
	}
	id := identityString(path, uint64(st.Dev), st.Ino) //nolint:unconvert // Dev's width differs by architecture.
	if _, ok := p.slots[id]; ok {
		return 0, false, nil
	}
	slot := int32(len(p.idx.files))
	p.idx.files = append(p.idx.files, imageFile{path: path, dev: uint64(st.Dev), ino: st.Ino}) //nolint:unconvert // Dev's width differs by architecture.
	p.slots[id] = slot
	return slot, true, nil
}

func (p *peerIndex) slotOf(f *os.File, path string) int32 {
	var st unix.Stat_t
	if unix.Fstat(int(f.Fd()), &st) != nil {
		return -1
	}
	if slot, ok := p.slots[identityString(path, uint64(st.Dev), st.Ino)]; ok { //nolint:unconvert // Dev's width differs by architecture.
		return slot
	}
	return -1
}

// anchor keeps the first place each hash was seen; any copy is equally valid.
func (p *peerIndex) anchor(h uint64, file int32, off int64) {
	if _, ok := p.idx.locs[h]; !ok {
		p.idx.locs[h] = blockLoc{file: file, off: off}
	}
}

// addFile indexes a memory file processed before this daemon started. Only
// the anchor pages are read: 4 KiB of every 64 KiB the file holds.
func (p *peerIndex) addFile(ctx context.Context, path string) error {
	if p == nil {
		return nil
	}
	f, err := os.Open(path) //nolint:forbidigo // cache-owned memory file; read only.
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	defer func() { _ = f.Close() }()
	file, isNew, err := p.register(f, path)
	if err != nil || !isNew {
		return err
	}
	page := make([]byte, pageSize)
	return forEachDataExtent(f, func(data, hole int64) error {
		first := (data + memoryShareMinRun - 1) &^ (memoryShareMinRun - 1)
		for pos := first; pos+pageSize <= hole; pos += memoryShareMinRun {
			if err := ctx.Err(); err != nil {
				return err
			}
			if _, err := f.ReadAt(page, pos); err != nil {
				return err
			}
			if !bytes.Equal(page, zeroPage) {
				p.anchor(hashPage(page), file, pos)
			}
		}
		return nil
	})
}

// share makes memPath share every run of at least memoryShareMinRun bytes
// that an indexed memory file holds at consecutive offsets, then indexes
// memPath's anchors. Pages already in a shared extent, which the image pass
// just shared, are left as they are.
func (p *peerIndex) share(ctx context.Context, memPath string) (int64, error) {
	if p == nil {
		return 0, nil
	}
	mem, err := os.OpenFile(memPath, os.O_RDWR, 0) //nolint:forbidigo // cache-owned memory file; FIDEDUPERANGE never changes its bytes.
	if err != nil {
		return 0, err
	}
	defer func() { _ = mem.Close() }()
	info, err := mem.Stat()
	if err != nil {
		return 0, err
	}
	pages := info.Size() / pageSize
	hashes := make([]uint64, pages)
	err = forEachDataPage(ctx, mem, func(off int64, page []byte) error {
		if i := off / pageSize; i < pages {
			hashes[i] = hashPage(page)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	taken, err := sharedPages(memPath, pages)
	if err != nil {
		return 0, err
	}
	s := pageSharer{ctx: ctx, mem: mem, open: map[*blockIndex]map[int32]*os.File{}}
	defer s.closeAll()
	src := newPeerPages(&s, p.idx)
	for _, run := range p.plan(hashes, taken, p.slotOf(mem, memPath), src) {
		if err := s.flushRun(run); err != nil {
			return s.shared, err
		}
	}
	file, isNew, err := p.register(mem, memPath)
	if err != nil || !isNew {
		return s.shared, err
	}
	for i := int64(0); i < pages; i += memoryShareMinRunPages {
		if hashes[i] != 0 {
			p.anchor(hashes[i], file, i*pageSize)
		}
	}
	return s.shared, nil
}

// plan returns the runs of at least memoryShareMinRunPages free pages that an
// indexed file other than self holds at consecutive offsets. A run is found
// through the anchor it contains and extended in both directions while the
// page hashes match; the kernel's comparison catches a hash collision.
func (p *peerIndex) plan(hashes []uint64, taken []bool, self int32, src *peerPages) []shareRun {
	free := func(i int) bool { return hashes[i] != 0 && !taken[i] }
	var runs []shareRun
	for i := 0; i < len(hashes); i++ {
		if !free(i) {
			continue
		}
		loc, ok := p.idx.locs[hashes[i]]
		if !ok || loc.file == self {
			continue
		}
		q := int(loc.off / pageSize)
		back := 0
		for i-back > 0 && q-back > 0 && free(i-back-1) && src.hash(loc.file, q-back-1) == hashes[i-back-1] {
			back++
		}
		fwd := 0
		for i+fwd < len(hashes) && free(i+fwd) && src.hash(loc.file, q+fwd) == hashes[i+fwd] {
			fwd++
		}
		if back+fwd < memoryShareMinRunPages {
			continue
		}
		for k := i - back; k < i+fwd; k++ {
			taken[k] = true
		}
		runs = append(runs, shareRun{
			idx:    p.idx,
			file:   loc.file,
			srcOff: int64(q-back) * pageSize,
			dstOff: int64(i-back) * pageSize,
			length: int64(back+fwd) * pageSize,
		})
		if fwd > 0 {
			i += fwd - 1
		}
	}
	return runs
}

// sharedPages marks the pages of path that lie in a shared extent.
func sharedPages(path string, pages int64) ([]bool, error) {
	taken := make([]bool, pages)
	ranges, err := sharedRanges(path)
	if errors.Is(err, errExtentsUnsupported) {
		return taken, nil
	}
	if err != nil {
		return nil, err
	}
	for _, r := range ranges {
		for i := r.start / pageSize; i < (r.end+pageSize-1)/pageSize && i < pages; i++ {
			taken[i] = true
		}
	}
	return taken, nil
}

type peerChunk struct {
	file  int32
	chunk int64
}

// peerPages hashes pages of indexed files on demand, a chunk at a time, for
// plan to extend runs. A file that was evicted or replaced hashes as zeros.
type peerPages struct {
	s      *pageSharer
	idx    *blockIndex
	chunks map[peerChunk][]uint64
	buf    []byte
}

func newPeerPages(s *pageSharer, idx *blockIndex) *peerPages {
	return &peerPages{s: s, idx: idx, chunks: map[peerChunk][]uint64{}, buf: make([]byte, peerChunkPages*pageSize)}
}

func (r *peerPages) hash(file int32, page int) uint64 {
	key := peerChunk{file: file, chunk: int64(page / peerChunkPages)}
	hs, ok := r.chunks[key]
	if !ok {
		if len(r.chunks) >= peerChunkCacheLimit {
			clear(r.chunks)
		}
		hs = make([]uint64, peerChunkPages)
		if f := r.s.source(r.idx, file); f != nil {
			n, _ := f.ReadAt(r.buf, key.chunk*int64(len(r.buf)))
			for i := 0; i+pageSize <= n; i += pageSize {
				if pg := r.buf[i : i+pageSize]; !bytes.Equal(pg, zeroPage) {
					hs[i/pageSize] = hashPage(pg)
				}
			}
		}
		r.chunks[key] = hs
	}
	return hs[page%peerChunkPages]
}

// flushRun shares a planned run in FIDEDUPERANGE calls of at most
// memoryShareRun bytes; a tail shorter than the minimum joins the call
// before it.
func (s *pageSharer) flushRun(run shareRun) error {
	for off := int64(0); off < run.length; {
		n := min(run.length-off, memoryShareRun)
		if rest := run.length - off - n; rest < memoryShareMinRun {
			n += rest
		}
		piece := run
		piece.srcOff += off
		piece.dstOff += off
		piece.length = n
		if err := s.flush(piece); err != nil {
			return err
		}
		off += n
	}
	return nil
}
