package objectstorage

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/onebox-faas/faas/pkg/api"
)

var _ ObjectVersionInventoryProvider = (*S3)(nil)

func (p *S3) ListObjectVersions(ctx context.Context, bucket, cursor string, limit int32) (ObjectVersionsPage, error) {
	page := ObjectVersionsPage{}
	if limit < 1 || limit > api.ObjectVersionInventoryPageSize || len(cursor) > api.ObjectVersionInventoryCursorMaxBytes {
		return page, ErrInvalid
	}
	r := ObjectHistoryProofRequest{Receipt: "version-inventory", Cursor: cursor}
	c, err := decodeHistoryCursor(bucket, r)
	if err != nil {
		return page, err
	}
	in := &s3.ListObjectVersionsInput{Bucket: aws.String(bucket), MaxKeys: aws.Int32(limit), EncodingType: types.EncodingTypeUrl}
	if c.Key != "" {
		in.KeyMarker = aws.String(c.Key)
	}
	if c.Version != "" {
		in.VersionIdMarker = aws.String(c.Version)
	}
	out, err := p.client.ListObjectVersions(ctx, in, func(o *s3.Options) { o.RetryMaxAttempts = 1 })
	if err != nil {
		return page, normalizeVersionHistoryError(err)
	}
	if out == nil || out.IsTruncated == nil || len(out.CommonPrefixes) > 0 || len(out.Versions)+len(out.DeleteMarkers) > int(limit) {
		return page, ErrUnavailable
	}
	seen := map[string]bool{}
	appendEntry := func(key, id string, size int64, marker bool) error {
		decoded, e := historyResponseKey(key, out.EncodingType)
		if e != nil || !validNativeVersionID(id) || size < 0 || size > api.MaxObjectUploadBytes {
			return ErrUnavailable
		}
		identity := decoded + "\x00" + id
		if seen[identity] {
			return ErrUnavailable
		}
		seen[identity] = true
		if marker {
			size = int64(len(decoded))
		}
		page.Items = append(page.Items, ObjectVersionInventoryEntry{Key: decoded, ProviderVersionID: id, SizeBytes: size, DeleteMarker: marker})
		return nil
	}
	for _, v := range out.Versions {
		if v.Size == nil {
			return ObjectVersionsPage{}, ErrUnavailable
		}
		if err = appendEntry(aws.ToString(v.Key), aws.ToString(v.VersionId), *v.Size, false); err != nil {
			return ObjectVersionsPage{}, err
		}
	}
	for _, v := range out.DeleteMarkers {
		if err = appendEntry(aws.ToString(v.Key), aws.ToString(v.VersionId), 0, true); err != nil {
			return ObjectVersionsPage{}, err
		}
	}
	page.NextCursor, err = nextHistoryCursor(c, out, "")
	if err != nil || len(page.Items) == 0 && page.NextCursor != "" {
		return ObjectVersionsPage{}, ErrUnavailable
	}
	return page, nil
}
