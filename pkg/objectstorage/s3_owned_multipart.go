package objectstorage

import (
	"context"
	"errors"
	"net/http"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsmiddleware "github.com/aws/aws-sdk-go-v2/aws/middleware"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/onebox-faas/faas/pkg/objectmultipart"
)

func ownedMultipartManifest(r MultipartCompleteRequest, c ObjectWriteConditions) objectmultipart.Manifest {
	m := objectmultipart.Manifest{UploadID: r.ProviderUploadID, Size: r.SizeBytes, IfMatch: c.IfMatch, IfNoneMatch: c.IfNoneMatch, Checksum: r.Checksum.Digest}
	for i, part := range r.Parts {
		p := objectmultipart.Part{PartNumber: part.PartNumber, ETag: part.ETag}
		if !r.Checksum.Empty() && i < len(r.Checksum.Parts) {
			p.Checksum = r.Checksum.Parts[i].Value
		}
		m.Parts = append(m.Parts, p)
	}
	return m
}

func ownedMultipartID(receipt string) string { return objectmultipart.NativeUploadID(receipt) }

// A missing receipt allows only a replay of the already authorized, immutable
// completion selection. It never establishes rejection or authorizes new parts.
func (p *S3) confirmOwnedMultipart(ctx context.Context, bucket string, r MultipartCompleteRequest, c ObjectWriteConditions) (MultipartCompletionResult, bool, error) {
	result := MultipartCompletionResult{RecoveryCursor: r.RecoveryCursor}
	if ownedMultipartID(r.SessionID) != r.ProviderUploadID || r.Encryption != nil || !capturedWriteProtection(ctx).Empty() {
		return result, false, ErrConfiguration
	}
	if err := multipartBeforeRequest(ctx, r); err != nil {
		return result, false, err
	}
	out, err := p.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(bucket), Key: aws.String(r.Key)}, func(o *s3.Options) {
		o.RetryMaxAttempts = 1
		o.APIOptions = append(o.APIOptions, ownedReceiptHeader(OwnedNodeMultipartReceiptHeader, r.SessionID))
	})
	if err != nil {
		var response *smithyhttp.ResponseError
		if errors.As(err, &response) && response.HTTPStatusCode() == http.StatusNotFound {
			return result, false, nil
		}
		if errors.Is(normalize(err), ErrConfiguration) || errors.As(err, &response) && response.HTTPStatusCode() == http.StatusForbidden {
			return result, false, ErrConfiguration
		}
		return result, false, ErrUnavailable
	}
	if out == nil || out.ContentLength == nil || *out.ContentLength != r.SizeBytes || out.Metadata[ReservedMultipartSessionMetadataKey] != r.SessionID || !validUploadETag(aws.ToString(out.ETag)) || !validTrackedProofHeaders(out.ResultMetadata, ReservedMultipartSessionMetadataKey) {
		return result, false, ErrUnavailable
	}
	raw, ok := awsmiddleware.GetRawResponse(out.ResultMetadata).(*smithyhttp.Response)
	if !ok || raw == nil || raw.Response == nil || !validOwnedReceiptHeaders(raw.Header) || len(raw.Header.Values(OwnedNodeMultipartManifestHeader)) != 1 || raw.Header.Get(OwnedNodeMultipartManifestHeader) != ownedMultipartManifest(r, c).Hash() {
		return result, false, ErrUnavailable
	}
	digest, err := checkedCompletionDigest(r.Checksum, string(out.ChecksumType), out.ChecksumCRC32, out.ChecksumCRC32C, out.ChecksumCRC64NVME, out.ChecksumSHA1, out.ChecksumSHA256)
	if err != nil {
		return result, false, err
	}
	switch raw.Header.Get(OwnedNodeReceiptStatusHeader) {
	case "committed":
		result.UploadResult, result.Checksum, result.RecoveryCursor = UploadResult{ETag: aws.ToString(out.ETag)}, digest, ""
		return result, true, nil
	case "rejected":
		if c.Empty() {
			return result, false, ErrUnavailable
		}
		return result, true, ErrPreconditionFailed
	default:
		return result, false, ErrUnavailable
	}
}
