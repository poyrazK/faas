package objectstorage

import (
	"context"
	"strconv"
	"time"

	"cloud.google.com/go/storage"
)

type gcsObjectRetentionStore interface {
	ObjectVersionRetention(context.Context, string, string, int64, int64) (*storage.ObjectAttrs, *storage.RetentionPolicy, error)
}

func (s *googleGCSStore) ObjectVersionRetention(ctx context.Context, bucket, key string, generation, metageneration int64) (*storage.ObjectAttrs, *storage.RetentionPolicy, error) {
	attrs, err := s.client.Bucket(bucket).Object(key).Generation(generation).If(storage.Conditions{MetagenerationMatch: metageneration}).Attrs(ctx)
	if err != nil {
		return nil, nil, err
	}
	if attrs.Retention != nil && attrs.Retention.Mode == "Locked" {
		return attrs, nil, nil
	}
	bucketAttrs, err := s.client.Bucket(bucket).Attrs(ctx)
	if err != nil {
		return nil, nil, err
	}
	return attrs, bucketAttrs.RetentionPolicy, nil
}

func (p *GCS) ObserveObjectVersionRetention(ctx context.Context, bucket string, item ObjectVersion) (ObjectVersionRetention, error) {
	generation, generationErr := strconv.ParseInt(item.VersionID, 10, 64)
	metageneration, metadataErr := strconv.ParseInt(item.MetadataVersion, 10, 64)
	if bucket == "" || !validRetentionObject(item) || generationErr != nil || generation <= 0 || metadataErr != nil || metageneration <= 0 ||
		strconv.FormatInt(generation, 10) != item.VersionID || strconv.FormatInt(metageneration, 10) != item.MetadataVersion {
		return ObjectVersionRetention{}, ErrInvalid
	}
	store, ok := p.store.(gcsObjectRetentionStore)
	if !ok {
		return ObjectVersionRetention{}, ErrUnsupported
	}
	attrs, policy, err := store.ObjectVersionRetention(ctx, bucket, item.Key, generation, metageneration)
	if err != nil {
		return ObjectVersionRetention{}, normalizeGCS(err)
	}
	if attrs == nil {
		return ObjectVersionRetention{}, ErrUnavailable
	}
	if attrs.Bucket != bucket || attrs.Name != item.Key || attrs.Generation != generation || attrs.Metageneration != metageneration ||
		attrs.Size != item.Size || !attrs.Created.Equal(item.LastModified) || item.ETag != "" && attrs.Etag != item.ETag {
		return ObjectVersionRetention{}, ErrObjectSnapshotCopyMismatch
	}
	var observed ObjectVersionRetention
	observed.VersionID, observed.MetadataVersion = item.VersionID, item.MetadataVersion
	if attrs.Retention != nil && attrs.Retention.Mode == "Locked" {
		observed.RetainedUntil = attrs.Retention.RetainUntil.UTC()
	} else if policy != nil && policy.IsLocked && policy.RetentionPeriod >= 24*time.Hour && !policy.EffectiveTime.IsZero() {
		// Use the server's per-generation expiration, never creation time plus
		// a mutable bucket default. GCS does not guarantee sub-day policies.
		observed.RetainedUntil = attrs.RetentionExpirationTime.UTC()
	}
	if observed.RetainedUntil.IsZero() {
		return ObjectVersionRetention{}, ErrObjectSnapshotRetentionUnavailable
	}
	return observed, nil
}

var _ ObjectVersionRetentionObserver = (*GCS)(nil)
