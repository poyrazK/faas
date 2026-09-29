package objectstorage

import (
	"context"
	"strconv"
)

func (p *GCS) BucketVersioningEnabled(ctx context.Context, bucket string) (bool, error) {
	if bucket == "" {
		return false, ErrInvalid
	}
	state, err := p.store.BucketState(ctx, bucket)
	if err != nil {
		return false, normalizeGCS(err)
	}
	return state.VersioningEnabled, nil
}

func (p *GCS) ListObjectVersions(ctx context.Context, bucket, cursor string, limit int32) (ObjectVersionPage, error) {
	if bucket == "" || limit < 1 || limit > 1000 || len(cursor) > 8192 {
		return ObjectVersionPage{}, ErrInvalid
	}
	objects, next, err := p.store.ListObjectVersions(ctx, bucket, cursor, limit)
	if err != nil {
		return ObjectVersionPage{}, normalizeGCS(err)
	}
	page := ObjectVersionPage{Items: make([]ObjectVersion, 0, len(objects)), NextCursor: next}
	for _, object := range objects {
		if object.Version <= 0 || object.MetaVersion <= 0 {
			return ObjectVersionPage{}, ErrUnavailable
		}
		page.Items = append(page.Items, ObjectVersion{
			Key: object.Key, VersionID: strconv.FormatInt(object.Version, 10), MetadataVersion: strconv.FormatInt(object.MetaVersion, 10),
			Size: object.Size, ETag: object.ETag, LastModified: object.LastModified,
			ValidUntil: object.ValidUntil,
		})
	}
	return page, nil
}
