package objectstorage

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/onebox-faas/faas/pkg/api"
)

var (
	_ BucketObjectLockProvider  = (*S3)(nil)
	_ ObjectVersionLockProvider = (*S3)(nil)
)

func validObjectLockBucket(bucket string) bool {
	return bucket != "" && !strings.ContainsAny(bucket, "\x00\r\n")
}

func validObjectLockTarget(bucket, key, version string) bool {
	return validObjectLockBucket(bucket) && ValidKey(key) && validNativeVersionID(version)
}

func (p *S3) GetBucketObjectLock(ctx context.Context, bucket string) (api.ObjectBucketObjectLockConfiguration, error) {
	var result api.ObjectBucketObjectLockConfiguration
	if !validObjectLockBucket(bucket) {
		return result, ErrInvalid
	}
	out, err := p.client.GetObjectLockConfiguration(ctx, &s3.GetObjectLockConfigurationInput{Bucket: aws.String(bucket)}, func(o *s3.Options) {
		o.RetryMaxAttempts = 1
		o.HTTPClient = objectLockResponseClient{base: o.HTTPClient, read: func(body []byte) error {
			var readErr error
			result, readErr = parseBucketObjectLock(body)
			return readErr
		}}
	})
	if err != nil {
		if missingObjectLockConfiguration(err) {
			return api.ObjectBucketObjectLockConfiguration{}, nil
		}
		// Keep only the conservative enablement observation on an unreadable
		// policy. A caller must not rewrite an incomplete default rule.
		return api.ObjectBucketObjectLockConfiguration{Enabled: result.Enabled}, normalizeObjectLockError(err)
	}
	if out == nil {
		return api.ObjectBucketObjectLockConfiguration{}, ErrUnavailable
	}
	return result, nil
}

func (p *S3) PutBucketObjectLock(ctx context.Context, bucket string, c api.ObjectBucketObjectLockConfiguration) error {
	c = c.Clone()
	if !validObjectLockBucket(bucket) || !c.Enabled || !c.Valid() {
		return ErrInvalid
	}
	native := &types.ObjectLockConfiguration{ObjectLockEnabled: types.ObjectLockEnabledEnabled}
	if r := c.DefaultRetention; r != nil {
		native.Rule = &types.ObjectLockRule{DefaultRetention: &types.DefaultRetention{Mode: types.ObjectLockRetentionMode(r.Mode), Days: r.Days, Years: r.Years, DefaultEventHold: nativeEventHold(r.DefaultEventHold)}}
	}
	_, err := p.client.PutObjectLockConfiguration(ctx, &s3.PutObjectLockConfigurationInput{Bucket: aws.String(bucket), ObjectLockConfiguration: native, ChecksumAlgorithm: types.ChecksumAlgorithmSha256}, objectLockWriteOptions(""))
	return normalizeObjectLockError(err)
}

func (p *S3) GetObjectVersionRetention(ctx context.Context, bucket, key, version string) (api.ObjectVersionRetention, error) {
	var result api.ObjectVersionRetention
	if !validObjectLockTarget(bucket, key, version) {
		return result, ErrInvalid
	}
	out, err := p.client.GetObjectRetention(ctx, &s3.GetObjectRetentionInput{Bucket: aws.String(bucket), Key: aws.String(key), VersionId: aws.String(version)}, func(o *s3.Options) {
		o.RetryMaxAttempts = 1
		o.HTTPClient = objectLockResponseClient{base: o.HTTPClient, version: version, read: func(body []byte) error {
			var readErr error
			result, readErr = parseObjectRetention(body)
			return readErr
		}}
	})
	if err != nil {
		return api.ObjectVersionRetention{}, normalizeObjectLockError(err)
	}
	if out == nil {
		return api.ObjectVersionRetention{}, ErrUnavailable
	}
	return result, nil
}

