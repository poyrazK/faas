package storage

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/klauspost/compress/zstd"
)

// Extent encoding for remote rootfs and snapshot artifacts (ADR-1135).
//
// An app drive is padded to its plan cap, so a 2 GiB ext4 image may hold
// 90 MiB of files. The plain zstd encoding compresses the logical bytes, which
// keeps the object small but makes a node-cache miss decode and scan every
// zero byte: 4–11 s per miss on the GCE compute nodes. The extent encoding
// stores only the data extents. The object body is one zstd frame whose
// content is a header followed by each extent's bytes in order:
//
//	magic "GRGEXT1\n"
//	uvarint logical size
//	uvarint extent count
//	count × (uvarint gap from the previous extent's end, uvarint length)
//
// The node cache writes each extent at its offset and sizes the file to the
// logical length, so a fill does work proportional to the data. Any other
// reader sees the logical byte stream, with zeros produced for the holes.

const (
	// encodingZstdExtents is the GCS encoding metadata value for this format.
	encodingZstdExtents = "zstd-extents"
	extentMagic         = "GRGEXT1\n"
	// maxEncodedExtents bounds the header a reader will allocate. An ext4
	// image of a few GiB has far fewer data extents.
	maxEncodedExtents = 1 << 20
)

var errCorruptExtentStream = errors.New("storage: corrupt extent stream")

type dataExtent struct{ off, length int64 }

// appendExtentHeader encodes the header for a file of size bytes with the
// given ascending, non-overlapping data extents.
func appendExtentHeader(b []byte, size int64, extents []dataExtent) []byte {
	b = append(b, extentMagic...)
	b = binary.AppendUvarint(b, uint64(size))
	b = binary.AppendUvarint(b, uint64(len(extents)))
	var end int64
	for _, e := range extents {
		b = binary.AppendUvarint(b, uint64(e.off-end))
		b = binary.AppendUvarint(b, uint64(e.length))
		end = e.off + e.length
	}
	return b
}

// encodeExtentStream writes the header and then each extent of f to w.
func encodeExtentStream(ctx context.Context, w io.Writer, f *os.File, size int64, extents []dataExtent) error {
	if _, err := w.Write(appendExtentHeader(nil, size, extents)); err != nil {
		return err
	}
	for _, e := range extents {
		n, err := copyContext(ctx, w, io.NewSectionReader(f, e.off, e.length))
		if err != nil {
			return err
		}
		if n != e.length {
			return fmt.Errorf("extent at %d: read %d of %d bytes", e.off, n, e.length)
		}
	}
	return nil
}

// readExtentHeader parses and validates the header from the decoded stream.
func readExtentHeader(r *bufio.Reader) (int64, []dataExtent, error) {
	magic := make([]byte, len(extentMagic))
	if _, err := io.ReadFull(r, magic); err != nil || string(magic) != extentMagic {
		return 0, nil, fmt.Errorf("%w: bad magic", errCorruptExtentStream)
	}
	size, err := binary.ReadUvarint(r)
	if err != nil || size > 1<<62 {
		return 0, nil, fmt.Errorf("%w: bad logical size", errCorruptExtentStream)
	}
	count, err := binary.ReadUvarint(r)
	if err != nil || count > maxEncodedExtents {
		return 0, nil, fmt.Errorf("%w: bad extent count", errCorruptExtentStream)
	}
	extents := make([]dataExtent, 0, count)
	var end uint64
	for i := uint64(0); i < count; i++ {
		gap, gapErr := binary.ReadUvarint(r)
		length, lenErr := binary.ReadUvarint(r)
		if gapErr != nil || lenErr != nil || length == 0 || gap > size || length > size ||
			end+gap > size || end+gap+length > size {
			return 0, nil, fmt.Errorf("%w: extent %d out of range", errCorruptExtentStream, i)
		}
		off := end + gap
		extents = append(extents, dataExtent{off: int64(off), length: int64(length)})
		end = off + length
	}
	return int64(size), extents, nil
}

// extentReadCloser decodes an extent-encoded object. Read yields the logical
// bytes; WriteSparseTo writes the extents straight into a file.
type extentReadCloser struct {
	decoded *bufio.Reader
	decoder *zstd.Decoder
	source  io.Closer
	logical int64
	extents []dataExtent
	pos     int64 // logical offset of the next Read
	next    int   // first extent not fully consumed
	ended   bool
}

func newExtentReadCloser(body io.ReadCloser, expectedSize int64) (*extentReadCloser, error) {
	decoder, err := zstd.NewReader(body, zstd.WithDecoderConcurrency(1))
	if err != nil {
		_ = body.Close()
		return nil, fmt.Errorf("start zstd decoder: %w", err)
	}
	decoded := bufio.NewReaderSize(decoder, 256*1024)
	logical, extents, err := readExtentHeader(decoded)
	if err == nil && logical != expectedSize {
		err = fmt.Errorf("%w: logical size %d, metadata says %d", errCorruptExtentStream, logical, expectedSize)
	}
	if err != nil {
		decoder.Close()
		_ = body.Close()
		return nil, err
	}
	return &extentReadCloser{decoded: decoded, decoder: decoder, source: body, logical: logical, extents: extents}, nil
}

