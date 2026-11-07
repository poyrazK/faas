package storage

import (
	"bytes"
	"context"
	"crypto/md5" // #nosec G501 -- S3 Content-MD5 wire integrity check, not authentication.
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

const (
	// s3PartSize is the multipart part size. It bounds the memory one upload
	// holds (one part buffer) and, with S3's 10,000-part limit, the largest
	// artifact: 10,000 x 16 MiB = 156 GiB, far above any rootfs or snapshot.
	s3PartSize = 16 << 20
	s3MaxParts = 10000
	// s3MaxCopyBytes is the largest object a single CopyObject call accepts.
	// Larger objects report errors.ErrUnsupported so callers stream instead.
	s3MaxCopyBytes = 5 << 30
)

var s3BucketName = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`)

// S3Config selects an S3-compatible bucket (AWS S3, OVHcloud Object Storage,
// Cloudflare R2, MinIO, ...) for the artifact store. Unlike GCS there is no
// attached machine identity on bare metal, so the backend authenticates with a
// static access key pair from the daemon's environment file.
type S3Config struct {
	Bucket          string
	Endpoint        string
	Region          string
	AccessKeyID     string
	SecretAccessKey string
	// PathStyle addresses the bucket as https://endpoint/bucket/key instead
	// of https://bucket.endpoint/key. MinIO and some providers require it.
	PathStyle bool
}

// Validate checks the configuration without contacting the endpoint.
func (c S3Config) Validate() error {
	if err := validateS3Bucket(c.Bucket); err != nil {
		return err
	}
	if err := validateS3Endpoint(c.Endpoint); err != nil {
		return err
	}
	if strings.TrimSpace(c.Region) == "" || c.Region != strings.TrimSpace(c.Region) {
		return errors.New("storage: FAAS_S3_REGION is required (the endpoint's signing region, for example \"bhs\" or \"us-east-1\")")
	}
	if c.AccessKeyID == "" || c.SecretAccessKey == "" {
		return errors.New("storage: FAAS_S3_ACCESS_KEY_ID and FAAS_S3_SECRET_ACCESS_KEY are required")
	}
	return nil
}

func validateS3Bucket(bucket string) error {
	if !s3BucketName.MatchString(bucket) || strings.Contains(bucket, "..") || net.ParseIP(bucket) != nil {
		return fmt.Errorf("storage: invalid FAAS_S3_BUCKET=%q", bucket)
	}
	return nil
}

// validateS3Endpoint requires an HTTPS origin. Plain HTTP is accepted only for
// a loopback host, which is what a local MinIO under test listens on; artifact
// bytes and signed requests never cross a network in the clear.
func validateS3Endpoint(endpoint string) error {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return fmt.Errorf("storage: invalid FAAS_S3_ENDPOINT=%q: want an origin such as https://s3.bhs.io.cloud.ovh.net", endpoint)
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		return nil
	case "http":
		if ip := net.ParseIP(u.Hostname()); (ip != nil && ip.IsLoopback()) || u.Hostname() == "localhost" {
			return nil
		}
	}
	return fmt.Errorf("storage: FAAS_S3_ENDPOINT=%q must use https://", endpoint)
}

// S3StorageBackend stores Gregale's logical artifact keys as object names in
// one S3 bucket. Compression and its metadata follow the GCS backend exactly
// (see remote_encoding.go). A GetObject response carries the body and its
// metadata together, so a concurrent overwrite cannot pair old metadata with a
// new body.
type S3StorageBackend struct {
	bucket              string
	store               s3ArtifactStore
	snapshotCompression string
	sparseExtents       bool
}

type s3Artifact struct {
	body     io.ReadCloser
	metadata map[string]string
}

// s3ArtifactStore is the wire boundary; tests substitute an in-memory store.
// Implementations return errors wrapping ErrNotFound for an absent key.
type s3ArtifactStore interface {
	Put(ctx context.Context, bucket, key string, body io.Reader, metadata map[string]string) error
	Get(ctx context.Context, bucket, key string) (s3Artifact, error)
	Head(ctx context.Context, bucket, key string) (size int64, err error)
	Delete(ctx context.Context, bucket, key string) error
	List(ctx context.Context, bucket, prefix string) ([]string, error)
	Copy(ctx context.Context, bucket, srcKey, dstKey string) error
}

var (
	_ StorageBackend      = (*S3StorageBackend)(nil)
	_ LocalArtifactLister = (*S3StorageBackend)(nil)
	_ ExistenceChecker    = (*S3StorageBackend)(nil)
	_ ObjectCopier        = (*S3StorageBackend)(nil)
)

// NewS3StorageBackend constructs the production S3 driver. The bucket must
// already exist; its lifecycle, versioning and access policy are
// infrastructure concerns that daemon startup never mutates.
func NewS3StorageBackend(cfg S3Config, snapshotCompression string) (*S3StorageBackend, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// No overall client timeout: a multi-GB rootfs download legitimately
	// streams for minutes. A stalled server is caught by the header timeout
	// and by the caller's context.
	transport.ResponseHeaderTimeout = 60 * time.Second
	client := s3.New(s3.Options{
		Region:       cfg.Region,
		BaseEndpoint: aws.String(strings.TrimSuffix(cfg.Endpoint, "/")),
		UsePathStyle: cfg.PathStyle,
		Credentials:  credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
		HTTPClient:   &http.Client{Transport: transport},
		// Compatible stores (OVHcloud, R2, MinIO releases) disagree on the
		// SDK's newer trailing CRC checksums. Integrity comes from the
		// Content-MD5 sent with every PUT and part instead.
		RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired,
		ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired,
		RetryMaxAttempts:           3,
	})
	return newS3StorageBackend(cfg.Bucket, snapshotCompression, &awsS3ArtifactStore{client: client})
}

func newS3StorageBackend(bucket, snapshotCompression string, store s3ArtifactStore) (*S3StorageBackend, error) {
	if err := validateS3Bucket(bucket); err != nil {
		return nil, err
	}
	if store == nil {
		return nil, errors.New("storage: s3 store is nil")
	}
	compression := strings.ToLower(strings.TrimSpace(snapshotCompression))
	switch compression {
	case "", snapshotCompressionNone:
		compression = snapshotCompressionNone
	case snapshotCompressionZstd:
	default:
		return nil, fmt.Errorf("%w: unsupported snapshot compression %q", ErrInvalidKey, compression)
	}
	return &S3StorageBackend{bucket: bucket, store: store, snapshotCompression: compression}, nil
}

// Bucket returns the configured bucket name for startup diagnostics.
func (b *S3StorageBackend) Bucket() string { return b.bucket }

// WithSparseExtents makes Put write the extent encoding (ADR-1135) for
// compressed artifacts it receives as regular files. Readers accept it either
// way.
func (b *S3StorageBackend) WithSparseExtents(enabled bool) *S3StorageBackend {
	b.sparseExtents = enabled
	return b
}

func (b *S3StorageBackend) Put(ctx context.Context, key string, r io.Reader) error {
	if err := validateKey(key); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("storage: s3 put %q: %w", key, err)
	}
	body, metadata, cleanup, err := encodeRemoteArtifact(ctx, "s3", "faas-s3-artifact-*.zst", key, r, b.snapshotCompression, b.sparseExtents)
	if err != nil {
		return err
	}
	defer cleanup()
	if err := b.store.Put(ctx, b.bucket, key, body, metadata); err != nil {
		return fmt.Errorf("storage: s3 put %q: %w", key, err)
	}
	return nil
}

func (b *S3StorageBackend) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := validateKey(key); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("storage: s3 get %q: %w", key, err)
	}
	artifact, err := b.store.Get(ctx, b.bucket, key)
	if err != nil {
		return nil, fmt.Errorf("storage: s3 get %q: %w", key, err)
	}
	return decodeRemoteArtifact("s3", key, artifact.body, artifact.metadata)
}

func (b *S3StorageBackend) Delete(ctx context.Context, key string) error {
	if err := validateKey(key); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("storage: s3 delete %q: %w", key, err)
	}
	if err := b.store.Delete(ctx, b.bucket, key); err != nil && !errors.Is(err, ErrNotFound) {
		return fmt.Errorf("storage: s3 delete %q: %w", key, err)
	}
	return nil
}

func (b *S3StorageBackend) Exists(ctx context.Context, key string) (bool, error) {
	if err := validateKey(key); err != nil {
		return false, err
	}
	_, err := b.store.Head(ctx, b.bucket, key)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, ErrNotFound):
		return false, nil
	default:
		return false, fmt.Errorf("storage: s3 exists %q: %w", key, err)
	}
}

func (b *S3StorageBackend) List(ctx context.Context, prefix string) ([]string, error) {
	if prefix != "" {
		if err := validateKey(strings.TrimSuffix(prefix, "/")); err != nil {
			return nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("storage: s3 list %q: %w", prefix, err)
	}
	keys, err := b.store.List(ctx, b.bucket, prefix)
	if err != nil {
		return nil, fmt.Errorf("storage: s3 list %q: %w", prefix, err)
	}
	for _, key := range keys {
		if err := validateKey(key); err != nil {
			return nil, fmt.Errorf("storage: s3 list %q: bucket returned invalid object name %q: %w", prefix, key, err)
		}
		if prefix != "" && !strings.HasPrefix(key, prefix) {
			return nil, fmt.Errorf("storage: s3 list %q: bucket returned out-of-prefix object %q", prefix, key)
		}
	}
	sort.Strings(keys)
	return keys, nil
}

// CopyObject copies srcKey to dstKey server side, metadata included, so Get
// reads the copy exactly as the original. Objects over the single-request
// copy limit report errors.ErrUnsupported and the caller streams instead.
func (b *S3StorageBackend) CopyObject(ctx context.Context, srcKey, dstKey string) error {
	if err := validateKey(srcKey); err != nil {
		return err
	}
	if err := validateKey(dstKey); err != nil {
		return err
	}
	size, err := b.store.Head(ctx, b.bucket, srcKey)
	if err != nil {
		return fmt.Errorf("storage: s3 copy %q to %q: %w", srcKey, dstKey, err)
	}
	if size > s3MaxCopyBytes {
		return errors.ErrUnsupported
	}
	if err := b.store.Copy(ctx, b.bucket, srcKey, dstKey); err != nil {
		return fmt.Errorf("storage: s3 copy %q to %q: %w", srcKey, dstKey, err)
	}
	return nil
}

// awsS3ArtifactStore is the aws-sdk-go-v2 implementation of s3ArtifactStore.
type awsS3ArtifactStore struct {
	client *s3.Client
}

func normalizeS3Error(err error) error {
	if err == nil {
		return nil
	}
	var noKey *types.NoSuchKey
	var notFound *types.NotFound
	if errors.As(err, &noKey) || errors.As(err, &notFound) {
		return errors.Join(ErrNotFound, err)
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) && (apiErr.ErrorCode() == "NoSuchKey" || apiErr.ErrorCode() == "NotFound") {
		return errors.Join(ErrNotFound, err)
	}
	return err
}

func contentMD5(p []byte) *string {
	sum := md5.Sum(p) // #nosec G401 -- S3 Content-MD5 protocol checksum.
	return aws.String(base64.StdEncoding.EncodeToString(sum[:]))
}

// Put uploads body in one PutObject when it fits in a part, and as a
// multipart upload otherwise. The length of body need not be known in
// advance. A failed multipart upload is aborted so no orphaned parts are
// billed; the object is only visible once CompleteMultipartUpload succeeds.
func (s *awsS3ArtifactStore) Put(ctx context.Context, bucket, key string, body io.Reader, metadata map[string]string) error {
	buf := make([]byte, s3PartSize)
	n, err := readPart(ctx, body, buf)
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	if errors.Is(err, io.EOF) {
		_, putErr := s.client.PutObject(ctx, &s3.PutObjectInput{
			Bucket:        aws.String(bucket),
			Key:           aws.String(key),
			Body:          bytes.NewReader(buf[:n]),
			ContentLength: aws.Int64(int64(n)),
			ContentMD5:    contentMD5(buf[:n]),
			ContentType:   aws.String(gcsContentType),
			Metadata:      metadata,
		})
		return normalizeS3Error(putErr)
	}

	created, err := s.client.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{
		Bucket:      aws.String(bucket),
		Key:         aws.String(key),
		ContentType: aws.String(gcsContentType),
		Metadata:    metadata,
	})
	if err != nil {
		return normalizeS3Error(err)
	}
	uploadID := created.UploadId
	abort := func(cause error) error {
		abortCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		_, abortErr := s.client.AbortMultipartUpload(abortCtx, &s3.AbortMultipartUploadInput{Bucket: aws.String(bucket), Key: aws.String(key), UploadId: uploadID})
		if abortErr != nil {
			return errors.Join(cause, fmt.Errorf("abort multipart upload: %w", abortErr))
		}
		return cause
	}
	var parts []types.CompletedPart
	for partNumber := int32(1); ; partNumber++ {
		if partNumber > s3MaxParts {
			return abort(fmt.Errorf("artifact exceeds %d parts of %d bytes", s3MaxParts, s3PartSize))
		}
		uploaded, upErr := s.client.UploadPart(ctx, &s3.UploadPartInput{
			Bucket:        aws.String(bucket),
			Key:           aws.String(key),
			UploadId:      uploadID,
			PartNumber:    aws.Int32(partNumber),
			Body:          bytes.NewReader(buf[:n]),
			ContentLength: aws.Int64(int64(n)),
			ContentMD5:    contentMD5(buf[:n]),
		})
		if upErr != nil {
			return abort(normalizeS3Error(upErr))
		}
		parts = append(parts, types.CompletedPart{ETag: uploaded.ETag, PartNumber: aws.Int32(partNumber)})
		n, err = readPart(ctx, body, buf)
		if errors.Is(err, io.EOF) && n == 0 {
			break
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return abort(err)
		}
		// A short final read (io.EOF with n > 0) is uploaded by the next
		// iteration, which then observes EOF with n == 0 and completes.
	}
	if _, err := s.client.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket:          aws.String(bucket),
		Key:             aws.String(key),
		UploadId:        uploadID,
		MultipartUpload: &types.CompletedMultipartUpload{Parts: parts},
	}); err != nil {
		return abort(normalizeS3Error(err))
	}
	return nil
}

// readPart fills buf from r. It returns io.EOF when r ended before buf was
// full (n may be > 0) and nil when buf is full.
func readPart(ctx context.Context, r io.Reader, buf []byte) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	n, err := io.ReadFull(r, buf)
	switch {
	case err == nil:
		return n, nil
	case errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF):
		return n, io.EOF
	default:
		return n, err
	}
}

func (s *awsS3ArtifactStore) Get(ctx context.Context, bucket, key string) (s3Artifact, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	if err != nil {
		return s3Artifact{}, normalizeS3Error(err)
	}
	metadata := make(map[string]string, len(out.Metadata))
	for name, value := range out.Metadata {
		// S3 user metadata names are case-insensitive HTTP headers.
		metadata[strings.ToLower(name)] = value
	}
	return s3Artifact{body: out.Body, metadata: metadata}, nil
}

func (s *awsS3ArtifactStore) Head(ctx context.Context, bucket, key string) (int64, error) {
	out, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	if err != nil {
		return 0, normalizeS3Error(err)
	}
	return aws.ToInt64(out.ContentLength), nil
}

func (s *awsS3ArtifactStore) Delete(ctx context.Context, bucket, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	return normalizeS3Error(err)
}

func (s *awsS3ArtifactStore) List(ctx context.Context, bucket, prefix string) ([]string, error) {
	input := &s3.ListObjectsV2Input{Bucket: aws.String(bucket)}
	if prefix != "" {
		input.Prefix = aws.String(prefix)
	}
	var keys []string
	pages := s3.NewListObjectsV2Paginator(s.client, input)
	for pages.HasMorePages() {
		page, err := pages.NextPage(ctx)
		if err != nil {
			return nil, normalizeS3Error(err)
		}
		for _, object := range page.Contents {
			if name := aws.ToString(object.Key); name != "" {
				keys = append(keys, name)
			}
		}
	}
	return keys, nil
}

func (s *awsS3ArtifactStore) Copy(ctx context.Context, bucket, srcKey, dstKey string) error {
	_, err := s.client.CopyObject(ctx, &s3.CopyObjectInput{
		Bucket:            aws.String(bucket),
		Key:               aws.String(dstKey),
		CopySource:        aws.String(s3CopySource(bucket, srcKey)),
		MetadataDirective: types.MetadataDirectiveCopy,
	})
	return normalizeS3Error(err)
}

// s3CopySource is the x-amz-copy-source value: bucket/key with each key
// segment URL-encoded.
func s3CopySource(bucket, key string) string {
	segments := strings.Split(key, "/")
	for i, segment := range segments {
		segments[i] = url.PathEscape(segment)
	}
	return bucket + "/" + strings.Join(segments, "/")
}
