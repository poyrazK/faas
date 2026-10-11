package objectstorage

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"cloud.google.com/go/storage"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

var _ TrackedObjectWriter = (*GCS)(nil)
var _ TrackedObjectPresigner = (*GCS)(nil)
var _ HistoricalObjectWriteConfirmer = (*GCS)(nil)

func gcsStateFromAttrs(a *storage.ObjectAttrs) gcsObjectState {
	return gcsObjectState{Key: a.Name, ETag: a.Etag, Size: a.Size, LastModified: a.Updated, Version: a.Generation, MetaVersion: a.Metageneration, Metadata: a.Metadata, KMSKeyName: a.KMSKeyName}
}

func validGCSReceipt(key, receipt string, size int64) bool {
	id, err := uuid.Parse(receipt)
	return err == nil && id != uuid.Nil && id.String() == receipt && ValidKey(key) && size >= 0 && size <= api.MaxObjectSinglePutBytes
}

func (p *GCS) WriteTrackedObject(ctx context.Context, bucket, key, receipt string, body io.Reader, size int64, metadata ObjectMetadata) (UploadResult, error) {
	if ctx.Err() != nil || !validGCSReceipt(key, receipt, size) || size > 0 && body == nil || ValidateObjectMetadata(metadata) != nil {
		return UploadResult{}, errors.Join(ErrWriteRejected, ErrInvalid)
	}
	headers := gcsUploadHeaders(metadata)
	headers.Set("x-goog-meta-"+ReservedUploadReceiptMetadataKey, receipt)
	response, err := p.gcsStreamRequest(ctx, http.MethodPut, bucket, key, nil, headers, body, size)
	if err != nil {
		return UploadResult{}, err
	}
	defer func() { _ = response.Body.Close() }()
	return p.verifyWriteAcknowledgment(response.Header)
}

func gcsUploadHeaders(metadata ObjectMetadata) http.Header {
	headers := http.Header{}
	contentType := metadata.ContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	headers.Set("Content-Type", contentType)
	for name, value := range gcsMetadataHeaderValues(metadata.Metadata) {
		headers.Set(name, value)
	}
	for name, value := range gcsObjectContentHeaderValues(metadata) {
		headers.Set(name, value)
	}
	if tags, _ := EncodeObjectTags(metadata.Tags); tags != "" {
		headers.Set("x-goog-meta-"+ReservedObjectTagsMetadataKey, tags)
	}
	return headers
}

// A stream is never replayable. Redirects, SDK retries and cleanup deletes are
// forbidden: an interrupted acknowledgment leaves a recoverable receipt.
func (p *GCS) gcsStreamRequest(ctx context.Context, method, bucket, key string, query url.Values, headers http.Header, body io.Reader, size int64) (*http.Response, error) {
	u := *p.endpoint
	u.Path, u.RawPath, u.RawQuery = "/"+bucket+"/"+key, "", query.Encode()
	var stream io.Reader
	if body != nil && size > 0 {
		stream = io.LimitReader(body, size)
	}
	r, err := http.NewRequestWithContext(context.WithValue(ctx, gcsObjectStreamContextKey{}, true), method, u.String(), stream)
	if err != nil {
		return nil, errors.Join(ErrWriteRejected, ErrInvalid)
	}
	r.ContentLength, r.Header = size, headers.Clone()
	if r.Header == nil {
		r.Header = make(http.Header)
	}
	if (method == http.MethodGet || method == http.MethodHead) && r.Header.Get("Accept-Encoding") == "" {
		r.Header.Set("Accept-Encoding", "gzip")
	}
	client := *p.httpClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(r)
	if err != nil {
		return nil, ErrUnavailable
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return response, nil
	}
	_ = response.Body.Close()
	err = normalizeGCS(&gcsHTTPError{status: response.StatusCode})
	if response.StatusCode == http.StatusPreconditionFailed {
		err = ErrPreconditionFailed
	}
	if response.StatusCode >= 400 && response.StatusCode < 500 && response.StatusCode != http.StatusRequestTimeout {
		err = errors.Join(ErrWriteRejected, err)
	}
	return nil, err
}

func (p *GCS) PresignTrackedPut(ctx context.Context, bucket string, r SignRequest, c ObjectWriteConditions, receipt string) (SignedRequest, error) {
	if r.SizeBytes == nil || !validGCSReceipt(r.Key, receipt, *r.SizeBytes) || r.Method != http.MethodPut || !c.Valid() {
		return SignedRequest{}, ErrInvalid
	}
	return p.presignGCSConditionalPut(ctx, bucket, r, c, http.Header{"x-goog-meta-" + ReservedUploadReceiptMetadataKey: {receipt}})
}

