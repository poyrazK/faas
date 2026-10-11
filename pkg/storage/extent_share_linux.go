//go:build linux

package storage

// Block sharing primitives for ADR-633. FICLONE and FIDEDUPERANGE only ever
// share blocks whose bytes are identical (FIDEDUPERANGE compares them in the
// kernel first), so neither can change what a reader of either file sees.

import (
	"context"
	"errors"
	"os"
	"unsafe"

	"golang.org/x/sys/unix"
)

const (
	fsIocFiemap          = 0xC020660B // _IOWR('f', 11, struct fiemap)
	fiemapFlagSync       = 0x1
	fiemapExtentLast     = 0x1
	fiemapExtentUnknown  = 0x2
	fiemapExtentDelalloc = 0x4
	fiemapExtentShared   = 0x2000
	fiemapBatch          = 512
)

type fiemapHeader struct {
	start         uint64
	length        uint64
	flags         uint32
	mappedExtents uint32
	extentCount   uint32
	reserved      uint32
}

type fiemapExtent struct {
	logical    uint64
	physical   uint64
	length     uint64
	reserved64 [2]uint64
	flags      uint32
	reserved   [3]uint32
}

// fileExtents lists path's allocated extents with FS_IOC_FIEMAP.
func fileExtents(path string) ([]physicalExtent, error) {
	f, err := os.Open(path) //nolint:forbidigo // cache-owned entry path; metadata only.
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	headerSize := int(unsafe.Sizeof(fiemapHeader{}))
	extentSize := int(unsafe.Sizeof(fiemapExtent{}))
	buf := make([]byte, headerSize+fiemapBatch*extentSize)
	var out []physicalExtent
	var start uint64
	for {
		clear(buf)
		header := (*fiemapHeader)(unsafe.Pointer(&buf[0])) //nolint:gosec // G103: FS_IOC_FIEMAP fills a caller-owned buffer; x/sys has no wrapper.
		header.start = start
		header.length = ^uint64(0) - start
		header.flags = fiemapFlagSync
		header.extentCount = fiemapBatch
		if _, _, errno := unix.Syscall(unix.SYS_IOCTL, f.Fd(), fsIocFiemap, uintptr(unsafe.Pointer(&buf[0]))); errno != 0 { //nolint:gosec // G103: see above.
			if errors.Is(errno, unix.EOPNOTSUPP) || errors.Is(errno, unix.ENOTTY) {
				return nil, errExtentsUnsupported
			}
			return nil, errno
		}
		mapped := int(header.mappedExtents)
		if mapped == 0 {
			return out, nil
		}
		last := false
		for i := range mapped {
			x := (*fiemapExtent)(unsafe.Pointer(&buf[headerSize+i*extentSize])) //nolint:gosec // G103: see above.
			// A delayed or unknown placement has no physical address yet;
			// count it as the file's own bytes.
			unplaced := x.flags&(fiemapExtentUnknown|fiemapExtentDelalloc) != 0
			out = append(out, physicalExtent{
				physical: int64(x.physical),
				length:   int64(x.length),
				shared:   !unplaced && x.flags&fiemapExtentShared != 0,
			})
			start = x.logical + x.length
			last = last || x.flags&fiemapExtentLast != 0
		}
		if last {
			return out, nil
		}
	}
}

// cloneInto makes dst a copy-on-write clone of src. It reports false, leaving
// dst untouched, when the filesystem or the pair of files cannot be cloned.
func cloneInto(dst, src *os.File) bool {
	return unix.IoctlFileClone(int(dst.Fd()), int(src.Fd())) == nil
}

// dedupeChunk is the first range size tried; a range whose bytes differ is
// split into dedupeSplit pieces down to one filesystem block.
const (
	dedupeChunk = 1 << 20
	dedupeSplit = 16
	dedupeBlock = 4096
)

// dedupeFile shares every block of dst whose bytes equal src's block at the
// same offset. It returns the bytes now shared.
func dedupeFile(ctx context.Context, dstPath, srcPath string) (int64, error) {
	src, err := os.Open(srcPath) //nolint:forbidigo // cache-owned entry path.
	if err != nil {
		return 0, err
	}
	defer func() { _ = src.Close() }()
	dst, err := os.OpenFile(dstPath, os.O_RDWR, 0) //nolint:forbidigo // cache-owned entry path; FIDEDUPERANGE never changes its bytes.
	if err != nil {
		return 0, err
	}
	defer func() { _ = dst.Close() }()
	srcInfo, err := src.Stat()
	if err != nil {
		return 0, err
	}
	dstInfo, err := dst.Stat()
	if err != nil {
		return 0, err
	}
	limit := min(srcInfo.Size(), dstInfo.Size())
	d := deduper{ctx: ctx, src: int(src.Fd()), dst: int64(dst.Fd())}
	for off := int64(0); off < limit; {
		data, err := unix.Seek(int(dst.Fd()), off, unix.SEEK_DATA)
		if err != nil {
			if errors.Is(err, unix.ENXIO) {
				break // no data after off
			}
			return d.shared, err
		}
		if data >= limit {
			break
		}
		hole, err := unix.Seek(int(dst.Fd()), data, unix.SEEK_HOLE)
		if err != nil {
			return d.shared, err
		}
		end := min(hole, limit)
		for start := data; start < end; start += dedupeChunk {
			if err := d.share(start, min(dedupeChunk, end-start)); err != nil {
				return d.shared, err
			}
		}
		off = end
	}
	return d.shared, nil
}

type deduper struct {
	ctx    context.Context
	src    int
	dst    int64
	shared int64
}

func (d *deduper) share(off, length int64) error {
	if err := d.ctx.Err(); err != nil {
		return err
	}
	req := unix.FileDedupeRange{
		Src_offset: uint64(off),
		Src_length: uint64(length),
		Info:       []unix.FileDedupeRangeInfo{{Dest_fd: d.dst, Dest_offset: uint64(off)}},
	}
	err := unix.IoctlFileDedupeRange(d.src, &req)
	switch {
	case errors.Is(err, unix.EINVAL):
		// An unaligned range (the file's partial last block): retry the
		// aligned part in smaller pieces.
		return d.split(off, length)
	case err != nil:
		// The filesystem or this pair of files cannot share blocks; no
		// smaller range would succeed either.
		return errDedupeUnsupported
	}
	info := req.Info[0]
	switch info.Status {
	case unix.FILE_DEDUPE_RANGE_SAME:
		d.shared += int64(info.Bytes_deduped)
	case unix.FILE_DEDUPE_RANGE_DIFFERS:
		return d.split(off, length)
	}
	return nil
}

// split retries a range that could not be shared whole as dedupeSplit
// pieces, down to single blocks, so the blocks the guest changed stay the
// drive's own and every other block is shared.
func (d *deduper) split(off, length int64) error {
	if length <= dedupeBlock {
		return nil
	}
	step := max(length/dedupeSplit, dedupeBlock)
	for start := off; start < off+length; start += step {
		if err := d.share(start, min(step, off+length-start)); err != nil {
			return err
		}
	}
	return nil
}
