package storage

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/klauspost/compress/zstd"
)

// Object stores that carry custom per-object metadata (GCS and S3) share one
// artifact encoding: compressible keys are stored zstd-encoded, and the object
// metadata names the encoding and the logical size. The metadata names are
// identical on both stores so a bucket copied with its metadata (for example
// a GCS to S3 migration) reads back unchanged.
const (
	remoteEncodingMetadata         = "gregale-storage-encoding"
	remoteUncompressedSizeMetadata = "gregale-storage-uncompressed-size"
)

// remoteArtifactCompressed reports whether Put stores key zstd-encoded.
func remoteArtifactCompressed(snapshotCompression, key string) bool {
	return (snapshotCompression == snapshotCompressionZstd && isSnapshotMemoryKey(key)) || isSnapshotDriveKey(key) || isRootfsFilesystemKey(key)
}

// encodeRemoteArtifact prepares the body and metadata Put uploads for key.
// Uncompressed keys pass r through with empty metadata. Compressed keys are
// spooled to a private temporary file; the caller must call cleanup once the
// upload has finished, whatever its outcome. cleanup is never nil.
func encodeRemoteArtifact(
	ctx context.Context,
	backend, pattern, key string,
	r io.Reader,
	snapshotCompression string,
	sparseExtents bool,
) (body io.Reader, metadata map[string]string, cleanup func(), err error) {
	metadata = map[string]string{}
	if !remoteArtifactCompressed(snapshotCompression, key) {
		return r, metadata, func() {}, nil
	}
	encoding := snapshotCompressionZstd
	var path string
	var uncompressedSize int64
	f, size, isFile := extentSource(r)
	if sparseExtents && isFile {
		encoding, uncompressedSize = encodingZstdExtents, size
		path, err = compressExtentArtifact(ctx, backend, pattern, key, f, size)
	} else {
		path, _, uncompressedSize, err = compressRemoteArtifact(ctx, backend, pattern, key, r)
	}
	if err != nil {
		return nil, nil, func() {}, err
	}
	file, err := os.Open(path) //nolint:forbidigo // private spool created above.
	if err != nil {
		_ = removeTmp(path)
		return nil, nil, func() {}, fmt.Errorf("storage: %s put %q: open compressed spool: %w", backend, key, err)
	}
	metadata[remoteEncodingMetadata] = encoding
	metadata[remoteUncompressedSizeMetadata] = strconv.FormatInt(uncompressedSize, 10)
	return file, metadata, func() {
		_ = file.Close()
		_ = removeTmp(path)
	}, nil
}

// decodeRemoteArtifact wraps body according to the encoding recorded in
// metadata. On error body has been closed.
func decodeRemoteArtifact(backend, key string, body io.ReadCloser, metadata map[string]string) (io.ReadCloser, error) {
	encoding := metadata[remoteEncodingMetadata]
	switch encoding {
	case "", snapshotCompressionNone:
		return body, nil
	case snapshotCompressionZstd:
		rawSize := metadata[remoteUncompressedSizeMetadata]
		expectedSize, parseErr := strconv.ParseInt(rawSize, 10, 64)
		if parseErr != nil || expectedSize <= 0 {
			_ = body.Close()
			return nil, fmt.Errorf("storage: %s get %q: invalid zstd uncompressed size %q", backend, key, rawSize)
		}
		decoder, decodeErr := zstd.NewReader(body, zstd.WithDecoderConcurrency(1))
		if decodeErr != nil {
			_ = body.Close()
			return nil, fmt.Errorf("storage: %s get %q: start zstd decoder: %w", backend, key, decodeErr)
		}
		return &decodedBlobReadCloser{decoder: decoder, source: body, expectedSize: expectedSize}, nil
	case encodingZstdExtents:
		rawSize := metadata[remoteUncompressedSizeMetadata]
		expectedSize, parseErr := strconv.ParseInt(rawSize, 10, 64)
		if parseErr != nil || expectedSize < 0 {
			_ = body.Close()
			return nil, fmt.Errorf("storage: %s get %q: invalid extent logical size %q", backend, key, rawSize)
		}
		reader, err := newExtentReadCloser(body, expectedSize)
		if err != nil {
			return nil, fmt.Errorf("storage: %s get %q: %w", backend, key, err)
		}
		return reader, nil
	default:
		_ = body.Close()
		return nil, fmt.Errorf("storage: %s get %q: unsupported object encoding %q", backend, key, encoding)
	}
}