func (p *GCS) verifyWriteAcknowledgment(headers http.Header) (UploadResult, error) {
	etags, generations := encryptionHeaderValues(headers, "ETag"), encryptionHeaderValues(headers, "X-Goog-Generation")
	if len(etags) != 1 || !validUploadETag(etags[0]) || len(generations) != 1 {
		return UploadResult{}, ErrUnavailable
	}
	if _, err := gcsGeneration(generations[0]); err != nil || len(encryptionHeaderValues(headers, "X-Amz-Version-Id")) != 0 {
		return UploadResult{}, ErrUnavailable
	}
	return UploadResult{ETag: etags[0], ProviderVersionID: generations[0]}, nil
}

func gcsGeneration(value string) (int64, error) {
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n <= 0 || strconv.FormatInt(n, 10) != value {
		return 0, ErrInvalid
	}
	return n, nil
}

func (p *GCS) ConfirmTrackedObject(ctx context.Context, bucket, key, receipt string, size int64) (UploadResult, error) {
	if !validGCSReceipt(key, receipt, size) {
		return UploadResult{}, ErrInvalid
	}
	object, err := p.gcsProofObject(ctx, bucket, key)
	if err != nil {
		return UploadResult{}, normalizeGCS(err)
	}
	if object.Key != key {
		return UploadResult{}, ErrUnavailable
	}
	return gcsReceiptProof(object, receipt, size, false)
}

func gcsReceiptProof(object gcsObjectState, receipt string, size int64, multipart bool) (UploadResult, error) {
	key := ReservedUploadReceiptMetadataKey
	if multipart {
		key = ReservedMultipartSessionMetadataKey
	}
	if object.Size != size || object.Metadata[key] != receipt {
		return UploadResult{}, ErrConflict
	}
	if object.Version <= 0 || !validUploadETag(object.ETag) {
		return UploadResult{}, ErrUnavailable
	}
	return UploadResult{ETag: object.ETag, ProviderVersionID: strconv.FormatInt(object.Version, 10)}, nil
}

func (p *GCS) ConfirmTrackedObjectHistory(ctx context.Context, bucket string, r ObjectHistoryProofRequest) (ObjectHistoryProofPage, error) {
	page := ObjectHistoryProofPage{Cursor: r.Cursor}
	maxBytes := api.MaxObjectSinglePutBytes
	if r.MultipartSession {
		maxBytes = api.MaxObjectUploadBytes
	}
	if !validGCSReceipt(r.Key, r.Receipt, min(r.SizeBytes, api.MaxObjectSinglePutBytes)) || r.SizeBytes < 0 || r.SizeBytes > maxBytes || r.BeforeRequest == nil {
		return page, ErrInvalid
	}
	if r.Encryption != nil {
		if err := p.CheckEncryptionKey(ctx, *r.Encryption); err != nil {
			return page, err
		}
	}
	cursor, err := decodeGCSHistoryCursor(bucket, r)
	if err != nil {
		return page, err
	}
	if err := r.BeforeRequest(ctx); err != nil {
		return page, err
	}
	objects, next, err := p.store.ListObjectVersions(ctx, bucket, cursor, api.ObjectVersionInventoryPageSize)
	if err != nil {
		return page, normalizeGCS(err)
	}
	if len(objects) > api.ObjectVersionInventoryPageSize || len(next) > api.ObjectVersionInventoryCursorMaxBytes || next != "" && (next == cursor || len(objects) == 0) {
		return page, ErrUnavailable
	}
	seen := map[string]bool{}
	for _, object := range objects {
		identity := object.Key + "\x00" + strconv.FormatInt(object.Version, 10)
		if !ValidKey(object.Key) || object.Version <= 0 || object.Size < 0 || object.Size > api.MaxObjectUploadBytes || seen[identity] {
			return page, ErrUnavailable
		}
		seen[identity] = true
	}
	for _, object := range objects {
		if object.Key != r.Key {
			continue
		}
		proof, err := gcsReceiptProof(object, r.Receipt, r.SizeBytes, r.MultipartSession)
		if err == nil && (r.Encryption == nil || validGCSStoredEncryption(object, *r.Encryption)) {
			if _, native := p.store.(*googleGCSStore); native {
				if err := r.BeforeRequest(ctx); err != nil {
					return page, err
				}
				verified, verifyErr := p.gcsXMLProofObject(ctx, bucket, r.Key, strconv.FormatInt(object.Version, 10))
				if verifyErr != nil {
					return page, verifyErr
				}
				proof, err = gcsReceiptProof(verified, r.Receipt, r.SizeBytes, r.MultipartSession)
				if err != nil || r.Encryption != nil && !validGCSStoredEncryption(verified, *r.Encryption) {
					continue
				}
			}
			if r.Encryption != nil {
				proof.Encryption = cloneObjectEncryption(r.Encryption.Selection)
			}
			page.UploadResult, page.VersionsObserved = proof, true
			return page, nil
		}
	}
	page.VersionsObserved = len(objects) > 0
	page.Cursor, err = encodeGCSHistoryCursor(bucket, r, next)
	if err != nil {
		return ObjectHistoryProofPage{Cursor: r.Cursor}, err
	}
	return page, ErrConflict
}
