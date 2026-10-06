// adr: 568 — acknowledged generations fence original GCS reads and retirement.
package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strconv"

	gcs "cloud.google.com/go/storage"
	"github.com/klauspost/compress/zstd"
	"github.com/onebox-faas/faas/pkg/api"
	"google.golang.org/api/googleapi"
)

type gcsExclusiveObjectReceipt struct{ Generation, Size int64 }

type gcsExclusiveReceiptStore interface {
	PutExclusiveReceipt(context.Context, string, string, io.Reader, map[string]string) (gcsExclusiveObjectReceipt, error)
	GetGeneration(context.Context, string, string, int64, int64) (io.ReadCloser, error)
	RetireGeneration(context.Context, string, string, int64) error
}

func (g *GCSStorageBackend) CheckExclusiveArtifact(ctx context.Context, key string) error {
	if err := errors.Join(validateKey(key), ctx.Err()); err != nil {
		return err
	}
	if _, ok := g.store.(gcsExclusiveReceiptStore); !ok {
		return ErrExclusiveReceiptUnsupported
	}
	return nil
}

func (g *GCSStorageBackend) PutExclusiveArtifact(ctx context.Context, key string, source io.Reader, size int64) (ExclusiveArtifactReceipt, error) {
	if err := g.CheckExclusiveArtifact(ctx, key); err != nil {
		return ExclusiveArtifactReceipt{}, err
	}
	if source == nil || size <= 0 {
		return ExclusiveArtifactReceipt{}, ErrArtifactReceiptMismatch
	}
	digest := sha256.New()
	body := &exactArtifactReader{source: io.TeeReader(source, digest), remaining: size}
	metadata := map[string]string{}
	r := ExclusiveArtifactReceipt{Version: 1, Key: key, ObjectKey: key, Backend: "gcs", Location: g.bucket, LogicalBytes: size}
	compressed := (g.snapshotCompression == snapshotCompressionZstd && isSnapshotMemoryKey(key)) || isSnapshotDriveKey(key) || isRootfsFilesystemKey(key)
	var original gcsExclusiveObjectReceipt
	var consumed int64
	consume := func(stream io.Reader) error {
		counter := &exclusiveCountingReader{Reader: stream}
		var err error
		original, err = g.store.(gcsExclusiveReceiptStore).PutExclusiveReceipt(ctx, g.bucket, key, counter, metadata)
		consumed = counter.read
		return err
	}
	var err error
	if compressed {
		r.Encoding = snapshotCompressionZstd
		metadata[gcsEncodingMetadata], metadata[gcsUncompressedSizeMetadata] = r.Encoding, strconv.FormatInt(size, 10)
		err = streamExclusiveZstd(ctx, body, consume)
	} else {
		err = consume(body)
	}
	if err == nil && (!body.complete || original.Generation <= 0 || original.Size != consumed || consumed <= 0) {
		err = ErrArtifactReceiptMismatch
	}
	if err := g.exclusivePutResult(key, errors.Join(err, ctx.Err())); err != nil {
		return ExclusiveArtifactReceipt{}, err
	}
	r.Generation, r.StoredBytes, r.SHA256 = original.Generation, original.Size, hex.EncodeToString(digest.Sum(nil))
	return r, r.Validate()
}

type exclusiveCountingReader struct {
	io.Reader
	read int64
}

func (r *exclusiveCountingReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	r.read += int64(n)
	return n, err
}

func (g *GCSStorageBackend) checkArtifactReceipt(ctx context.Context, r ExclusiveArtifactReceipt) error {
	if err := errors.Join(r.Validate(), ctx.Err()); err != nil {
		return err
	}
	if r.Backend != "gcs" || r.Location != g.bucket || r.Key != r.ObjectKey {
		return ErrArtifactReceiptMismatch
	}
	if _, ok := g.store.(gcsExclusiveReceiptStore); !ok {
		return ErrExclusiveReceiptUnsupported
	}
	return nil
}

