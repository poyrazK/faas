package objectstorage

import (
	"context"
	"strconv"

	"cloud.google.com/go/storage"
	"github.com/onebox-faas/faas/pkg/api"
)

type gcsBucketControlStore interface {
	BucketAttrs(context.Context, string) (*storage.BucketAttrs, error)
	UpdateBucketAttrs(context.Context, string, int64, storage.BucketAttrsToUpdate) error
}

func (s *googleGCSStore) BucketAttrs(ctx context.Context, bucket string) (*storage.BucketAttrs, error) {
	return s.client.Bucket(bucket).Retryer(storage.WithPolicy(storage.RetryNever)).Attrs(ctx)
}
func (s *googleGCSStore) UpdateBucketAttrs(ctx context.Context, bucket string, meta int64, update storage.BucketAttrsToUpdate) error {
	h := s.client.Bucket(bucket)
	if meta > 0 {
		h = h.If(storage.BucketConditions{MetagenerationMatch: meta})
	}
	_, err := h.Retryer(storage.WithPolicy(storage.RetryNever)).Update(ctx, update)
	return err
}

var _ BucketVersioningProvider = (*GCS)(nil)
var _ ObjectVersionInventoryProvider = (*GCS)(nil)

func (p *GCS) GetBucketVersioning(ctx context.Context, bucket string) (BucketVersioning, error) {
	if bucket == "" {
		return BucketVersioning{}, ErrInvalid
	}
	b, err := p.store.BucketState(ctx, bucket)
	if err != nil {
		return BucketVersioning{}, normalizeGCS(err)
	}
	status := "Suspended"
	if b.VersioningEnabled {
		status = "Enabled"
	}
	return BucketVersioning{Status: status}, nil
}
func (p *GCS) PutBucketVersioning(ctx context.Context, bucket, status string) error {
	if bucket == "" || status != "Enabled" && status != "Suspended" {
		return ErrInvalid
	}
	store, ok := p.store.(gcsBucketControlStore)
	if !ok {
		return ErrUnsupported
	}
	// The service meters one mutation. A blind update touches only versioning;
	// it never replaces IAM, retention, encryption or other bucket settings.
	return normalizeGCS(store.UpdateBucketAttrs(ctx, bucket, 0, storage.BucketAttrsToUpdate{VersioningEnabled: status == "Enabled"}))
}

func (p *GCS) ListObjectVersions(ctx context.Context, bucket, cursor string, limit int32) (ObjectVersionsPage, error) {
	if bucket == "" || limit < 1 || limit > api.ObjectVersionInventoryPageSize || len(cursor) > api.ObjectVersionInventoryCursorMaxBytes {
		return ObjectVersionsPage{}, ErrInvalid
	}
	objects, next, err := p.store.ListObjectVersions(ctx, bucket, cursor, limit)
	if err != nil {
		return ObjectVersionsPage{}, normalizeGCS(err)
	}
	if len(objects) > int(limit) || len(next) > api.ObjectVersionInventoryCursorMaxBytes || next != "" && (next == cursor || len(objects) == 0) {
		return ObjectVersionsPage{}, ErrUnavailable
	}
	page := ObjectVersionsPage{Items: make([]ObjectVersionInventoryEntry, 0, len(objects)), NextCursor: next}
	seen := map[string]bool{}
	for _, object := range objects {
		identity := object.Key + "\x00" + strconv.FormatInt(object.Version, 10)
		if !ValidKey(object.Key) || object.Version <= 0 || object.Size < 0 || object.Size > api.MaxObjectUploadBytes || seen[identity] {
			return ObjectVersionsPage{}, ErrUnavailable
		}
		seen[identity] = true
		page.Items = append(page.Items, ObjectVersionInventoryEntry{Key: object.Key, ProviderVersionID: strconv.FormatInt(object.Version, 10), SizeBytes: object.Size})
	}
	return page, nil
}

var _ BucketEncryptionProvider = (*GCS)(nil)

func (p *GCS) GetBucketEncryption(ctx context.Context, bucket string) (NativeBucketEncryption, error) {
	store, ok := p.store.(gcsBucketControlStore)
	if !ok {
		return NativeBucketEncryption{}, ErrUnsupported
	}
	a, err := store.BucketAttrs(ctx, bucket)
	if err != nil {
		return NativeBucketEncryption{}, normalizeGCS(err)
	}
	if a == nil {
		return NativeBucketEncryption{}, ErrUnavailable
	}
	if a.Encryption != nil && a.Encryption.DefaultKMSKeyName != "" {
		return NativeBucketEncryption{Algorithm: "aws:kms", KeyID: a.Encryption.DefaultKMSKeyName}, nil
	}
	return NativeBucketEncryption{Algorithm: "AES256"}, nil
}

func (p *GCS) PutBucketEncryption(ctx context.Context, bucket string, e ResolvedObjectEncryption, current NativeBucketEncryption) error {
	if err := p.CheckEncryptionKey(ctx, e); err != nil {
		return err
	}
	return p.clearGCSBucketEncryption(ctx, bucket, current)
}
func (p *GCS) ClearBucketEncryption(ctx context.Context, bucket string, current NativeBucketEncryption) error {
	return p.clearGCSBucketEncryption(ctx, bucket, current)
}

func (p *GCS) clearGCSBucketEncryption(ctx context.Context, bucket string, current NativeBucketEncryption) error {
	store, ok := p.store.(gcsBucketControlStore)
	if !ok {
		return ErrUnsupported
	}
	if !validNativeBucketSettings(current) {
		return ErrInvalid
	}
	// Default CMEK and provider AES are distinct. Clearing never decrypts
	// existing objects and leaves retention, holds, CORS and IAM untouched.
	if err := beforeEncryptionKeyRequest(ctx); err != nil {
		return err
	}
	a, err := store.BucketAttrs(ctx, bucket)
	if err != nil {
		return normalizeGCS(err)
	}
	if a == nil || a.MetaGeneration <= 0 {
		return ErrUnavailable
	}
	observed := ""
	if a.Encryption != nil {
		observed = a.Encryption.DefaultKMSKeyName
	}
	if observed != current.KeyID {
		return ErrConflict
	}
	if err := beforeEncryptionWrite(ctx); err != nil {
		return err
	}
	return normalizeGCS(store.UpdateBucketAttrs(ctx, bucket, a.MetaGeneration, storage.BucketAttrsToUpdate{Encryption: &storage.BucketEncryption{}}))
}
