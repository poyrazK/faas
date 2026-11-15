package storage

// Snapshot drive deltas (ADR-20261115204045).
//
// A snapshot's private drive is a copy-on-write clone of its app layer in
// which the guest changed a few blocks. The node cache already keeps it that
// way on disk (ADR-633), but the parent used to receive the whole drive: the
// layer's files again, for every snapshot. With deltas enabled the cache
// uploads only the blocks that differ from the layer, under a header naming
// the layer and the digest of its data, and rebuilds the full drive from the
// layer when it fetches one. Every reader goes through the cache, so callers
// keep reading a whole drive.
//
// Payload (before the parent's own compression):
//
//	magic "GRGDLT1\n"
//	uvarint version (1)
//	uvarint len(base key), base key
//	uvarint base logical size
//	32 bytes base data digest (driveDataDigest)
//	uvarint drive logical size
//	records: uvarint gap in blocks from the previous record's end,
//	         uvarint blocks (0 ends the list), byte kind,
//	         blocks × 4096 bytes when kind is data
//
// A kind of zero means the drive reads zeros where the layer held data (the
// guest discarded them); the decoder writes zeros there.

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
)

const (
	driveDeltaMagic   = "GRGDLT1\n"
	driveDeltaVersion = 1
	driveDeltaBlock   = 4096
	// driveDeltaMaxKey bounds the base key a decoder will read.
	driveDeltaMaxKey = 1024

	driveDeltaData byte = 0
	driveDeltaZero byte = 1
)

// DriveDeltaSource is a snapshot drive handed to Put with the key of the app
// layer it was cloned from. A cache with deltas enabled uploads only the
// blocks that differ from that layer; any other backend reads it as the
// plain file.
type DriveDeltaSource struct {
	*os.File
	BaseKey string
}

// WithDriveDeltaBase pairs a snapshot drive file with its layer's key.
func WithDriveDeltaBase(f *os.File, baseKey string) io.Reader {
	if baseKey == "" {
		return f
	}
	return DriveDeltaSource{File: f, BaseKey: baseKey}
}

// errNotDriveDelta reports a payload without the delta header.
var errNotDriveDelta = errors.New("storage: not a drive delta")

type driveDeltaHeader struct {
	baseKey    string
	baseSize   int64
	baseDigest [32]byte
	size       int64
}

// DriveDeltaStats describes one encoded delta.
type DriveDeltaStats struct {
	DataBlocks, ZeroBlocks int64
}

// encodeDriveDelta writes the delta of drive against base to w.
func encodeDriveDelta(ctx context.Context, w io.Writer, drive, base *os.File, baseKey string) (DriveDeltaStats, error) {
	var stats DriveDeltaStats
	driveInfo, err := drive.Stat()
	if err != nil {
		return stats, err
	}
	baseInfo, err := base.Stat()
	if err != nil {
		return stats, err
	}
	if len(baseKey) > driveDeltaMaxKey {
		return stats, fmt.Errorf("storage: drive delta: base key is %d bytes", len(baseKey))
	}
	digest, err := driveDataDigest(ctx, base, baseInfo.Size())
	if err != nil {
		return stats, fmt.Errorf("storage: drive delta: digest base: %w", err)
	}
	bw := bufio.NewWriterSize(w, 1<<20)
	hdr := []byte(driveDeltaMagic)
	hdr = binary.AppendUvarint(hdr, driveDeltaVersion)
	hdr = binary.AppendUvarint(hdr, uint64(len(baseKey)))
	hdr = append(hdr, baseKey...)
	hdr = binary.AppendUvarint(hdr, uint64(baseInfo.Size()))
	hdr = append(hdr, digest[:]...)
	hdr = binary.AppendUvarint(hdr, uint64(driveInfo.Size()))
	if _, err := bw.Write(hdr); err != nil {
		return stats, err
	}

	ranges, err := unionDataRanges(drive, driveInfo.Size(), base, baseInfo.Size())
	if err != nil {
		return stats, err
	}
	// The decoder truncates to the drive's size, so the layer's data past
	// it needs no record.
	ranges = clipRanges(ranges, (driveInfo.Size()+driveDeltaBlock-1)/driveDeltaBlock*driveDeltaBlock)
	var (
		next     int64 // block after the previous record
		runStart int64 = -1
		runLen   int64
		runKind  byte
		runData  []byte
	)
	flush := func() error {
		if runStart < 0 {
			return nil
		}
		var rec []byte
		rec = binary.AppendUvarint(rec, uint64(runStart-next))
		rec = binary.AppendUvarint(rec, uint64(runLen))
		rec = append(rec, runKind)
		if _, err := bw.Write(rec); err != nil {
			return err
		}
		if runKind == driveDeltaData {
			if _, err := bw.Write(runData); err != nil {
				return err
			}
			stats.DataBlocks += runLen
		} else {
			stats.ZeroBlocks += runLen
		}
		next = runStart + runLen
		runStart, runLen, runData = -1, 0, runData[:0]
		return nil
	}
	db := make([]byte, 1<<20)
	bb := make([]byte, 1<<20)
	page := make([]byte, driveDeltaBlock)
	for _, r := range ranges {
		for off := r.off; off < r.off+r.length; off += int64(len(db)) {
			if err := ctx.Err(); err != nil {
				return stats, err
			}
			n := int(min(int64(len(db)), r.off+r.length-off))
			if err := readAtZeroFill(drive, db[:n], off, driveInfo.Size()); err != nil {
				return stats, err
			}
			if err := readAtZeroFill(base, bb[:n], off, baseInfo.Size()); err != nil {
				return stats, err
			}
			for i := 0; i < n; i += driveDeltaBlock {
				end := min(i+driveDeltaBlock, n)
				block := off/driveDeltaBlock + int64(i/driveDeltaBlock)
				if bytes.Equal(db[i:end], bb[i:end]) {
					if err := flush(); err != nil {
						return stats, err
					}
					continue
				}
				kind := driveDeltaData
				if isZeroBytes(db[i:end]) {
					kind = driveDeltaZero
				}
				if runStart < 0 || runKind != kind || block != runStart+runLen {
					if err := flush(); err != nil {
						return stats, err
					}
					runStart, runKind = block, kind
				}
				runLen++
				if kind == driveDeltaData {
					clear(page)
					copy(page, db[i:end])
					runData = append(runData, page...)
				}
				// Bound the buffered run; the next block starts a new record.
				if len(runData) >= 4<<20 {
					if err := flush(); err != nil {
						return stats, err
					}
				}
			}
		}
	}
	if err := flush(); err != nil {
		return stats, err
	}
	end := binary.AppendUvarint(nil, 0)
	end = binary.AppendUvarint(end, 0)
	if _, err := bw.Write(end); err != nil {
		return stats, err
	}
	return stats, bw.Flush()
}

