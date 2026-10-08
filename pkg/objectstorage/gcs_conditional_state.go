package objectstorage

// adr: 712

import (
	"context"
	"errors"
	"io"
	"math"
	"net/http"
	"strconv"

	"cloud.google.com/go/storage"
)

var _ ConditionalStateProvider = (*GCS)(nil)
var _ ConditionalStateProvider = (*googleGCSStore)(nil)

// GCS state versions are canonical content generations, never HTTP ETags or
// metagenerations. The engine treats these provider-specific tokens as opaque.
func (p *GCS) ReadStateObject(ctx context.Context, bucket, key string, maxBytes int64) ([]byte, string, error) {
	if bucket == "" || !ValidKey(key) || maxBytes <= 0 || maxBytes == math.MaxInt64 {
		return nil, "", ErrInvalid
	}
	store, ok := p.store.(ConditionalStateProvider)
	if !ok {
		return nil, "", ErrUnsupported
	}
	body, version, err := store.ReadStateObject(ctx, bucket, key, maxBytes)
	if err != nil {
		return nil, "", normalizeGCS(err)
	}
	return body, version, nil
}

func (p *GCS) WriteStateObject(ctx context.Context, bucket, key string, body []byte, expectedVersion string) (string, error) {
	if bucket == "" || !ValidKey(key) || expectedVersion != "" && !validGCSStateVersion(expectedVersion) {
		return "", ErrInvalid
	}
	store, ok := p.store.(ConditionalStateProvider)
	if !ok {
		return "", ErrUnsupported
	}
	version, err := store.WriteStateObject(ctx, bucket, key, body, expectedVersion)
	if gcsStatusCode(err) == http.StatusPreconditionFailed {
		return "", ErrPreconditionFailed
	}
	if err != nil {
		normalized := normalizeGCS(err)
		// A generic 409 is not proof of a rejected generation precondition.
		if errors.Is(normalized, ErrConflict) {
			normalized = ErrUnavailable
		}
		return "", normalized
	}
	return version, nil
}

func validGCSStateVersion(version string) bool {
	generation, err := strconv.ParseInt(version, 10, 64)
	return err == nil && generation > 0 && strconv.FormatInt(generation, 10) == version
}

func (s *googleGCSStore) ReadStateObject(ctx context.Context, bucket, key string, maxBytes int64) ([]byte, string, error) {
	object := s.client.Bucket(bucket).Object(key).ReadCompressed(true).Retryer(storage.WithPolicy(storage.RetryNever))
	reader, err := object.NewReader(ctx)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = reader.Close() }()
	attrs := reader.Attrs
	if attrs.Generation <= 0 || attrs.Size < 0 || attrs.Size > maxBytes || attrs.StartOffset != 0 || attrs.ContentEncoding != "" || attrs.Decompressed {
		return nil, "", ErrUnavailable
	}
	body, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	if err != nil || int64(len(body)) != attrs.Size || int64(len(body)) > maxBytes {
		return nil, "", ErrUnavailable
	}
	return body, strconv.FormatInt(attrs.Generation, 10), nil
}

func (s *googleGCSStore) WriteStateObject(ctx context.Context, bucket, key string, body []byte, expectedVersion string) (string, error) {
	conditions := storage.Conditions{DoesNotExist: true}
	if expectedVersion != "" {
		generation, err := strconv.ParseInt(expectedVersion, 10, 64)
		if err != nil || !validGCSStateVersion(expectedVersion) {
			return "", ErrInvalid
		}
		conditions = storage.Conditions{GenerationMatch: generation}
	}
	writeCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	writer := s.client.Bucket(bucket).Object(key).If(conditions).Retryer(storage.WithPolicy(storage.RetryNever)).NewWriter(writeCtx)
	// One non-resumable request: an uncertain dispatched write must be resolved
	// by the entity receipt protocol, never by a hidden SDK upload retry.
	writer.ChunkSize = 0
	writer.ContentType = "application/json"
	writer.CacheControl = "no-store"
	if _, err := writer.Write(body); err != nil {
		cancel()
		_ = writer.Close()
		return "", err
	}
	if err := writer.Close(); err != nil {
		return "", err
	}
	attrs := writer.Attrs()
	if attrs == nil || attrs.Generation <= 0 || attrs.Size != int64(len(body)) || strconv.FormatInt(attrs.Generation, 10) == expectedVersion {
		return "", ErrUnavailable
	}
	return strconv.FormatInt(attrs.Generation, 10), nil
}
