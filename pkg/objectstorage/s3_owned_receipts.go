package objectstorage

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsmiddleware "github.com/aws/aws-sdk-go-v2/aws/middleware"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go/middleware"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

// These headers belong to the private, operator-authenticated node protocol.
// The receipt selector must be signed and is never forwarded from tenant input.
const (
	OwnedNodeReceiptHeader           = "X-Gregale-Write-Receipt"
	OwnedNodeReceiptStatusHeader     = "X-Gregale-Write-Receipt-Status"
	OwnedNodeMultipartReceiptHeader  = "X-Gregale-Multipart-Receipt"
	OwnedNodeMultipartManifestHeader = "X-Gregale-Multipart-Manifest"
)

// ProviderWriteDefinitelyRejected classifies a single tracked HTTP attempt.
// Owned-node replay conflicts can describe an already committed original and
// must be resolved through its durable receipt instead of releasing admission.
func ProviderWriteDefinitelyRejected(provider Provider, status int) bool {
	if status < 400 || status >= 500 || status == http.StatusRequestTimeout {
		return false
	}
	p, owned := provider.(*S3)
	return !owned || !p.ownedNodeReceipts || status != http.StatusConflict
}

func (p *S3) confirmOwnedNodeReceipt(ctx context.Context, bucket, key, receipt string, size int64) (UploadResult, error) {
	out, err := p.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)}, func(o *s3.Options) {
		o.RetryMaxAttempts = 1
		o.APIOptions = append(o.APIOptions, ownedReceiptSelector(receipt))
	})
	// A 404, malformed reply or transport failure is never negative proof and
	// must not trigger the generic provider's native-version history fallback.
	if err != nil {
		var response *smithyhttp.ResponseError
		if errors.Is(normalize(err), ErrConfiguration) || errors.As(err, &response) && (response.HTTPStatusCode() == http.StatusForbidden || response.HTTPStatusCode() == http.StatusUnauthorized) {
			return UploadResult{}, ErrConfiguration
		}
		return UploadResult{}, ErrUnavailable
	}
	if out == nil || out.ContentLength == nil || *out.ContentLength != size || out.Metadata[ReservedUploadReceiptMetadataKey] != receipt || !validUploadETag(aws.ToString(out.ETag)) || !validTrackedProofHeaders(out.ResultMetadata, ReservedUploadReceiptMetadataKey) {
		return UploadResult{}, ErrUnavailable
	}
	response, ok := awsmiddleware.GetRawResponse(out.ResultMetadata).(*smithyhttp.Response)
	if !ok || response == nil || response.Response == nil || !validOwnedReceiptHeaders(response.Header) {
		return UploadResult{}, ErrUnavailable
	}
	switch response.Header.Get(OwnedNodeReceiptStatusHeader) {
	case "committed":
		return UploadResult{ETag: aws.ToString(out.ETag)}, nil
	case "rejected":
		return UploadResult{}, ErrWriteRejected
	default:
		return UploadResult{}, ErrUnavailable
	}
}

func validOwnedReceiptHeaders(headers http.Header) bool {
	if len(headers.Values(OwnedNodeReceiptStatusHeader)) != 1 || len(headers.Values("X-Amz-Version-Id")) != 0 || len(headers.Values("X-Amz-Delete-Marker")) != 0 {
		return false
	}
	for name := range headers {
		name = strings.ToLower(name)
		if strings.HasPrefix(name, "x-amz-server-side-encryption") || strings.HasPrefix(name, "x-amz-object-lock-") || name == "x-amz-meta-"+ReservedObjectEncryptionMetadataKey || name == "x-amz-meta-"+ReservedObjectProtectionMetadataKey {
			return false
		}
	}
	return true
}

func ownedReceiptSelector(receipt string) func(*middleware.Stack) error {
	return ownedReceiptHeader(OwnedNodeReceiptHeader, receipt)
}

func ownedReceiptHeader(header, receipt string) func(*middleware.Stack) error {
	return func(stack *middleware.Stack) error {
		return stack.Build.Add(middleware.BuildMiddlewareFunc("GregaleOwnedNodeReceipt", func(ctx context.Context, in middleware.BuildInput, next middleware.BuildHandler) (middleware.BuildOutput, middleware.Metadata, error) {
			req, ok := in.Request.(*smithyhttp.Request)
			if !ok {
				return middleware.BuildOutput{}, middleware.Metadata{}, ErrUnavailable
			}
			req.Header.Set(header, receipt)
			return next.HandleBuild(ctx, in)
		}), middleware.After)
	}
}

func (p *S3) ownedReceiptConflict(err error) bool {
	var response *smithyhttp.ResponseError
	// A conflicting replay may refer to an already committed original write.
	return p.ownedNodeReceipts && errors.As(err, &response) && response.HTTPStatusCode() == http.StatusConflict
}
