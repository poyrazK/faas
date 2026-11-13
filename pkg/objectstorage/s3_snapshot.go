package objectstorage

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type s3VersionCursor struct {
	Key     string `json:"k"`
	Version string `json:"v,omitempty"`
}

func (p *S3) BucketVersioningEnabled(ctx context.Context, bucket string) (bool, error) {
	if bucket == "" {
		return false, ErrInvalid
	}
	out, err := p.GetBucketVersioning(ctx, bucket)
	if err != nil {
		return false, err
	}
	return out.Status == "Enabled", nil
}

func (p *S3) ListSnapshotObjectVersions(ctx context.Context, bucket, cursor string, limit int32) (ObjectVersionPage, error) {
	if bucket == "" || limit < 1 || limit > 1000 || len(cursor) > 8192 {
		return ObjectVersionPage{}, ErrInvalid
	}
	request := ObjectVersionListRequest{Limit: limit}
	if cursor != "" {
		raw, err := base64.RawURLEncoding.Strict().DecodeString(cursor)
		if err != nil {
			return ObjectVersionPage{}, ErrInvalid
		}
		var marker s3VersionCursor
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&marker) != nil || decoder.Decode(new(any)) != io.EOF || !ValidKey(marker.Key) || marker.Version != "" && !validNativeVersionID(marker.Version) {
			return ObjectVersionPage{}, ErrInvalid
		}
		request.KeyMarker, request.ProviderVersionMarker = marker.Key, marker.Version
	}
	// Reuse the validated version-list contract. Missing completion flags or
	// contradictory continuation markers cannot establish a complete snapshot.
	out, err := p.ListObjectVersionPage(ctx, bucket, request)
	if err != nil {
		return ObjectVersionPage{}, err
	}
	page := ObjectVersionPage{Items: make([]ObjectVersion, 0, len(out.Items))}
	for _, version := range out.Items {
		page.Items = append(page.Items, ObjectVersion{
			Key: version.Key, VersionID: version.ProviderVersionID,
			Size: version.Size, ETag: version.ETag, LastModified: version.LastModified,
			Deleted: version.DeleteMarker,
		})
	}
	if out.NextKeyMarker != "" {
		marker := s3VersionCursor{Key: out.NextKeyMarker, Version: out.NextProviderVersionMarker}
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
	}, func(o *s3.Options) {
		p.boundStreamClient(ctx, o)
	})
	if err != nil {
		return nil, normalize(err)
	}
	if out == nil || out.Body == nil {
		return nil, ErrUnavailable
	}
	return out.Body, nil
}
