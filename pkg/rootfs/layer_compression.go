package rootfs

import (
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"

	"github.com/klauspost/compress/zstd"

	"github.com/onebox-faas/faas/pkg/api"
)

// LayerCompression names the compression of one OCI layer blob.
type LayerCompression string

const (
	LayerGzip LayerCompression = "gzip"
	LayerZstd LayerCompression = "zstd"
)

// ErrUnsupportedLayerCompression rejects a layer blob that is neither gzip nor
// zstd. Uncompressed tar is refused on purpose: every producer compresses, and
// a plain stream here means the caller passed the wrong bytes.
var ErrUnsupportedLayerCompression = errors.New("rootfs: layer is neither gzip nor zstd")

var (
	gzipMagic = []byte{0x1f, 0x8b}
	zstdMagic = []byte{0x28, 0xb5, 0x2f, 0xfd}
)

// DetectLayerCompression reads a layer's compression from its first bytes.
// It returns "" for anything that is neither gzip nor zstd.
func DetectLayerCompression(head []byte) LayerCompression {
	switch {
	case bytes.HasPrefix(head, zstdMagic):
		return LayerZstd
	case bytes.HasPrefix(head, gzipMagic):
		return LayerGzip
	default:
		return ""
	}
}

// OpenCompressedLayer returns the uncompressed tar stream of one OCI layer
// blob compressed with gzip or zstd. Source builds may emit zstd layers
// (BuildKit's compression=zstd): they compress about six times faster than
// gzip on a builder vCPU and decompress about five times faster here, at a
// similar size. Closing the reader releases the decompressor; it does not
// close r.
func OpenCompressedLayer(r io.Reader) (io.ReadCloser, error) {
	head := make([]byte, len(zstdMagic))
	n, err := io.ReadFull(r, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("rootfs: read layer header: %w", err)
	}
	head = head[:n]
	src := io.MultiReader(bytes.NewReader(head), r)
	switch DetectLayerCompression(head) {
	case LayerZstd:
		dec, err := zstd.NewReader(src,
			zstd.WithDecoderConcurrency(1),
			zstd.WithDecoderMaxWindow(api.LocalOCIZstdMaxWindowBytes))
		if err != nil {
			return nil, fmt.Errorf("rootfs: zstd: %w", err)
		}
		return dec.IOReadCloser(), nil
	case LayerGzip:
		zr, err := gzip.NewReader(src)
		if err != nil {
			return nil, fmt.Errorf("rootfs: gzip: %w", err)
		}
		return zr, nil
	default:
		return nil, ErrUnsupportedLayerCompression
	}
}

// NewLayerWriter returns a zstd writer for host-local intermediate layers,
// such as the /app-only layers imaged filters out of a build. They are
// written once, read once by rootfs assembly, and never leave the node, so
// they take the fast codec. Close flushes the frame; it does not close w.
func NewLayerWriter(w io.Writer) (io.WriteCloser, error) {
	enc, err := zstd.NewWriter(w,
		zstd.WithEncoderLevel(zstd.SpeedDefault),
		zstd.WithEncoderConcurrency(1))
	if err != nil {
		return nil, fmt.Errorf("rootfs: zstd writer: %w", err)
	}
	return enc, nil
}
