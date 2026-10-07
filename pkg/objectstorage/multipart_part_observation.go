package objectstorage

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
)

// MultipartPartObservationRequest identifies the original native upload and
// part. BeforeRequest must recheck the caller's original journal authority;
// listing permission alone does not authorize recovery of a writer.
type MultipartPartObservationRequest struct {
	Key              string
	ProviderUploadID string
	PartNumber       int32
	BeforeRequest    func(context.Context) error
}

// ObserveMultipartPart performs only a read. A returned part may belong to an
// earlier attempt, and ErrNotFound is only a negative observation. Neither
// outcome proves acceptance, ownership, rejection or drainage of an uncertain
// writer, even when its size or ETag matches the durable intent. Callers must
// not settle receipts, retry writes or release capture holds from this result.
func ObserveMultipartPart(ctx context.Context, p Provider, bucket string, r MultipartPartObservationRequest) (MultipartPart, error) {
	if bucket == "" || !ValidKey(r.Key) || !validMultipartUploadID(r.ProviderUploadID) || r.PartNumber < 1 || r.PartNumber > api.MaxMultipartParts || r.BeforeRequest == nil {
		return MultipartPart{}, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return MultipartPart{}, err
	}
	if err := r.BeforeRequest(ctx); err != nil {
		return MultipartPart{}, err
	}
	page, err := p.ListMultipartParts(ctx, bucket, MultipartListPartsRequest{
		Key: r.Key, ProviderUploadID: r.ProviderUploadID, PartNumberMarker: r.PartNumber - 1, Limit: 1,
	})
	if err != nil {
		return MultipartPart{}, err
	}
	if len(page.Items) > 1 || page.NextPartNumberMarker != 0 && (len(page.Items) != 1 || page.NextPartNumberMarker != page.Items[0].PartNumber) {
		return MultipartPart{}, ErrUnavailable
	}
	if len(page.Items) == 0 {
		return MultipartPart{}, ErrNotFound
	}
	part := page.Items[0]
	if part.PartNumber < r.PartNumber || part.PartNumber > api.MaxMultipartParts || !validUploadETag(part.ETag) || part.SizeBytes < 1 || part.SizeBytes > api.MaxObjectSinglePutBytes {
		return MultipartPart{}, ErrUnavailable
	}
	if part.PartNumber != r.PartNumber {
		return MultipartPart{}, ErrNotFound
	}
	return part, nil
}
