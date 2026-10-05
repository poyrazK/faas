package objectstorage

import (
	"context"
	"errors"
)

// VerifyMultipartAbort runs after writes have been fenced and an abort has
// succeeded. An abort acknowledgement alone does not prove that late parts
// were removed. A nonempty or truncated listing requires another abort.
func VerifyMultipartAbort(ctx context.Context, p Provider, bucket string, r MultipartAbortRequest) error {
	page, err := p.ListMultipartParts(ctx, bucket, MultipartListPartsRequest{Key: r.Key, ProviderUploadID: r.ProviderUploadID, Limit: 1})
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(page.Items) != 0 || page.NextPartNumberMarker != 0 {
		return ErrConflict
	}
	return nil
}
