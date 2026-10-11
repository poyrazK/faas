//go:build linux

package storage

import (
	"bytes"
	"context"
	"errors"
	"hash/maphash"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

const (
	pageSize = 4096
	// memoryShareRun caps one FIDEDUPERANGE call; consecutive memory pages
	// that are consecutive blocks of one image are shared together.
	memoryShareRun = 1 << 20
	// memorySharedXattr marks a memory file whose pages were shared. A cache
	// refresh writes a new file, which starts without it.
	memorySharedXattr = "user.faas.memory-shared"
)

var pageSeed = maphash.MakeSeed()

type imageFile struct {
	path     string
	dev, ino uint64
}

type blockLoc struct {
	file int32
	off  int64
}

// blockIndex maps the hash of every non-zero 4 KiB block of some image files
// to one place that holds it. Files are recorded by inode and reopened when
// sharing, so the index never pins a replaced or evicted image on disk.
type blockIndex struct {
	files []imageFile
	locs  map[uint64]blockLoc
}

func fileIdentity(path string) (string, error) {
	var st unix.Stat_t
	if err := unix.Stat(path, &st); err != nil {
		return "", err
	}
	return identityString(path, uint64(st.Dev), st.Ino), nil //nolint:unconvert // Dev's width differs by architecture.
}

func indexImageFiles(ctx context.Context, paths []string) (*blockIndex, error) {
	idx := &blockIndex{locs: map[uint64]blockLoc{}}
	for _, path := range paths {
		if err := idx.add(ctx, path); err != nil {
			return nil, err
		}
	}
	return idx, nil
}

func (idx *blockIndex) add(ctx context.Context, path string) error {
	f, err := os.Open(path) //nolint:forbidigo // cache-owned image path; read only.
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil // evicted between listing and indexing
		}
		return err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	file := int32(len(idx.files))
	idx.files = append(idx.files, imageFile{path: path, dev: uint64(st.Dev), ino: st.Ino}) //nolint:unconvert // Dev's width differs by architecture.
	return forEachDataPage(ctx, f, func(off int64, page []byte) error {
		h := maphash.Bytes(pageSeed, page)
		if _, ok := idx.locs[h]; !ok {
			idx.locs[h] = blockLoc{file: file, off: off}
		}
		return nil
	})
}

// forEachDataPage calls fn for every whole, non-zero 4 KiB page in f's
// allocated extents. Holes are skipped without reading them.
func forEachDataPage(ctx context.Context, f *os.File, fn func(off int64, page []byte) error) error {
	info, err := f.Stat()
	if err != nil {
		return err
	}
	size := info.Size()
	zero := make([]byte, pageSize)
	buf := make([]byte, memoryShareRun)
	for off := int64(0); off < size; {
		data, err := unix.Seek(int(f.Fd()), off, unix.SEEK_DATA)
		if errors.Is(err, unix.ENXIO) {
			return nil
		}
		if err != nil {
			return err
		}
		hole, err := unix.Seek(int(f.Fd()), data, unix.SEEK_HOLE)
		if err != nil {
			return err
		}
		// Data extents start on filesystem block boundaries; align anyway so
		// every page offset is valid for FIDEDUPERANGE.
		for pos := data &^ (pageSize - 1); pos < hole; {
			if err := ctx.Err(); err != nil {
				return err
			}
			n, err := f.ReadAt(buf[:min(int64(len(buf)), hole-pos)], pos)
			if n == 0 && err != nil {
				return err
			}
			for i := 0; i+pageSize <= n; i += pageSize {
				page := buf[i : i+pageSize]
				if bytes.Equal(page, zero) {
					continue
				}
				if err := fn(pos+int64(i), page); err != nil {
					return err
				}
			}
			pos += int64(n) &^ (pageSize - 1)
			if n < pageSize {
				break
			}
		}
		off = hole
	}
	return nil
}

// shareRun is a run of memory pages that are consecutive blocks of one image.
type shareRun struct {
	idx    *blockIndex
	file   int32
	srcOff int64
	dstOff int64
	length int64
}

