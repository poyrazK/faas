package objectstorage

import (
	"context"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var _ CrossBucketTrackedObjectCopier = (*GCS)(nil)
var _ CrossBucketMultipartPartCopier = (*GCS)(nil)

func (p *GCS) SnapshotCopySource(ctx context.Context, bucket, key string) (CopySourceSnapshot, error) {
	return p.snapshotGCSCopySource(ctx, bucket, key, "", api.MaxObjectSinglePutBytes)
}
func (p *GCS) SnapshotVersionCopySource(ctx context.Context, bucket, key, version string) (CopySourceSnapshot, error) {
	return p.snapshotGCSCopySource(ctx, bucket, key, version, api.MaxObjectSinglePutBytes)
}
func (p *GCS) SnapshotMultipartCopySource(ctx context.Context, bucket, key string) (CopySourceSnapshot, error) {
	return p.snapshotGCSCopySource(ctx, bucket, key, "", api.MaxObjectUploadBytes)
}
func (p *GCS) SnapshotVersionMultipartCopySource(ctx context.Context, bucket, key, version string) (CopySourceSnapshot, error) {
	return p.snapshotGCSCopySource(ctx, bucket, key, version, api.MaxObjectUploadBytes)
}

func (p *GCS) snapshotGCSCopySource(ctx context.Context, bucket, key, version string, maxBytes int64) (CopySourceSnapshot, error) {
	if !ValidKey(key) || bucket == "" {
		return CopySourceSnapshot{}, ErrInvalid
	}
	query := url.Values{}
	if version != "" {
		if _, err := gcsGeneration(version); err != nil {
			return CopySourceSnapshot{}, err
		}
		query.Set("generation", version)
	}
	response, err := p.gcsStreamRequest(ctx, http.MethodHead, bucket, key, query, nil, nil, 0)
	if err != nil {
		return CopySourceSnapshot{}, err
	}
	defer func() { _ = response.Body.Close() }()
	h := response.Header
	if _, err := ProviderReadEncryption(p, p.encryption, "", h); err != nil {
		return CopySourceSnapshot{}, err
	}
	identity, err := p.verifyWriteAcknowledgment(h)
	if err != nil || response.StatusCode != http.StatusOK || response.Uncompressed || response.ContentLength < 0 || response.ContentLength > maxBytes {
		return CopySourceSnapshot{}, ErrUnavailable
	}
	metas := encryptionHeaderValues(h, "X-Goog-Metageneration")
	if len(metas) != 1 {
		return CopySourceSnapshot{}, ErrUnavailable
	}
	if _, err = gcsGeneration(metas[0]); err != nil {
		return CopySourceSnapshot{}, ErrUnavailable
	}
	if version != "" && version != identity.ProviderVersionID {
		return CopySourceSnapshot{}, ErrUnavailable
	}
	m := ObjectMetadata{ContentType: h.Get("Content-Type"), CacheControl: h.Get("Cache-Control"), ContentDisposition: h.Get("Content-Disposition"), ContentEncoding: h.Get("Content-Encoding"), ContentLanguage: h.Get("Content-Language"), Metadata: map[string]string{}}
	for name, values := range h {
		if strings.HasPrefix(strings.ToLower(name), "x-goog-meta-") {
			if len(values) != 1 {
				return CopySourceSnapshot{}, ErrUnavailable
			}
			m.Metadata[strings.TrimPrefix(strings.ToLower(name), "x-goog-meta-")] = values[0]
		}
	}
	m.Tags, err = gcsTagsFromMetadata(m.Metadata)
	if err != nil {
		return CopySourceSnapshot{}, err
	}
	m.Metadata = copyCustomerMetadata(m.Metadata)
	modified, err := http.ParseTime(h.Get("Last-Modified"))
	if err != nil || ValidateObjectMetadata(m) != nil {
		return CopySourceSnapshot{}, ErrUnavailable
	}
	return CopySourceSnapshot{SizeBytes: response.ContentLength, ETag: identity.ETag, Metadata: m, ProviderVersionID: identity.ProviderVersionID, ProviderMetadataVersion: metas[0], LastModified: modified}, nil
}

func checkGCSCopySource(source CopySourceSnapshot, c CopySourceConditions, maxBytes int64) error {
	if !validCopySourceSize(source, maxBytes) {
		return ErrInvalid
	}
	if _, err := gcsGeneration(source.ProviderVersionID); err != nil {
		return ErrInvalid
	}
	if _, err := gcsGeneration(source.ProviderMetadataVersion); err != nil {
		return ErrInvalid
	}
	if err := c.Check(source); err != nil {
		return err
	}
	if c.HasDates() && source.LastModified.IsZero() {
		return ErrUnavailable
	}
	if c.IfModifiedSince != nil && !source.LastModified.After(*c.IfModifiedSince) || c.IfMatch == "" && c.IfUnmodifiedSince != nil && source.LastModified.After(*c.IfUnmodifiedSince) {
		return ErrPreconditionFailed
	}
	return nil
}

func (p *GCS) CopyTrackedObject(ctx context.Context, bucket, receipt string, r CopyObjectRequest, source CopySourceSnapshot) (CopyObjectResult, error) {
	return p.copyGCSTracked(ctx, bucket, bucket, receipt, r, source, CopySourceConditions{})
}
func (p *GCS) CopyConditionalTrackedObject(ctx context.Context, bucket, receipt string, r CopyObjectRequest, source CopySourceSnapshot, c CopySourceConditions) (CopyObjectResult, error) {
	return p.copyGCSTracked(ctx, bucket, bucket, receipt, r, source, c)
}
func (p *GCS) CopyDateConditionalTrackedObject(ctx context.Context, bucket, receipt string, r CopyObjectRequest, source CopySourceSnapshot, c CopySourceConditions) (CopyObjectResult, error) {
	return p.copyGCSTracked(ctx, bucket, bucket, receipt, r, source, c)
}
func (p *GCS) CopyCrossBucketTrackedObject(ctx context.Context, sourceBucket, destinationBucket, receipt string, r CopyObjectRequest, source CopySourceSnapshot, c CopySourceConditions, e ResolvedObjectEncryption) (CopyObjectResult, error) {
	if e.Empty() {
		return p.copyGCSTracked(ctx, sourceBucket, destinationBucket, receipt, r, source, c)
	}
	if err := p.CheckEncryptionKey(ctx, e); err != nil {
		return CopyObjectResult{}, errors.Join(ErrWriteRejected, err)
	}
	return p.copyGCSTrackedEncrypted(ctx, sourceBucket, destinationBucket, receipt, r, source, c, &e)
}

func (p *GCS) copyGCSTracked(ctx context.Context, sourceBucket, destinationBucket, receipt string, r CopyObjectRequest, source CopySourceSnapshot, c CopySourceConditions) (CopyObjectResult, error) {
	return p.copyGCSTrackedEncrypted(ctx, sourceBucket, destinationBucket, receipt, r, source, c, nil)
}

func (p *GCS) copyGCSTrackedEncrypted(ctx context.Context, sourceBucket, destinationBucket, receipt string, r CopyObjectRequest, source CopySourceSnapshot, c CopySourceConditions, encryption *ResolvedObjectEncryption) (CopyObjectResult, error) {
	if !validGCSReceipt(r.DestinationKey, receipt, source.SizeBytes) || ctx.Err() != nil || !ValidKey(r.SourceKey) || sourceBucket == "" || destinationBucket == "" || ValidateObjectMetadata(r.Metadata) != nil || r.SourceProviderVersionID != "" && r.SourceProviderVersionID != source.ProviderVersionID {
		return CopyObjectResult{}, errors.Join(ErrWriteRejected, ErrInvalid)
	}
	if err := checkGCSCopySource(source, c, api.MaxObjectSinglePutBytes); err != nil {
		return CopyObjectResult{}, errors.Join(ErrWriteRejected, err)
	}
	if r.MetadataDirective != "" && r.MetadataDirective != "COPY" && r.MetadataDirective != "REPLACE" || r.TaggingDirective != "" && r.TaggingDirective != "COPY" && r.TaggingDirective != "REPLACE" {
		return CopyObjectResult{}, errors.Join(ErrWriteRejected, ErrInvalid)
	}
	metadata := r.Metadata
	if r.MetadataDirective == "" || r.MetadataDirective == "COPY" {
		metadata = source.Metadata
	}
	if r.TaggingDirective == "" || r.TaggingDirective == "COPY" {
		metadata.Tags = source.Metadata.Tags
	} else {
		metadata.Tags = r.Metadata.Tags
	}
	headers := gcsUploadHeaders(metadata)
	headers.Set("x-goog-meta-"+ReservedUploadReceiptMetadataKey, receipt)
	headers.Set("x-goog-metadata-directive", "REPLACE")
	headers.Set("x-goog-copy-source", "/"+sourceBucket+"/"+url.PathEscape(r.SourceKey))
	headers.Set("x-goog-copy-source-generation", source.ProviderVersionID)
	headers.Set("x-goog-copy-source-if-generation-match", source.ProviderVersionID)
	headers.Set("x-goog-copy-source-if-metageneration-match", source.ProviderMetadataVersion)
	if encryption != nil {
		headers.Set("x-goog-meta-"+ReservedObjectEncryptionMetadataKey, encryption.Proof())
		if err := beforeEncryptionWrite(ctx); err != nil {
			return CopyObjectResult{}, errors.Join(ErrWriteRejected, err)
		}
	}
	response, err := p.gcsStreamRequest(ctx, http.MethodPut, destinationBucket, r.DestinationKey, nil, headers, nil, 0)
	if err != nil {
		return CopyObjectResult{}, err
	}
	defer func() { _ = response.Body.Close() }()
	var result struct {
		XMLName      xml.Name  `xml:"CopyObjectResult"`
		ETag         string    `xml:"ETag"`
		LastModified time.Time `xml:"LastModified"`
	}
	data, readErr := io.ReadAll(io.LimitReader(response.Body, gcsMaxControlBody+1))
	if readErr != nil || len(data) > gcsMaxControlBody || xml.Unmarshal(data, &result) != nil || !validUploadETag(result.ETag) || result.LastModified.IsZero() {
		return CopyObjectResult{}, ErrUnavailable
	}
	ack, err := p.verifyWriteAcknowledgment(response.Header)
	if err != nil || ack.ETag != result.ETag {
		return CopyObjectResult{}, ErrUnavailable
	}
	out := CopyObjectResult{ETag: result.ETag, LastModified: result.LastModified, ProviderVersionID: ack.ProviderVersionID}
	if encryption != nil {
		if err := beforeEncryptionKeyRequest(ctx); err != nil {
			return CopyObjectResult{}, err
		}
		proof, err := p.ConfirmEncryptedObject(ctx, destinationBucket, r.DestinationKey, receipt, source.SizeBytes, *encryption)
		if err != nil || proof.ProviderVersionID != ack.ProviderVersionID {
			return CopyObjectResult{}, ErrUnavailable
		}
		out.Encryption = proof.Encryption
	}
	return out, nil
}

// Multipart copy uses a bounded stream because GCS's XML API has no native
// UploadPartCopy. BeforeSourceRead must meter this additional read before any
// data is requested. It is supplied by the gateway, never customer input.
type gcsCopyReadRecorderKey struct{}

func WithMultipartCopyReadRecorder(ctx context.Context, before func(context.Context, int64) error) context.Context {
	return context.WithValue(ctx, gcsCopyReadRecorderKey{}, before)
}

func (p *GCS) CopyMultipartPart(ctx context.Context, bucket string, r MultipartPartCopyRequest, source CopySourceSnapshot) (CopyObjectResult, error) {
	return p.copyGCSPart(ctx, bucket, bucket, r, source)
}
func (p *GCS) CopyDateConditionalMultipartPart(ctx context.Context, bucket string, r MultipartPartCopyRequest, source CopySourceSnapshot) (CopyObjectResult, error) {
	return p.copyGCSPart(ctx, bucket, bucket, r, source)
}
func (p *GCS) CopyCrossBucketMultipartPart(ctx context.Context, sourceBucket, destinationBucket string, r MultipartPartCopyRequest, source CopySourceSnapshot) (CopyObjectResult, error) {
	return p.copyGCSPart(ctx, sourceBucket, destinationBucket, r, source)
}

func (p *GCS) copyGCSPart(ctx context.Context, sourceBucket, destinationBucket string, r MultipartPartCopyRequest, source CopySourceSnapshot) (CopyObjectResult, error) {
	size, err := MultipartCopySize(source, r.Range)
	if err != nil || ctx.Err() != nil || !ValidKey(r.SourceKey) || !ValidKey(r.Key) || sourceBucket == "" || destinationBucket == "" || r.PartNumber < 1 || r.PartNumber > api.MaxMultipartParts || r.ProviderUploadID == "" || r.SourceProviderVersionID != "" && r.SourceProviderVersionID != source.ProviderVersionID {
		return CopyObjectResult{}, errors.Join(ErrWriteRejected, ErrInvalid)
	}
	if err = checkGCSCopySource(source, r.Conditions, api.MaxObjectUploadBytes); err != nil {
		return CopyObjectResult{}, errors.Join(ErrWriteRejected, err)
	}
	before, ok := ctx.Value(gcsCopyReadRecorderKey{}).(func(context.Context, int64) error)
	if !ok || before == nil {
		return CopyObjectResult{}, errors.Join(ErrWriteRejected, ErrConfiguration)
	}
	if err = before(ctx, size); err != nil {
		return CopyObjectResult{}, errors.Join(ErrWriteRejected, err)
	}
	headers := http.Header{"x-goog-if-metageneration-match": {source.ProviderMetadataVersion}, "Accept-Encoding": {"gzip"}}
	if r.Range != nil {
		headers.Set("Range", r.Range.String())
	}
	read, err := p.gcsStreamRequest(ctx, http.MethodGet, sourceBucket, r.SourceKey, url.Values{"generation": {source.ProviderVersionID}}, headers, nil, 0)
	if err != nil {
		return CopyObjectResult{}, errors.Join(ErrWriteRejected, err)
	}
	defer func() { _ = read.Body.Close() }()
	if _, err := ProviderReadEncryption(p, p.encryption, "", read.Header); err != nil {
		return CopyObjectResult{}, errors.Join(ErrWriteRejected, err)
	}
	identity, err := p.verifyWriteAcknowledgment(read.Header)
	if err != nil || read.Uncompressed || read.ContentLength != size || identity.ProviderVersionID != source.ProviderVersionID || identity.ETag != source.ETag || r.Range != nil && read.StatusCode != http.StatusPartialContent {
		return CopyObjectResult{}, errors.Join(ErrWriteRejected, ErrUnavailable)
	}
	if r.Range != nil && read.Header.Get("Content-Range") != "bytes "+strconv.FormatInt(r.Range.First, 10)+"-"+strconv.FormatInt(r.Range.Last, 10)+"/"+strconv.FormatInt(source.SizeBytes, 10) || r.Range == nil && read.StatusCode != http.StatusOK {
		return CopyObjectResult{}, errors.Join(ErrWriteRejected, ErrUnavailable)
	}
	part, err := p.gcsStreamRequest(ctx, http.MethodPut, destinationBucket, r.Key, url.Values{"uploadId": {r.ProviderUploadID}, "partNumber": {strconv.Itoa(int(r.PartNumber))}}, nil, read.Body, size)
	if err != nil {
		return CopyObjectResult{}, err
	}
	defer func() { _ = part.Body.Close() }()
	etags := encryptionHeaderValues(part.Header, "ETag")
	if len(etags) != 1 || !validUploadETag(etags[0]) {
		return CopyObjectResult{}, ErrUnavailable
	}
	return CopyObjectResult{ETag: etags[0], LastModified: p.now()}, nil
}
