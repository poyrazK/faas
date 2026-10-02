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

func (p *S3) DeleteObjectVersion(ctx context.Context, bucket, key, version string) (VersionDeleteResult, error) {
	result := VersionDeleteResult{}
	if !ValidKey(key) || !validNativeVersionID(version) || version == "null" {
		return result, ErrInvalid
	}
	out, err := p.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(bucket), Key: aws.String(key), VersionId: aws.String(version)}, func(o *s3.Options) { o.RetryMaxAttempts = 1 })
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
