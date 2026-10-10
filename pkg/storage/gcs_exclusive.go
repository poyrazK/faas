// adr: 568 — GCS capture uploads stream under a create-only generation fence.
package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"

	gcs "cloud.google.com/go/storage"
	"github.com/klauspost/compress/zstd"
	"google.golang.org/api/googleapi"
)

type gcsExclusiveArtifactStore interface {
	PutExclusive(context.Context, string, string, io.Reader, map[string]string) error
}

func (g *GCSStorageBackend) CheckExclusivePut(ctx context.Context, key string) error {
	if err := validateKey(key); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, ok := g.store.(gcsExclusiveArtifactStore); !ok {
		return ErrExclusivePutUnsupported
	}
	return nil
}

func (g *GCSStorageBackend) PutExclusive(ctx context.Context, key string, reader io.Reader, size int64) error {
	if err := g.CheckExclusivePut(ctx, key); err != nil {
		return err
	}
	if reader == nil || size <= 0 {
		return errors.New("storage: exclusive publication requires a nonempty original reader")
	}
	store := g.store.(gcsExclusiveArtifactStore)
	body := &exactArtifactReader{source: reader, remaining: size}
	metadata := map[string]string{}
	compressed := (g.snapshotCompression == snapshotCompressionZstd && isSnapshotMemoryKey(key)) || isSnapshotDriveKey(key) || isRootfsFilesystemKey(key)
	if !compressed {
		err := store.PutExclusive(ctx, g.bucket, key, body, metadata)
		if err == nil && !body.complete {
			err = errors.New("storage: exclusive consumer did not finish its original source")
		}
		return g.exclusivePutResult(key, errors.Join(err, ctx.Err()))
	}
	metadata[gcsEncodingMetadata] = snapshotCompressionZstd
	metadata[gcsUncompressedSizeMetadata] = strconv.FormatInt(size, 10)
	err := streamExclusiveZstd(ctx, body, func(stream io.Reader) error {
		return store.PutExclusive(ctx, g.bucket, key, stream, metadata)
	})
	return g.exclusivePutResult(key, errors.Join(err, ctx.Err()))
}

func (g *GCSStorageBackend) exclusivePutResult(key string, err error) error {
	if err == nil {
		return nil
	}
	var apiError *googleapi.Error
	if errors.As(err, &apiError) && apiError.Code == 412 {
		err = errors.Join(ErrArtifactExists, err)
	}
	return fmt.Errorf("storage: gcs exclusive put %q: %w", key, err)
}

// Join the single producer before returning to its pinned source owner. A
// failed or early-returning consumer closes the pipe and cancels the encoder;
// cancellation cannot leave a worker reading an already released descriptor.
func streamExclusiveZstd(ctx context.Context, source io.Reader, consume func(io.Reader) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	reader, writer := io.Pipe()
	stop := context.AfterFunc(ctx, func() { _ = reader.CloseWithError(ctx.Err()) })
	defer stop()
	done := make(chan error, 1)
	go func() {
		encoder, err := zstd.NewWriter(writer, zstd.WithEncoderLevel(zstd.SpeedFastest), zstd.WithEncoderConcurrency(1))
		if err == nil {
			_, err = copyContext(ctx, encoder, source)
			err = errors.Join(err, encoder.Close())
		}
		err = errors.Join(err, writer.CloseWithError(err))
		done <- err
	}()
	err := consume(reader)
	_ = reader.CloseWithError(io.ErrClosedPipe)
	cancel()
	producerErr := <-done
	return errors.Join(err, producerErr)
}

func (s *googleGCSArtifactStore) PutExclusive(ctx context.Context, bucket, key string, body io.Reader, metadata map[string]string) error {
	writeCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	w := s.client.Bucket(bucket).Object(key).If(gcs.Conditions{DoesNotExist: true}).NewWriter(writeCtx)
	w.ContentType, w.Metadata = gcsContentType, metadata
	w.ChunkSize = 256 << 10
	if _, err := copyContext(ctx, w, body); err != nil {
		cancel()
		return errors.Join(err, w.Close())
	}
	return w.Close()
}
