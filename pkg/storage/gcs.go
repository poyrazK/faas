package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"

	gcs "cloud.google.com/go/storage"
	"google.golang.org/api/iterator"
)

const (
	gcsEncodingMetadata         = remoteEncodingMetadata
	gcsUncompressedSizeMetadata = remoteUncompressedSizeMetadata
	gcsContentType              = "application/octet-stream"
)

var gcsBucketName = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{1,220}[a-z0-9]$`)

// GCSStorageBackend stores Gregale's logical artifact keys as native Cloud
// Storage object names. It deliberately uses Application Default Credentials:
// production VMs authenticate with their attached service account, so no
// long-lived service-account key is copied into storage.env.
//
// Compressed objects carry their encoding and logical size in GCS custom
// metadata. Readers pin the object generation observed with that metadata,
// preventing a last-writer-wins replacement from pairing old metadata with a
// new object body.
type GCSStorageBackend struct {
	bucket              string
	store               gcsArtifactStore
	snapshotCompression string
	// sparseExtents writes compressed artifacts handed over as files in the
	// extent encoding (ADR-1135). Readers accept it either way.
	sparseExtents bool
}

type gcsArtifact struct {
	body       io.ReadCloser
	metadata   map[string]string
	generation int64
}

type gcsArtifactStore interface {
	Put(context.Context, string, string, io.Reader, map[string]string) error
	Get(context.Context, string, string) (gcsArtifact, error)
	Delete(context.Context, string, string) error
	List(context.Context, string, string) ([]string, error)
}

type googleGCSArtifactStore struct {
	client *gcs.Client
}

var (
	_ StorageBackend      = (*GCSStorageBackend)(nil)
	_ LocalArtifactLister = (*GCSStorageBackend)(nil)
)

// NewGCSStorageBackend constructs the production GCS driver with Application
// Default Credentials. bucket must already exist; bucket lifecycle and IAM are
// infrastructure concerns and are intentionally not mutated by daemon startup.
func NewGCSStorageBackend(ctx context.Context, bucket, snapshotCompression string) (*GCSStorageBackend, error) {
	if err := validateGCSBucket(bucket); err != nil {
		return nil, err
	}
	client, err := gcs.NewClient(ctx, gcs.WithJSONReads(), gcs.WithDisabledClientMetrics())
	if err != nil {
		return nil, fmt.Errorf("storage: gcs client: %w", err)
	}
	backend, err := newGCSStorageBackend(bucket, snapshotCompression, &googleGCSArtifactStore{client: client})
	if err != nil {
		_ = client.Close()
		return nil, err
	}
	return backend, nil
}

func newGCSStorageBackend(bucket, snapshotCompression string, store gcsArtifactStore) (*GCSStorageBackend, error) {
	if err := validateGCSBucket(bucket); err != nil {
		return nil, err
	}
	if store == nil {
		return nil, errors.New("storage: gcs store is nil")
	}
	compression := strings.ToLower(strings.TrimSpace(snapshotCompression))
	switch compression {
	case "", snapshotCompressionNone:
		compression = snapshotCompressionNone
	case snapshotCompressionZstd:
	default:
		return nil, fmt.Errorf("%w: unsupported snapshot compression %q", ErrInvalidKey, compression)
	}
	return &GCSStorageBackend{bucket: bucket, store: store, snapshotCompression: compression}, nil
}

func validateGCSBucket(bucket string) error {
	if bucket != strings.TrimSpace(bucket) || !gcsBucketName.MatchString(bucket) {
		return fmt.Errorf("storage: invalid FAAS_GCS_BUCKET=%q", bucket)
	}
	return nil
}

// Bucket returns the configured bucket name for startup diagnostics.
func (g *GCSStorageBackend) Bucket() string { return g.bucket }

// WithSparseExtents makes Put write the extent encoding (ADR-1135) for
// compressed artifacts it receives as regular files. Enable it only once
// every reader of the bucket accepts that encoding.
func (g *GCSStorageBackend) WithSparseExtents(enabled bool) *GCSStorageBackend {
	g.sparseExtents = enabled
	return g
}

func (g *GCSStorageBackend) Put(ctx context.Context, key string, r io.Reader) error {
	if err := validateKey(key); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("storage: gcs put %q: %w", key, err)
	}

	body, metadata, cleanup, err := encodeRemoteArtifact(ctx, "gcs", "faas-gcs-artifact-*.zst", key, r, g.snapshotCompression, g.sparseExtents)
	if err != nil {
		return err
	}
	defer cleanup()
	r = body
	if err := g.store.Put(ctx, g.bucket, key, r, metadata); err != nil {
		return fmt.Errorf("storage: gcs put %q: %w", key, normalizeGCSError(err))
	}
	return nil
}

func (g *GCSStorageBackend) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := validateKey(key); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("storage: gcs get %q: %w", key, err)
	}
	artifact, err := g.store.Get(ctx, g.bucket, key)
	if err != nil {
		return nil, fmt.Errorf("storage: gcs get %q: %w", key, normalizeGCSError(err))
	}
	return decodeRemoteArtifact("gcs", key, artifact.body, artifact.metadata)
}

func (g *GCSStorageBackend) Delete(ctx context.Context, key string) error {
	if err := validateKey(key); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("storage: gcs delete %q: %w", key, err)
	}
	if err := g.store.Delete(ctx, g.bucket, key); err != nil && !errors.Is(err, gcs.ErrObjectNotExist) {
		return fmt.Errorf("storage: gcs delete %q: %w", key, normalizeGCSError(err))
	}
	return nil
}

func (g *GCSStorageBackend) List(ctx context.Context, prefix string) ([]string, error) {
	if prefix != "" {
		if err := validateKey(strings.TrimSuffix(prefix, "/")); err != nil {
			return nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("storage: gcs list %q: %w", prefix, err)
	}
	keys, err := g.store.List(ctx, g.bucket, prefix)
	if err != nil {
		return nil, fmt.Errorf("storage: gcs list %q: %w", prefix, normalizeGCSError(err))
	}
	for _, key := range keys {
		if err := validateKey(key); err != nil {
			return nil, fmt.Errorf("storage: gcs list %q: bucket returned invalid object name %q: %w", prefix, key, err)
		}
		if prefix != "" && !strings.HasPrefix(key, prefix) {
			return nil, fmt.Errorf("storage: gcs list %q: bucket returned out-of-prefix object %q", prefix, key)
		}
	}
	sort.Strings(keys)
	return keys, nil
}

func normalizeGCSError(err error) error {
	if errors.Is(err, gcs.ErrObjectNotExist) {
		return errors.Join(ErrNotFound, err)
	}
	return err
}

func (s *googleGCSArtifactStore) Put(ctx context.Context, bucket, key string, body io.Reader, metadata map[string]string) error {
	writeCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	w := s.client.Bucket(bucket).Object(key).NewWriter(writeCtx)
	w.ContentType = gcsContentType
	w.Metadata = metadata
	w.ChunkRetryDeadline = 2 * time.Minute
	if _, err := copyContext(ctx, w, body); err != nil {
		cancel()
		_ = w.Close()
		return err
	}
	return w.Close()
}

func (s *googleGCSArtifactStore) Get(ctx context.Context, bucket, key string) (gcsArtifact, error) {
	object := s.client.Bucket(bucket).Object(key)
	attrs, err := object.Attrs(ctx)
	if err != nil {
		return gcsArtifact{}, err
	}
	reader, err := object.Generation(attrs.Generation).NewReader(ctx)
	if err != nil {
		return gcsArtifact{}, err
	}
	metadata := make(map[string]string, len(attrs.Metadata))
	for name, value := range attrs.Metadata {
		metadata[name] = value
	}
	return gcsArtifact{body: reader, metadata: metadata, generation: attrs.Generation}, nil
}

func (s *googleGCSArtifactStore) Delete(ctx context.Context, bucket, key string) error {
	return s.client.Bucket(bucket).Object(key).Delete(ctx)
}

func (s *googleGCSArtifactStore) List(ctx context.Context, bucket, prefix string) ([]string, error) {
	iter := s.client.Bucket(bucket).Objects(ctx, &gcs.Query{Prefix: prefix, Projection: gcs.ProjectionNoACL})
	var keys []string
	for {
		attrs, err := iter.Next()
		if errors.Is(err, iterator.Done) {
			return keys, nil
		}
		if err != nil {
			return nil, err
		}
		if attrs.Name != "" {
			keys = append(keys, attrs.Name)
		}
	}
}