func (g *GCSStorageBackend) GetExclusiveArtifact(ctx context.Context, r ExclusiveArtifactReceipt) (io.ReadCloser, error) {
	if err := g.checkArtifactReceipt(ctx, r); err != nil {
		return nil, err
	}
	body, err := g.store.(gcsExclusiveReceiptStore).GetGeneration(ctx, g.bucket, r.ObjectKey, r.Generation, r.StoredBytes)
	if err != nil {
		return nil, normalizeGCSError(err)
	}
	if r.Encoding == "" {
		return body, nil
	}
	decoder, err := zstd.NewReader(body, zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxMemory(api.ExclusiveArtifactDecoderMaxMemoryBytes))
	if err != nil {
		return nil, errors.Join(err, body.Close())
	}
	return &gcsReceiptDecoder{Decoder: decoder, source: body}, nil
}

type gcsReceiptDecoder struct {
	*zstd.Decoder
	source io.ReadCloser
}

func (r *gcsReceiptDecoder) Close() error { r.Decoder.Close(); return r.source.Close() }

func (g *GCSStorageBackend) RetireExclusiveArtifact(ctx context.Context, r ExclusiveArtifactReceipt) error {
	if err := g.checkArtifactReceipt(ctx, r); err != nil {
		return err
	}
	err := g.store.(gcsExclusiveReceiptStore).RetireGeneration(ctx, g.bucket, r.ObjectKey, r.Generation)
	// A missing acknowledged generation is idempotent retirement evidence.
	// A replacement's existence does not grant permission to delete it.
	if errors.Is(err, gcs.ErrObjectNotExist) {
		return ctx.Err()
	}
	var apiError *googleapi.Error
	if errors.As(err, &apiError) && apiError.Code == 412 {
		return errors.Join(ErrArtifactReceiptMismatch, err)
	}
	return errors.Join(normalizeGCSError(err), ctx.Err())
}

func (s *googleGCSArtifactStore) PutExclusiveReceipt(ctx context.Context, bucket, key string, body io.Reader, metadata map[string]string) (gcsExclusiveObjectReceipt, error) {
	writeCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	w := s.client.Bucket(bucket).Object(key).If(gcs.Conditions{DoesNotExist: true}).NewWriter(writeCtx)
	w.ContentType, w.Metadata, w.ChunkSize = gcsContentType, metadata, 256<<10
	if _, err := copyContext(ctx, w, body); err != nil {
		cancel()
		return gcsExclusiveObjectReceipt{}, errors.Join(err, w.Close())
	}
	if err := w.Close(); err != nil {
		return gcsExclusiveObjectReceipt{}, err
	}
	attrs := w.Attrs() // Original successful writer response, never a later key lookup.
	if attrs == nil || attrs.Bucket != bucket || attrs.Name != key || attrs.Generation <= 0 || attrs.Size <= 0 {
		return gcsExclusiveObjectReceipt{}, ErrArtifactReceiptMismatch
	}
	return gcsExclusiveObjectReceipt{Generation: attrs.Generation, Size: attrs.Size}, ctx.Err()
}

func (s *googleGCSArtifactStore) GetGeneration(ctx context.Context, bucket, key string, generation, size int64) (io.ReadCloser, error) {
	r, err := s.client.Bucket(bucket).Object(key).Generation(generation).NewReader(ctx)
	if err != nil {
		return nil, err
	}
	if r.Attrs.Generation != generation || r.Attrs.Size != size || r.Attrs.StartOffset != 0 || r.Attrs.Decompressed {
		return nil, errors.Join(ErrArtifactReceiptMismatch, r.Close())
	}
	return r, nil
}

func (s *googleGCSArtifactStore) RetireGeneration(ctx context.Context, bucket, key string, generation int64) error {
	return s.client.Bucket(bucket).Object(key).Generation(generation).If(gcs.Conditions{GenerationMatch: generation}).Delete(ctx)
}
