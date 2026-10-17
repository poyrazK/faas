package ci

import (
	"context"
	"errors"
	"io"
	"strings"

	gcs "cloud.google.com/go/storage"
	"github.com/onebox-faas/faas/pkg/api"
	"google.golang.org/api/googleapi"
)

const physicalState = "gregale-ci-state"

// GCSObjects uses a dedicated private bucket. Permanent empty tombstones fence
// delayed create-only writers; bucket lifecycle must never remove them.
type GCSObjects struct {
	client *gcs.Client
	bucket *gcs.BucketHandle
}

func NewGCSObjects(ctx context.Context, bucket string) (*GCSObjects, error) {
	if bucket == "" || bucket != strings.TrimSpace(bucket) {
		return nil, ErrStorageUnavailable
	}
	client, err := gcs.NewClient(ctx, gcs.WithJSONReads(), gcs.WithDisabledClientMetrics())
	if err != nil {
		return nil, ErrStorageUnavailable
	}
	objects := &GCSObjects{client: client, bucket: client.Bucket(bucket)}
	if err := objects.checkBucket(ctx); err != nil {
		_ = client.Close()
		return nil, err
	}
	return objects, nil
}

func (g *GCSObjects) Close() error { return g.client.Close() }

func (g *GCSObjects) checkBucket(ctx context.Context) error {
	attrs, err := g.bucket.Attrs(ctx)
	if err != nil || attrs.VersioningEnabled || attrs.RetentionPolicy != nil || len(attrs.Lifecycle.Rules) != 0 ||
		attrs.PublicAccessPrevention != gcs.PublicAccessPreventionEnforced || !attrs.UniformBucketLevelAccess.Enabled ||
		attrs.SoftDeletePolicy != nil && attrs.SoftDeletePolicy.RetentionDuration > 0 {
		return ErrStorageUnavailable
	}
	return nil
}

func validPhysicalKey(key string) bool {
	parts := strings.Split(key, "/")
	return len(parts) == 3 && parts[0] == "ci-v1" && ValidID(parts[1]) && ValidID(parts[2])
}

func (g *GCSObjects) Publish(ctx context.Context, key string, object RuntimeObject, body io.Reader) error {
	if !validPhysicalKey(key) || object.Bytes < 0 || object.Bytes > api.CIArtifactMaxBytes || !artifactDigestPattern.MatchString(object.Digest) {
		return ErrStorageUnavailable
	}
	if err := g.checkBucket(ctx); err != nil {
		return err
	}
	handle := g.bucket.Object(key)
	writeCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	writer := handle.If(gcs.Conditions{DoesNotExist: true}).NewWriter(writeCtx)
	writer.ChunkSize = api.CIStorageWriterChunkBytes
	writer.ContentType = "application/octet-stream"
	writer.Metadata = map[string]string{physicalState: "ready", "gregale-ci-digest": object.Digest}
	count, err := io.Copy(writer, io.LimitReader(body, object.Bytes+1))
	if err != nil || count != object.Bytes {
		cancel()
		_ = writer.Close()
		return ErrStorageUnavailable
	}
	if err := writer.Close(); err == nil {
		return nil
	} else if !gcsConflict(err) {
		return ErrStorageUnavailable
	}
	attrs, err := handle.Attrs(ctx)
	if err != nil || attrs.Metadata[physicalState] != "ready" || attrs.Size != object.Bytes || attrs.Metadata["gregale-ci-digest"] != object.Digest {
		return ErrStorageUnavailable
	}
	return nil
}

func (g *GCSObjects) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	if !validPhysicalKey(key) {
		return nil, ErrStorageUnavailable
	}
	handle := g.bucket.Object(key)
	attrs, err := handle.Attrs(ctx)
	if errors.Is(err, gcs.ErrObjectNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, ErrStorageUnavailable
	}
	if attrs.Metadata[physicalState] != "ready" {
		return nil, ErrNotFound
	}
	body, err := handle.Generation(attrs.Generation).NewReader(ctx)
	if err != nil {
		return nil, ErrStorageUnavailable
	}
	return body, nil
}

func (g *GCSObjects) Retire(ctx context.Context, key string) error {
	if !validPhysicalKey(key) {
		return ErrStorageUnavailable
	}
	if err := g.checkBucket(ctx); err != nil {
		return err
	}
	object := g.bucket.Object(key)
	for range api.CIJobsTransferMaxAttempts {
		attrs, err := object.Attrs(ctx)
		condition := gcs.Conditions{DoesNotExist: true}
		if err == nil {
			if attrs.Metadata[physicalState] == "retired" && attrs.Size == 0 {
				return nil
			}
			condition = gcs.Conditions{GenerationMatch: attrs.Generation}
		} else if !errors.Is(err, gcs.ErrObjectNotExist) {
			return ErrStorageUnavailable
		}
		writer := object.If(condition).NewWriter(ctx)
		writer.ChunkSize = api.CIStorageWriterChunkBytes
		writer.ContentType = "application/octet-stream"
		writer.Metadata = map[string]string{physicalState: "retired"}
		if err := writer.Close(); err == nil {
			return nil
		} else if !gcsConflict(err) {
			return ErrStorageUnavailable
		}
	}
	return ErrStorageUnavailable
}

func gcsConflict(err error) bool {
	var response *googleapi.Error
	return errors.As(err, &response) && (response.Code == 409 || response.Code == 412)
}