// readDriveDeltaHeader reads the header after the caller has seen the magic.
func readDriveDeltaHeader(r *bufio.Reader) (driveDeltaHeader, error) {
	var h driveDeltaHeader
	magic := make([]byte, len(driveDeltaMagic))
	if _, err := io.ReadFull(r, magic); err != nil || string(magic) != driveDeltaMagic {
		return h, errNotDriveDelta
	}
	version, err := binary.ReadUvarint(r)
	if err != nil {
		return h, fmt.Errorf("storage: drive delta header: %w", err)
	}
	if version != driveDeltaVersion {
		return h, fmt.Errorf("storage: drive delta version %d unsupported", version)
	}
	keyLen, err := binary.ReadUvarint(r)
	if err != nil {
		return h, fmt.Errorf("storage: drive delta header: base key length: %w", err)
	}
	if keyLen == 0 || keyLen > driveDeltaMaxKey {
		return h, fmt.Errorf("storage: drive delta header: base key length %d out of range", keyLen)
	}
	key := make([]byte, keyLen)
	if _, err := io.ReadFull(r, key); err != nil {
		return h, fmt.Errorf("storage: drive delta header: %w", err)
	}
	h.baseKey = string(key)
	if err := validateKey(h.baseKey); err != nil {
		return h, fmt.Errorf("storage: drive delta header: base key: %w", err)
	}
	baseSize, err := binary.ReadUvarint(r)
	if err != nil {
		return h, fmt.Errorf("storage: drive delta header: base size: %w", err)
	}
	if baseSize > 1<<50 {
		return h, fmt.Errorf("storage: drive delta header: base size %d out of range", baseSize)
	}
	h.baseSize = int64(baseSize)
	if _, err := io.ReadFull(r, h.baseDigest[:]); err != nil {
		return h, fmt.Errorf("storage: drive delta header: %w", err)
	}
	size, err := binary.ReadUvarint(r)
	if err != nil {
		return h, fmt.Errorf("storage: drive delta header: size: %w", err)
	}
	if size > 1<<50 {
		return h, fmt.Errorf("storage: drive delta header: size %d out of range", size)
	}
	h.size = int64(size)
	return h, nil
}

