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

var _ MutableObjectDeleter = (*S3)(nil)

func (p *S3) DeleteMutableObject(ctx context.Context, bucket, key, selector string) (MutableDeleteResult, error) {
	result := MutableDeleteResult{}
	if !ValidKey(key) || selector != "" && selector != "null" {
		return result, ErrInvalid
	}
	out, e := p.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(bucket), Key: aws.String(key), VersionId: stringPtrOrNil(selector)}, func(o *s3.Options) { o.RetryMaxAttempts = 1 })
	if e != nil {
		cause := normalizeVersionHistoryError(e)
		var response *smithyhttp.ResponseError
		var service smithy.APIError
		// A parsed permission rejection proves this one attempt did not mutate.
		// Status alone, transport failures and timeouts are not such proof.
		if errors.As(e, &response) && response.HTTPStatusCode() == http.StatusForbidden && errors.As(e, &service) && service.ErrorCode() == "AccessDenied" {
			cause = errors.Join(ErrDeletionRejected, cause)
		}
		return result, cause
	}
	if out == nil {
		return result, ErrUnavailable
	}
	response, ok := awsmiddleware.GetRawResponse(out.ResultMetadata).(*smithyhttp.Response)
	if !ok || response == nil || response.Response == nil || response.StatusCode != http.StatusNoContent || len(response.Header.Values("X-Amz-Version-Id")) > 1 || len(response.Header.Values("X-Amz-Delete-Marker")) > 1 {
		return result, ErrUnavailable
	}
	marker := response.Header.Get("X-Amz-Delete-Marker")
	id := aws.ToString(out.VersionId)
	if marker != "" && marker != "true" && marker != "false" || id != "" && !validNativeVersionID(id) || selector == "null" && id != "" && id != "null" {
		return result, ErrUnavailable
	}
	if selector == "null" {
		id = "null"
	}
	return MutableDeleteResult{ProviderVersionID: id, DeleteMarker: aws.ToBool(out.DeleteMarker)}, nil
}
