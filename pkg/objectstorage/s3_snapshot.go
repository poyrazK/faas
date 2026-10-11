package objectstorage

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

type s3VersionCursor struct {
	Key     string `json:"k"`
	Version string `json:"v,omitempty"`
}

func (p *S3) BucketVersioningEnabled(ctx context.Context, bucket string) (bool, error) {
	if bucket == "" {
		return false, ErrInvalid
	}
	out, err := p.client.GetBucketVersioning(ctx, &s3.GetBucketVersioningInput{Bucket: aws.String(bucket)})
	if err != nil {
		return false, normalize(err)
	}
	if out == nil {
		return false, ErrUnavailable
	}
	return out.Status == types.BucketVersioningStatusEnabled, nil
}

func (p *S3) ListSnapshotObjectVersions(ctx context.Context, bucket, cursor string, limit int32) (ObjectVersionPage, error) {
	if bucket == "" || limit < 1 || limit > 1000 || len(cursor) > 8192 {
		return ObjectVersionPage{}, ErrInvalid
	}
	in := &s3.ListObjectVersionsInput{Bucket: aws.String(bucket), MaxKeys: aws.Int32(limit)}
	if cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil {
			return ObjectVersionPage{}, ErrInvalid
		}
		var marker s3VersionCursor
		if err := json.Unmarshal(raw, &marker); err != nil || marker.Key == "" {
			return ObjectVersionPage{}, ErrInvalid
		}
		in.KeyMarker = aws.String(marker.Key)
		if marker.Version != "" {
			in.VersionIdMarker = aws.String(marker.Version)
		}
	}
	out, err := p.client.ListObjectVersions(ctx, in)
	if err != nil {
		return ObjectVersionPage{}, normalize(err)
	}
	if out == nil {
		return ObjectVersionPage{}, ErrUnavailable
	}
	page := ObjectVersionPage{Items: make([]ObjectVersion, 0, len(out.Versions)+len(out.DeleteMarkers))}
	for _, version := range out.Versions {
		page.Items = append(page.Items, ObjectVersion{
			Key: aws.ToString(version.Key), VersionID: aws.ToString(version.VersionId),
			Size: aws.ToInt64(version.Size), ETag: aws.ToString(version.ETag),
			LastModified: aws.ToTime(version.LastModified),
		})
	}
	for _, marker := range out.DeleteMarkers {
		page.Items = append(page.Items, ObjectVersion{
			Key: aws.ToString(marker.Key), VersionID: aws.ToString(marker.VersionId),
			LastModified: aws.ToTime(marker.LastModified), Deleted: true,
		})
	}
	if aws.ToBool(out.IsTruncated) {
		marker := s3VersionCursor{Key: aws.ToString(out.NextKeyMarker), Version: aws.ToString(out.NextVersionIdMarker)}
		if marker.Key == "" {
			return ObjectVersionPage{}, ErrUnavailable
		}
		raw, err := json.Marshal(marker)
		if err != nil {
			return ObjectVersionPage{}, err
		}
		page.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	return page, nil
}

func (p *S3) ReadObjectVersion(ctx context.Context, bucket, key, version string) (io.ReadCloser, error) {
	if bucket == "" || !ValidKey(key) || version == "" {
		return nil, ErrInvalid
	}
	out, err := p.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket), Key: aws.String(key), VersionId: aws.String(version),
	})
	if err != nil {
		return nil, normalize(err)
	}
	if out == nil || out.Body == nil {
		return nil, ErrUnavailable
	}
	return out.Body, nil
}