func (e *extentReadCloser) Read(p []byte) (int, error) {
	if e.pos >= e.logical {
		return 0, e.finish()
	}
	if len(p) == 0 {
		return 0, nil
	}
	// The next stop is the start or end of the current extent, or EOF.
	if e.next < len(e.extents) {
		x := e.extents[e.next]
		if e.pos < x.off {
			n := int(min(int64(len(p)), x.off-e.pos))
			clear(p[:n])
			e.pos += int64(n)
			return n, nil
		}
		n := int(min(int64(len(p)), x.off+x.length-e.pos))
		if _, err := io.ReadFull(e.decoded, p[:n]); err != nil {
			return 0, fmt.Errorf("%w: extent at %d: %w", errCorruptExtentStream, x.off, err)
		}
		e.pos += int64(n)
		if e.pos == x.off+x.length {
			e.next++
		}
		return n, nil
	}
	n := int(min(int64(len(p)), e.logical-e.pos))
	clear(p[:n])
	e.pos += int64(n)
	return n, nil
}

// WriteSparseTo writes the whole object into the empty file dst: each extent
// at its offset, then the logical size, leaving holes elsewhere. It returns
// the logical size.
func (e *extentReadCloser) WriteSparseTo(ctx context.Context, dst *os.File) (int64, error) {
	if e.pos != 0 {
		return 0, errors.New("storage: extent stream already partly read")
	}
	for _, x := range e.extents {
		n, err := copyContext(ctx, io.NewOffsetWriter(dst, x.off), io.LimitReader(e.decoded, x.length))
		if err != nil {
			return 0, err
		}
		if n != x.length {
			return 0, fmt.Errorf("%w: extent at %d: %d of %d bytes", errCorruptExtentStream, x.off, n, x.length)
		}
	}
	e.pos, e.next = e.logical, len(e.extents)
	if err := e.finish(); !errors.Is(err, io.EOF) {
		return 0, err
	}
	if err := dst.Truncate(e.logical); err != nil {
		return 0, err
	}
	return e.logical, nil
}

// finish confirms the decoded stream ends exactly after the last extent,
// which also makes the decoder verify the frame checksum.
func (e *extentReadCloser) finish() error {
	if e.ended {
		return io.EOF
	}
	e.ended = true
	if _, err := e.decoded.ReadByte(); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("%w: trailing data after the last extent", errCorruptExtentStream)
		}
		return fmt.Errorf("%w: %w", errCorruptExtentStream, err)
	}
	return io.EOF
}

func (e *extentReadCloser) Close() error {
	e.decoder.Close()
	return e.source.Close()
}

// sparseWriterTo is implemented by readers that can fill a file sparsely
// faster than by streaming logical bytes.
type sparseWriterTo interface {
	WriteSparseTo(ctx context.Context, dst *os.File) (int64, error)
}

// compressExtentArtifact writes f (size bytes) to a temporary file as one
// fast zstd frame in the extent encoding and returns its path.
func compressExtentArtifact(ctx context.Context, backend, pattern, key string, f *os.File, size int64) (string, error) {
	extents, err := fileDataExtents(f, size)
	if err != nil {
		return "", fmt.Errorf("storage: %s put %q: list data extents: %w", backend, key, err)
	}
	out, err := osCreateTemp("", pattern)
	if err != nil {
		return "", fmt.Errorf("storage: %s put %q: create zstd tmp: %w", backend, key, err)
	}
	path := out.Name()
	succeeded := false
	defer func() {
		if !succeeded {
			_ = out.Close()
			_ = removeTmp(path)
		}
	}()
	encoder, err := zstd.NewWriter(out, zstd.WithEncoderLevel(zstd.SpeedFastest), zstd.WithEncoderConcurrency(1))
	if err != nil {
		return "", fmt.Errorf("storage: %s put %q: create zstd encoder: %w", backend, key, err)
	}
	encodeErr := encodeExtentStream(ctx, encoder, f, size, extents)
	closeErr := encoder.Close()
	if encodeErr != nil {
		return "", fmt.Errorf("storage: %s put %q: encode extents: %w", backend, key, encodeErr)
	}
	if closeErr != nil {
		return "", fmt.Errorf("storage: %s put %q: finish zstd stream: %w", backend, key, closeErr)
	}
	if err := out.Sync(); err != nil {
		return "", fmt.Errorf("storage: %s put %q: fsync zstd tmp: %w", backend, key, err)
	}
	if err := out.Close(); err != nil {
		return "", fmt.Errorf("storage: %s put %q: close zstd tmp: %w", backend, key, err)
	}
	succeeded = true
	return path, nil
}

// extentSource returns the regular file behind r and its size when r is one,
// read from its start; only then can its holes be listed.
func extentSource(r io.Reader) (*os.File, int64, bool) {
	f, ok := r.(*os.File)
	if !ok {
		return nil, 0, false
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, 0, false
	}
	if off, err := f.Seek(0, io.SeekCurrent); err != nil || off != 0 {
		return nil, 0, false
	}
	return f, info.Size(), true
}
