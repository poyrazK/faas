package objectstorage

import "net/http"

// Native GCS generation headers differ from S3 version headers. The selected
// provider validates its own identity rather than guessing from response data.
func VerifyProviderObjectWriteAcknowledgment(p Provider, headers http.Header) (UploadResult, error) {
	if gcs, ok := p.(*GCS); ok {
		return gcs.verifyWriteAcknowledgment(headers)
	}
	return VerifyObjectWriteAcknowledgment(headers)
}

// VerifyObjectWriteAcknowledgment validates native PUT identity headers before
// a broker publishes success or settles a dispatched receipt.
func VerifyObjectWriteAcknowledgment(headers http.Header) (UploadResult, error) {
	etag, version, marker := headers.Get("ETag"), headers.Get("X-Amz-Version-Id"), headers.Get("X-Amz-Delete-Marker")
	if len(headers.Values("ETag")) != 1 || !validUploadETag(etag) || len(headers.Values("X-Amz-Version-Id")) > 1 || version != "" && !validNativeVersionID(version) || len(headers.Values("X-Amz-Delete-Marker")) > 1 || marker != "" && marker != "false" || len(headers.Values("X-Amz-Meta-"+ReservedUploadReceiptMetadataKey)) > 1 {
		return UploadResult{}, ErrUnavailable
	}
	return UploadResult{ETag: etag, ProviderVersionID: version}, nil
}