// shareMemoryPages shares every page of memPath found in the indexes, trying
// them in order, and returns the bytes the kernel reports shared.
func shareMemoryPages(ctx context.Context, memPath string, indexes ...*blockIndex) (int64, error) {
	mem, err := os.OpenFile(memPath, os.O_RDWR, 0) //nolint:forbidigo // cache-owned memory file; FIDEDUPERANGE never changes its bytes.
	if err != nil {
		return 0, err
	}
	defer func() { _ = mem.Close() }()
	s := pageSharer{ctx: ctx, mem: mem, open: map[*blockIndex]map[int32]*os.File{}}
	defer s.closeAll()
	var run shareRun
	err = forEachDataPage(ctx, mem, func(off int64, page []byte) error {
		h := maphash.Bytes(pageSeed, page)
		for _, idx := range indexes {
			if idx == nil {
				continue
			}
			loc, ok := idx.locs[h]
			if !ok {
				continue
			}
			if run.length > 0 && run.idx == idx && run.file == loc.file &&
				run.srcOff+run.length == loc.off && run.dstOff+run.length == off &&
				run.length < memoryShareRun {
				run.length += pageSize
				return nil
			}
			if err := s.flush(run); err != nil {
				return err
			}
			run = shareRun{idx: idx, file: loc.file, srcOff: loc.off, dstOff: off, length: pageSize}
			return nil
		}
		return nil
	})
	if err == nil {
		err = s.flush(run)
	}
	return s.shared, err
}

type pageSharer struct {
	ctx    context.Context
	mem    *os.File
	open   map[*blockIndex]map[int32]*os.File
	shared int64
}

// source opens an indexed image, or returns nil when it was replaced or
// removed since it was indexed.
func (s *pageSharer) source(idx *blockIndex, file int32) *os.File {
	files := s.open[idx]
	if files == nil {
		files = map[int32]*os.File{}
		s.open[idx] = files
	}
	if f, ok := files[file]; ok {
		return f
	}
	want := idx.files[file]
	f, err := os.Open(want.path) //nolint:forbidigo // cache-owned image path; source of FIDEDUPERANGE only.
	if err == nil {
		var st unix.Stat_t
		if unix.Fstat(int(f.Fd()), &st) != nil || uint64(st.Dev) != want.dev || st.Ino != want.ino { //nolint:unconvert // Dev's width differs by architecture.
			_ = f.Close()
			f = nil
		}
	} else {
		f = nil
	}
	files[file] = f
	return f
}

func (s *pageSharer) flush(run shareRun) error {
	if run.length == 0 {
		return nil
	}
	src := s.source(run.idx, run.file)
	if src == nil {
		return nil
	}
	same, err := s.dedupe(src, run.srcOff, run.dstOff, run.length)
	if err != nil || same || run.length == pageSize {
		return err
	}
	// Some page differs (a hash collision or a block that changed): share
	// the run page by page so every other page still shares.
	for off := int64(0); off < run.length; off += pageSize {
		if _, err := s.dedupe(src, run.srcOff+off, run.dstOff+off, pageSize); err != nil {
			return err
		}
	}
	return nil
}

func (s *pageSharer) dedupe(src *os.File, srcOff, dstOff, length int64) (bool, error) {
	if err := s.ctx.Err(); err != nil {
		return false, err
	}
	req := unix.FileDedupeRange{
		Src_offset: uint64(srcOff),
		Src_length: uint64(length),
		Info:       []unix.FileDedupeRangeInfo{{Dest_fd: int64(s.mem.Fd()), Dest_offset: uint64(dstOff)}},
	}
	if err := unix.IoctlFileDedupeRange(int(src.Fd()), &req); err != nil {
		if errors.Is(err, unix.EINVAL) {
			return false, nil
		}
		return false, errDedupeUnsupported
	}
	info := req.Info[0]
	if info.Status == unix.FILE_DEDUPE_RANGE_SAME {
		s.shared += int64(info.Bytes_deduped)
		return true, nil
	}
	return false, nil
}

func (s *pageSharer) closeAll() {
	for _, files := range s.open {
		for _, f := range files {
			if f != nil {
				_ = f.Close()
			}
		}
	}
}

func memorySharedMarker(path string) bool {
	buf := make([]byte, 8)
	_, err := unix.Getxattr(path, memorySharedXattr, buf)
	return err == nil
}

// setMemorySharedMarker is best-effort: without user xattrs the in-process
// record still prevents repeating the pass until vmmd restarts.
func setMemorySharedMarker(path string) {
	_ = unix.Setxattr(path, memorySharedXattr, []byte("1"), 0)
}