func (p *S3) PutObjectVersionRetention(ctx context.Context, bucket, key, version string, r api.ObjectVersionRetention, bypass bool) error {
	r = r.ForWrite()
	if !validObjectLockTarget(bucket, key, version) || !r.ValidForWrite() {
		return ErrInvalid
	}
	in := &s3.PutObjectRetentionInput{Bucket: aws.String(bucket), Key: aws.String(key), VersionId: aws.String(version), ChecksumAlgorithm: types.ChecksumAlgorithmSha256, Retention: &types.ObjectLockRetention{Mode: types.ObjectLockRetentionMode(r.Mode), RetainUntilDate: r.RetainUntilDate, EventHold: types.ObjectLockEventHold(r.EventHold), EventHoldDuration: nativeEventHold(r.EventHoldDuration)}}
	if bypass {
		in.BypassGovernanceRetention = aws.Bool(true)
	}
	_, err := p.client.PutObjectRetention(ctx, in, objectLockWriteOptions(version))
	return normalizeProtectionWriteError(err)
}

func (p *S3) GetObjectVersionLegalHold(ctx context.Context, bucket, key, version string) (api.ObjectVersionLegalHold, error) {
	var result api.ObjectVersionLegalHold
	if !validObjectLockTarget(bucket, key, version) {
		return result, ErrInvalid
	}
	out, err := p.client.GetObjectLegalHold(ctx, &s3.GetObjectLegalHoldInput{Bucket: aws.String(bucket), Key: aws.String(key), VersionId: aws.String(version)}, func(o *s3.Options) {
		o.RetryMaxAttempts = 1
		o.HTTPClient = objectLockResponseClient{base: o.HTTPClient, version: version, read: func(body []byte) error {
			var readErr error
			result, readErr = parseObjectLegalHold(body)
			return readErr
		}}
	})
	if err != nil {
		return api.ObjectVersionLegalHold{}, normalizeObjectLockError(err)
	}
	if out == nil {
		return api.ObjectVersionLegalHold{}, ErrUnavailable
	}
	return result, nil
}

func (p *S3) PutObjectVersionLegalHold(ctx context.Context, bucket, key, version string, h api.ObjectVersionLegalHold) error {
	if !validObjectLockTarget(bucket, key, version) || !h.Valid() {
		return ErrInvalid
	}
	_, err := p.client.PutObjectLegalHold(ctx, &s3.PutObjectLegalHoldInput{Bucket: aws.String(bucket), Key: aws.String(key), VersionId: aws.String(version), LegalHold: &types.ObjectLockLegalHold{Status: types.ObjectLockLegalHoldStatus(h.Status)}, ChecksumAlgorithm: types.ChecksumAlgorithmSha256}, objectLockWriteOptions(version))
	return normalizeProtectionWriteError(err)
}

func normalizeProtectionWriteError(err error) error {
	var response *smithyhttp.ResponseError
	var service smithy.APIError
	if errors.As(err, &response) && errors.As(err, &service) {
		status, code := response.HTTPStatusCode(), service.ErrorCode()
		if status == http.StatusForbidden && code == "AccessDenied" || status == http.StatusNotFound && (code == "NoSuchKey" || code == "NoSuchVersion") || status == http.StatusBadRequest && (code == "MalformedXML" || code == "InvalidArgument" || code == "InvalidRequest") {
			return fmt.Errorf("%w: %w", ErrProtectionRejected, normalizeObjectLockError(err))
		}
	}
	return normalizeObjectLockError(err)
}

func nativeEventHold(period *api.ObjectRetentionPeriod) *types.EventHoldDuration {
	if period == nil {
		return nil
	}
	return &types.EventHoldDuration{Days: period.Days, Years: period.Years}
}

func objectLockWriteOptions(version string) func(*s3.Options) {
	return func(o *s3.Options) {
		o.RetryMaxAttempts = 1
		o.HTTPClient = objectLockResponseClient{base: o.HTTPClient, version: version}
	}
}

func missingObjectLockConfiguration(err error) bool {
	var service smithy.APIError
	var response *smithyhttp.ResponseError
	return errors.As(err, &service) && service.ErrorCode() == "ObjectLockConfigurationNotFoundError" && errors.As(err, &response) && response.HTTPStatusCode() == 404
}

func normalizeObjectLockError(err error) error {
	if err == nil {
		return nil
	}
	for _, kind := range []error{ErrInvalid, ErrUnavailable, ErrUnsupported} {
		if errors.Is(err, kind) {
			return kind
		}
	}
	var service smithy.APIError
	if errors.As(err, &service) && service.ErrorCode() == "MalformedXML" {
		return ErrInvalid
	}
	return normalizeVersionHistoryError(err)
}
