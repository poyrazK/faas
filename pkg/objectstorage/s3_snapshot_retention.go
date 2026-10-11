package objectstorage

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

func (p *S3) ObserveObjectVersionRetention(ctx context.Context, bucket string, item ObjectVersion) (ObjectVersionRetention, error) {
	if bucket == "" || !validRetentionObject(item) || item.MetadataVersion != "" || item.VersionID == "null" {
		return ObjectVersionRetention{}, ErrInvalid
	}
	// HEAD identifies the actual version and its protection in one provider
	// response. A bucket default does not prove protection of an older version.
	out, err := p.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(bucket), Key: aws.String(item.Key), VersionId: aws.String(item.VersionID),
	})
	if err != nil {
		return ObjectVersionRetention{}, normalize(err)
	}
	if out == nil {
		return ObjectVersionRetention{}, ErrUnavailable
	}
	if aws.ToString(out.VersionId) != item.VersionID || out.ContentLength == nil || aws.ToInt64(out.ContentLength) != item.Size ||
		!aws.ToTime(out.LastModified).Equal(item.LastModified) || item.ETag != "" && aws.ToString(out.ETag) != item.ETag {
		return ObjectVersionRetention{}, ErrObjectSnapshotCopyMismatch
	}
	// Governance bypass and removable legal/event holds cannot secure a capture
	// independently of subsequent administrative or source-writer changes.
	if out.ObjectLockMode != types.ObjectLockModeCompliance || out.ObjectLockRetainUntilDate == nil || aws.ToTime(out.ObjectLockRetainUntilDate).IsZero() {
		return ObjectVersionRetention{}, ErrObjectSnapshotRetentionUnavailable
	}
	return ObjectVersionRetention{VersionID: item.VersionID, RetainedUntil: aws.ToTime(out.ObjectLockRetainUntilDate).UTC()}, nil
}

var _ ObjectVersionRetentionObserver = (*S3)(nil)
