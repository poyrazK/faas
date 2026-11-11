package objectstorage

import (
	"context"
	"errors"
	"net/http"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsmiddleware "github.com/aws/aws-sdk-go-v2/aws/middleware"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

var _ ObjectVersionDeleter = (*S3)(nil)
var _ GovernanceBypassDeleter = (*S3)(nil)

func (p *S3) DeleteObjectVersion(ctx context.Context, bucket, key, version string) (VersionDeleteResult, error) {
	return p.deleteObjectVersion(ctx, bucket, key, version, false)
}

// DeleteObjectVersionBypassingGovernance deletes a version under GOVERNANCE
// retention with x-amz-bypass-governance-retention. Only the platform's own
// erasure and over-ceiling deletion use it (ADR-20261111123000 decision 8).
func (p *S3) DeleteObjectVersionBypassingGovernance(ctx context.Context, bucket, key, version string) (VersionDeleteResult, error) {
	return p.deleteObjectVersion(ctx, bucket, key, version, true)
}

func (p *S3) deleteObjectVersion(ctx context.Context, bucket, key, version string, bypass bool) (VersionDeleteResult, error) {
	result := VersionDeleteResult{}
	if !ValidKey(key) || !validNativeVersionID(version) || version == "null" {
		return result, ErrInvalid
	}
	in := &s3.DeleteObjectInput{Bucket: aws.String(bucket), Key: aws.String(key), VersionId: aws.String(version)}
	if bypass {
		in.BypassGovernanceRetention = aws.Bool(true)
	}
	out, err := p.client.DeleteObject(ctx, in, func(o *s3.Options) { o.RetryMaxAttempts = 1 })
	if err != nil {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) && apiErr.ErrorCode() == "NoSuchVersion" {
			return result, nil
		}
		return result, normalizeDeletionError(err)
	}
	if out == nil || aws.ToString(out.VersionId) != "" && aws.ToString(out.VersionId) != version {
		return result, ErrUnavailable
	}
	response, ok := awsmiddleware.GetRawResponse(out.ResultMetadata).(*smithyhttp.Response)
	if !ok || response == nil || response.Response == nil || response.StatusCode != http.StatusNoContent || len(response.Header.Values("X-Amz-Version-Id")) > 1 || len(response.Header.Values("X-Amz-Delete-Marker")) > 1 {
		return result, ErrUnavailable
	}
	marker := response.Header.Get("X-Amz-Delete-Marker")
	if marker != "" && marker != "true" && marker != "false" {
		return result, ErrUnavailable
	}
	return VersionDeleteResult{DeleteMarker: aws.ToBool(out.DeleteMarker)}, nil
}
