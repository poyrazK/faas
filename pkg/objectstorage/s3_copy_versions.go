package objectstorage

import (
	"context"
	"errors"
	"net/http"

	awsmiddleware "github.com/aws/aws-sdk-go-v2/aws/middleware"
	"github.com/aws/smithy-go/middleware"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/onebox-faas/faas/pkg/api"
)

var _ VersionedTrackedObjectCopier = (*S3)(nil)
var _ VersionedMultipartPartCopier = (*S3)(nil)

func (p *S3) SnapshotVersionCopySource(ctx context.Context, bucket, key, version string) (CopySourceSnapshot, error) {
	if !validNativeVersionID(version) {
		return CopySourceSnapshot{}, ErrInvalid
	}
	return p.snapshotCopySource(ctx, bucket, key, version, api.MaxObjectSinglePutBytes)
}

func (p *S3) SnapshotVersionMultipartCopySource(ctx context.Context, bucket, key, version string) (CopySourceSnapshot, error) {
	if !validNativeVersionID(version) {
		return CopySourceSnapshot{}, ErrInvalid
	}
	return p.snapshotCopySource(ctx, bucket, key, version, api.MaxObjectUploadBytes)
}

func validCopySnapshotVersion(metadata middleware.Metadata, returned, expected string) bool {
	response, ok := awsmiddleware.GetRawResponse(metadata).(*smithyhttp.Response)
	if !ok || response == nil || response.Response == nil {
		return false
	}
	marker := response.Header.Get("X-Amz-Delete-Marker")
	if len(response.Header.Values("X-Amz-Version-Id")) > 1 || len(response.Header.Values("X-Amz-Delete-Marker")) > 1 || marker != "" && marker != "true" && marker != "false" || returned != "" && !validNativeVersionID(returned) {
		return false
	}
	return expected == "" || returned == expected || expected == "null" && returned == ""
}

func validCopyResponseSource(metadata middleware.Metadata, returned, expected string) bool {
	response, ok := awsmiddleware.GetRawResponse(metadata).(*smithyhttp.Response)
	if !ok || response == nil || response.Response == nil || len(response.Header.Values("X-Amz-Copy-Source-Version-Id")) > 1 {
		return false
	}
	// Some compatible providers omit this optional header. The request still
	// contains the exact selector. A supplied identity must agree with it.
	return returned == "" || validNativeVersionID(returned) && returned == expected
}

func copySourceHeadError(err error, version string) error {
	var response *smithyhttp.ResponseError
	if version != "" && errors.As(err, &response) && response.HTTPStatusCode() == http.StatusMethodNotAllowed {
		h := response.Response.Header
		if len(h.Values("X-Amz-Version-Id")) == 1 && len(h.Values("X-Amz-Delete-Marker")) == 1 && h.Get("X-Amz-Version-Id") == version && h.Get("X-Amz-Delete-Marker") == "true" {
			return ErrInvalid
		}
		return ErrUnavailable
	}
	return normalize(err)
}
