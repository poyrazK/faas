package objectstorage

import (
	"context"
	"errors"
)

func (p *GCS) recoverGCSMultipart(ctx context.Context, bucket string, r MultipartCompleteRequest, etag string) (MultipartCompletionResult, error) {
	result := MultipartCompletionResult{RecoveryCursor: r.RecoveryCursor}
	if r.RecoveryCursor == "" {
		if err := multipartBeforeRequest(ctx, r); err != nil {
			return result, err
		}
		object, err := p.gcsProofObject(ctx, bucket, r.Key)
		if err == nil && object.Key == r.Key {
			result.VersionsObserved = object.Version > 0
			proof, proofErr := gcsReceiptProof(object, r.SessionID, r.SizeBytes, true)
			if proofErr == nil && (r.Encryption == nil || validGCSStoredEncryption(object, *r.Encryption)) {
				if etag != "" {
					proof.ETag = etag
				}
				if r.Encryption != nil {
					proof.Encryption = cloneObjectEncryption(r.Encryption.Selection)
				}
				result.UploadResult = proof
				return result, nil
			}
		} else if err != nil && !errors.Is(normalizeGCS(err), ErrNotFound) {
			return result, normalizeGCS(err)
		}
	}
	page, err := p.ConfirmTrackedObjectHistory(ctx, bucket, multipartHistoryRequest(r))
	result.RecoveryCursor = page.Cursor
	result.VersionsObserved = result.VersionsObserved || page.VersionsObserved
	if err != nil {
		return result, err
	}
	result.UploadResult = page.UploadResult
	result.RecoveryCursor = ""
	return result, nil
}
