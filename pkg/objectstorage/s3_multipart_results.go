package objectstorage

import (
	"context"
	"errors"
	"net/http"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsmiddleware "github.com/aws/aws-sdk-go-v2/aws/middleware"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"github.com/aws/smithy-go/middleware"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/onebox-faas/faas/pkg/api"
)

var _ MultipartResultCompleter = (*S3)(nil)

func multipartBeforeRequest(ctx context.Context, r MultipartCompleteRequest) error {
	if r.BeforeRequest != nil {
		return r.BeforeRequest(ctx)
	}
	return ctx.Err()
}

func (p *S3) CompleteMultipartWithResult(ctx context.Context, bucket string, r MultipartCompleteRequest, c ObjectWriteConditions) (MultipartCompletionResult, error) {
	result := MultipartCompletionResult{RecoveryCursor: r.RecoveryCursor}
	if r.Encryption != nil {
		if err := p.verifyEncryption(*r.Encryption); err != nil {
			return result, err
		}
	}
	in, err := multipartCompletionInput(bucket, r, c)
	if err != nil {
		return result, err
	}
	if err = multipartBeforeRequest(ctx, r); err != nil {
		return result, err
	}
	out, err := p.client.CompleteMultipartUpload(ctx, in, func(o *s3.Options) { o.RetryMaxAttempts = 1 })
	if err == nil {
		if out != nil {
			if response, ok := awsmiddleware.GetRawResponse(out.ResultMetadata).(*smithyhttp.Response); ok && response != nil && response.Response != nil {
				result.VersionsObserved = multipartVersionsObserved(response.Header)
			}
		}
		if out == nil || !validUploadETag(aws.ToString(out.ETag)) || !validCopySnapshotVersion(out.ResultMetadata, aws.ToString(out.VersionId), "") || multipartResultIsMarker(out.ResultMetadata) || !validEncryptionResponse(out.ResultMetadata, r.Encryption) {
			return result, ErrUnavailable
		}
		result.UploadResult = UploadResult{Encryption: publicObjectEncryption(r.Encryption), ETag: aws.ToString(out.ETag), ProviderVersionID: aws.ToString(out.VersionId)}
		if err = p.confirmEncryptedMultipartResult(ctx, bucket, r, result.UploadResult); err != nil {
			result.UploadResult = UploadResult{}
			return result, err
		}
		result.RecoveryCursor = ""
		return result, nil
	}
	var response *smithyhttp.ResponseError
	if errors.As(err, &response) && response.Response != nil && response.Response.Response != nil {
		result.VersionsObserved = multipartVersionsObserved(response.Response.Header)
	}
	var bounded s3MetadataResponseError
	if errors.As(err, &bounded) {
		result.VersionsObserved = result.VersionsObserved || bounded.versionsObserved
	}
	var service smithy.APIError
	if !errors.As(err, &service) {
		return result, normalize(err)
	}
	var rejected error
	switch service.ErrorCode() {
	case "PreconditionFailed":
		rejected = ErrPreconditionFailed
	case "ConditionalRequestConflict":
		rejected = ErrConditionalConflict
	case "NoSuchKey", "NotFound":
		rejected = ErrConditionalNotFound
	case "NoSuchUpload":
		// A missing upload is ambiguous even on the first observed response:
		// completion and abort both remove the provider upload identity.
	default:
		return result, normalize(err)
	}
	if c.Empty() && service.ErrorCode() != "NoSuchUpload" {
		return result, normalize(err)
	}
	return p.recoverMultipartResult(ctx, bucket, r, result, rejected)
}

func multipartCompletionInput(bucket string, r MultipartCompleteRequest, c ObjectWriteConditions) (*s3.CompleteMultipartUploadInput, error) {
	if !c.Valid() || r.SessionID == "" || !ValidKey(r.Key) || r.ProviderUploadID == "" || r.SizeBytes <= 0 || r.SizeBytes > api.MaxObjectUploadBytes || len(r.Parts) < 1 || len(r.Parts) > api.MaxMultipartParts {
		return nil, ErrInvalid
	}
	if r.RecoveryCursor != "" {
		if _, err := decodeHistoryCursor(bucket, multipartHistoryRequest(r)); err != nil {
			return nil, err
		}
	}
	parts := make([]types.CompletedPart, 0, len(r.Parts))
	var previous int32
	for _, part := range r.Parts {
		if part.PartNumber < 1 || part.PartNumber > api.MaxMultipartParts || part.PartNumber <= previous || !validUploadETag(part.ETag) {
			return nil, ErrInvalid
		}
		previous = part.PartNumber
		parts = append(parts, types.CompletedPart{PartNumber: aws.Int32(part.PartNumber), ETag: aws.String(part.ETag)})
	}
	return &s3.CompleteMultipartUploadInput{Bucket: aws.String(bucket), Key: aws.String(r.Key), UploadId: aws.String(r.ProviderUploadID), MultipartUpload: &types.CompletedMultipartUpload{Parts: parts}, IfMatch: stringPtrOrNil(c.IfMatch), IfNoneMatch: stringPtrOrNil(c.IfNoneMatch)}, nil
}