// applyDriveDelta writes the records in r over dst, which already holds the
// base, and sizes dst to h.size.
func applyDriveDelta(ctx context.Context, r *bufio.Reader, h driveDeltaHeader, dst *os.File) error {
	if err := dst.Truncate(h.size); err != nil {
		return fmt.Errorf("storage: drive delta: size drive: %w", err)
	}
	maxBlocks := (h.size + driveDeltaBlock - 1) / driveDeltaBlock
	var next int64
	buf := make([]byte, 1<<20)
	zeros := make([]byte, 1<<20)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		gap, err := binary.ReadUvarint(r)
		if err != nil {
			return fmt.Errorf("storage: drive delta record: %w", err)
		}
		n, err := binary.ReadUvarint(r)
		if err != nil {
			return fmt.Errorf("storage: drive delta record: %w", err)
		}
		if n == 0 {
			if gap != 0 {
				return errors.New("storage: drive delta: malformed end record")
			}
			return nil
		}
		kind, err := r.ReadByte()
		if err != nil {
			return fmt.Errorf("storage: drive delta record: %w", err)
		}
		start := next + int64(gap)
		if gap > uint64(maxBlocks) || n > uint64(maxBlocks) || start+int64(n) > maxBlocks {
			return fmt.Errorf("storage: drive delta: record at block %d (+%d) past the drive's %d blocks", start, n, maxBlocks)
		}
		off, length := start*driveDeltaBlock, int64(n)*driveDeltaBlock
		for done := int64(0); done < length; {
			chunk := min(int64(len(buf)), length-done)
			src := zeros[:chunk]
			switch kind {
			case driveDeltaData:
				if _, err := io.ReadFull(r, buf[:chunk]); err != nil {
					return fmt.Errorf("storage: drive delta data: %w", err)
				}
				src = buf[:chunk]
			case driveDeltaZero:
			default:
				return fmt.Errorf("storage: drive delta: record kind %d", kind)
			}
			// The last block of a drive whose size is not a block multiple
			// stops at the drive's end.
			w := min(chunk, h.size-(off+done))
			if w > 0 {
				if _, err := dst.WriteAt(src[:w], off+done); err != nil {
					return fmt.Errorf("storage: drive delta: write: %w", err)
				}
			}
			done += chunk
		}
		next = start + int64(n)
	}
}

// driveDataDigest hashes f's non-zero 4 KiB blocks with their offsets. Holes
// and zero blocks do not contribute, so two files with equal bytes have equal
// digests however they are laid out on disk; the cost follows the data.
func driveDataDigest(ctx context.Context, f *os.File, size int64) ([32]byte, error) {
	var sum [32]byte
	extents, err := fileDataExtents(f, size)
	if err != nil {
		return sum, err
	}
	h := sha256.New()
	buf := make([]byte, 1<<20)
	var off [8]byte
	for _, e := range extents {
		start := e.off / driveDeltaBlock * driveDeltaBlock
		for pos := start; pos < e.off+e.length; pos += int64(len(buf)) {
			if err := ctx.Err(); err != nil {
				return sum, err
			}
			n := int(min(int64(len(buf)), e.off+e.length-pos))
			if err := readAtZeroFill(f, buf[:n], pos, size); err != nil {
				return sum, err
			}
			for i := 0; i < n; i += driveDeltaBlock {
				block := buf[i:min(i+driveDeltaBlock, n)]
				if isZeroBytes(block) {
					continue
				}
				binary.LittleEndian.PutUint64(off[:], uint64(pos)+uint64(i))
				h.Write(off[:])
				h.Write(block)
			}
		}
	}
	copy(sum[:], h.Sum(nil))
	return sum, nil
}

// unionDataRanges returns the block-aligned ranges where either file holds
// data, merged and sorted.
func unionDataRanges(a *os.File, aSize int64, b *os.File, bSize int64) ([]dataExtent, error) {
	ea, err := fileDataExtents(a, aSize)
	if err != nil {
		return nil, err
	}
	eb, err := fileDataExtents(b, bSize)
	if err != nil {
		return nil, err
	}
	all := append(append([]dataExtent{}, ea...), eb...)
	for i := range all {
		start := all[i].off / driveDeltaBlock * driveDeltaBlock
		end := (all[i].off + all[i].length + driveDeltaBlock - 1) / driveDeltaBlock * driveDeltaBlock
		all[i] = dataExtent{off: start, length: end - start}
	}
	// Sort by offset (few extents; insertion sort keeps this dependency-free).
	for i := 1; i < len(all); i++ {
		for j := i; j > 0 && all[j].off < all[j-1].off; j-- {
			all[j], all[j-1] = all[j-1], all[j]
		}
	}
	var out []dataExtent
	for _, e := range all {
		if k := len(out) - 1; k >= 0 && e.off <= out[k].off+out[k].length {
			out[k].length = max(out[k].length, e.off+e.length-out[k].off)
			continue
		}
		out = append(out, e)
	}
	return out, nil
}

// clipRanges drops the parts of ranges at or past end.
func clipRanges(ranges []dataExtent, end int64) []dataExtent {
	out := ranges[:0]
	for _, r := range ranges {
		if r.off >= end {
			break
		}
		r.length = min(r.length, end-r.off)
		out = append(out, r)
	}
	return out
}

// readAtZeroFill reads len(p) bytes at off, treating bytes past size as zero.
func readAtZeroFill(f *os.File, p []byte, off, size int64) error {
	clear(p)
	if off >= size {
		return nil
	}
	n := min(int64(len(p)), size-off)
	if _, err := f.ReadAt(p[:n], off); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func isZeroBytes(b []byte) bool {
	for _, c := range b {
		if c != 0 {
			return false
		}
	}
	return true
}