func multipartHistoryRequest(r MultipartCompleteRequest) ObjectHistoryProofRequest {
	return ObjectHistoryProofRequest{Encryption: r.Encryption, Key: r.Key, Receipt: r.SessionID, Cursor: r.RecoveryCursor, SizeBytes: r.SizeBytes, MultipartSession: true, BeforeRequest: func(ctx context.Context) error { return multipartBeforeRequest(ctx, r) }}
}

func (p *S3) recoverMultipartResult(ctx context.Context, bucket string, r MultipartCompleteRequest, result MultipartCompletionResult, rejected error) (MultipartCompletionResult, error) {
	if err := multipartBeforeRequest(ctx, r); err != nil {
		return result, err
	}
	head, err := p.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(bucket), Key: aws.String(r.Key)}, func(o *s3.Options) { o.RetryMaxAttempts = 1 })
	if head != nil {
		if response, ok := awsmiddleware.GetRawResponse(head.ResultMetadata).(*smithyhttp.Response); ok && response != nil && response.Response != nil {
			result.VersionsObserved = result.VersionsObserved || multipartVersionsObserved(response.Header)
		}
	}
	var response *smithyhttp.ResponseError
	if errors.As(err, &response) && response.Response != nil && response.Response.Response != nil {
		result.VersionsObserved = result.VersionsObserved || multipartVersionsObserved(response.Response.Header)
	}
	if err == nil {
		if head == nil || !validCopySnapshotVersion(head.ResultMetadata, aws.ToString(head.VersionId), "") {
			return result, ErrUnavailable
		}
		if !aws.ToBool(head.DeleteMarker) && head.ContentLength != nil && *head.ContentLength == r.SizeBytes && head.Metadata[ReservedMultipartSessionMetadataKey] == r.SessionID {
			if !validUploadETag(aws.ToString(head.ETag)) || !validTrackedProofHeaders(head.ResultMetadata, ReservedMultipartSessionMetadataKey) {
				return result, ErrUnavailable
			}
			if !validEncryptionResponse(head.ResultMetadata, r.Encryption) || !validStoredEncryptionResponse(head.Metadata, head.ResultMetadata, r.Encryption) {
				return result, ErrUnavailable
			}
			result.UploadResult = UploadResult{Encryption: publicObjectEncryption(r.Encryption), ETag: aws.ToString(head.ETag), ProviderVersionID: aws.ToString(head.VersionId)}
			result.RecoveryCursor = ""
			return result, nil
		}
	} else if !errors.Is(normalize(err), ErrNotFound) {
		return result, normalize(err)
	}
	if rejected != nil && !r.Recovering {
		return result, rejected
	}
	if rejected != nil {
		// A still-existing provider upload proves this identity has not completed.
		// NoSuchUpload cannot prove whether its completion committed earlier.
		if err = multipartBeforeRequest(ctx, r); err != nil {
			return result, err
		}
		parts, e := p.client.ListParts(ctx, &s3.ListPartsInput{Bucket: aws.String(bucket), Key: aws.String(r.Key), UploadId: aws.String(r.ProviderUploadID), MaxParts: aws.Int32(1)}, func(o *s3.Options) { o.RetryMaxAttempts = 1 })
		if e == nil && parts != nil && parts.IsTruncated != nil {
			return result, rejected
		}
		if e == nil || !errors.Is(normalize(e), ErrNotFound) {
			return result, ErrUnavailable
		}
	}
	page, err := p.ConfirmTrackedObjectHistory(ctx, bucket, multipartHistoryRequest(r))
	result.RecoveryCursor, result.VersionsObserved = page.Cursor, result.VersionsObserved || page.VersionsObserved
	if err == nil {
		result.UploadResult = page.UploadResult
		return result, nil
	}
	if errors.Is(err, ErrConflict) || errors.Is(err, ErrNotFound) {
		return result, ErrUnavailable
	}
	return result, err
}

func multipartResultIsMarker(metadata middleware.Metadata) bool {
	response, ok := awsmiddleware.GetRawResponse(metadata).(*smithyhttp.Response)
	return ok && response != nil && response.Response != nil && response.Header.Get("X-Amz-Delete-Marker") == "true"
}

func validTrackedProofHeaders(metadata middleware.Metadata, receiptKey string) bool {
	response, ok := awsmiddleware.GetRawResponse(metadata).(*smithyhttp.Response)
	return ok && response != nil && response.Response != nil && len(response.Header.Values("ETag")) == 1 && len(response.Header.Values("X-Amz-Meta-"+receiptKey)) <= 1
}

func multipartVersionsObserved(headers http.Header) bool {
	for _, id := range headers.Values("X-Amz-Version-Id") {
		if id != "" && id != "null" && validNativeVersionID(id) {
			return true
		}
	}
	return headers.Get("X-Amz-Delete-Marker") == "true"
}
