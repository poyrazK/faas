package objectstorage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

var _ HistoricalObjectWriteConfirmer = (*S3)(nil)

type s3HistoryCursor struct {
	Binding string `json:"b"`
	Key     string `json:"k"`
	Version string `json:"v"`
}

func historyBinding(bucket string, r ObjectHistoryProofRequest) string {
	identity := bucket + "\x00" + r.Key + "\x00" + r.Receipt + "\x00" + strconv.FormatInt(r.SizeBytes, 10)
	if r.MultipartSession {
		identity += "\x00multipart"
	}
	if r.Encryption != nil {
		identity += "\x00encryption\x00" + r.Encryption.Proof()
	}
	sum := sha256.Sum256([]byte(identity))
	return hex.EncodeToString(sum[:])
}

func validNativeVersionID(id string) bool {
	if id == "" || len(id) > api.ObjectProviderVersionIDMaxBytes || !utf8.ValidString(id) {
		return false
	}
	for _, c := range id {
		if c < 32 || c == 127 {
			return false
		}
	}
	return true
}

func decodeHistoryCursor(bucket string, r ObjectHistoryProofRequest) (s3HistoryCursor, error) {
	c := s3HistoryCursor{Binding: historyBinding(bucket, r)}
	if r.Cursor == "" {
		return c, nil
	}
	if len(r.Cursor) > api.ObjectUploadHistoryCursorMaxBytes {
		return c, ErrInvalid
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(r.Cursor)
	if err != nil {
		return c, ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&c) != nil || d.Decode(new(any)) != io.EOF || c.Binding != historyBinding(bucket, r) || !ValidKey(c.Key) || !strings.HasPrefix(c.Key, r.Key) || c.Version != "" && !validNativeVersionID(c.Version) {
		return c, ErrInvalid
	}
	return c, nil
}

// ConfirmTrackedObjectHistory never creates or deletes versions. It lists one
// page and probes only immutable versions of the exact key, not prefix siblings
// or delete markers. Missing history remains uncertain, including a full sweep.
func (p *S3) ConfirmTrackedObjectHistory(ctx context.Context, bucket string, r ObjectHistoryProofRequest) (ObjectHistoryProofPage, error) {
	page := ObjectHistoryProofPage{Cursor: r.Cursor}
	if r.Encryption != nil {
		if err := p.verifyEncryption(*r.Encryption); err != nil {
			return page, err
		}
	}
	maxBytes := api.MaxObjectSinglePutBytes
	if r.MultipartSession {
		maxBytes = api.MaxObjectUploadBytes
	}
	if _, err := uuid.Parse(r.Receipt); err != nil || !ValidKey(r.Key) || r.SizeBytes < 0 || r.SizeBytes > maxBytes || r.BeforeRequest == nil {
		return page, ErrInvalid
	}
	c, err := decodeHistoryCursor(bucket, r)
	if err != nil {
		return page, err
	}
	if err = r.BeforeRequest(ctx); err != nil {
		return page, err
	}
	input := &s3.ListObjectVersionsInput{Bucket: aws.String(bucket), Prefix: aws.String(r.Key), MaxKeys: aws.Int32(api.ObjectUploadHistoryPageSize), EncodingType: types.EncodingTypeUrl}
	if c.Key != "" {
		input.KeyMarker = aws.String(c.Key)
	}
	if c.Version != "" {
		input.VersionIdMarker = aws.String(c.Version)
	}
	out, err := p.client.ListObjectVersions(ctx, input, func(o *s3.Options) { o.RetryMaxAttempts = 1 })
	if err != nil {
		return page, normalizeVersionHistoryError(err)
	}
	if out == nil || out.IsTruncated == nil {
		return page, ErrUnavailable
	}
	// Preserve the safety latch even if a later HEAD or cursor is malformed.
	page.VersionsObserved = len(out.DeleteMarkers) > 0
	for _, v := range out.Versions {
		id := aws.ToString(v.VersionId)
		page.VersionsObserved = page.VersionsObserved || id != "" && id != "null"
	}
	if len(out.Versions)+len(out.DeleteMarkers)+len(out.CommonPrefixes) > api.ObjectUploadHistoryPageSize {
		return page, ErrUnavailable
	}
	for _, v := range out.Versions {
		key, e := historyResponseKey(aws.ToString(v.Key), out.EncodingType)
		if e != nil {
			return page, e
		}
		id := aws.ToString(v.VersionId)
		if key != r.Key || id == "null" {
			continue
		}
		if !validNativeVersionID(id) || v.Size == nil || *v.Size < 0 {
			return page, ErrUnavailable
		}
		if *v.Size != r.SizeBytes {
			continue
		}
		if err = r.BeforeRequest(ctx); err != nil {
			return page, err
		}
		proof, e := p.confirmTrackedVersion(ctx, bucket, r, id)
		if e == nil {
			page.UploadResult = proof
			page.Cursor = ""
			return page, nil
		}
		if !errors.Is(e, ErrConflict) && !errors.Is(e, ErrNotFound) {
			return page, e
		}
	}
	page.Cursor, err = nextHistoryCursor(c, out, r.Key)
	if err != nil {
		page.Cursor = r.Cursor
		return page, err
	}
	return page, ErrConflict
}

func historyResponseKey(key string, encoding types.EncodingType) (string, error) {
	if encoding == types.EncodingTypeUrl {
		var err error
		key, err = url.PathUnescape(key)
		if err != nil {
			return "", ErrUnavailable
		}
	} else if encoding != "" {
		return "", ErrUnavailable
	}
	if !ValidKey(key) {
		return "", ErrUnavailable
	}
	return key, nil
}

func nextHistoryCursor(old s3HistoryCursor, out *s3.ListObjectVersionsOutput, prefix string) (string, error) {
	if !aws.ToBool(out.IsTruncated) {
		return "", nil
	}
	key, err := historyResponseKey(aws.ToString(out.NextKeyMarker), out.EncodingType)
	version := aws.ToString(out.NextVersionIdMarker)
	if err != nil || !strings.HasPrefix(key, prefix) || version != "" && !validNativeVersionID(version) || key == old.Key && version == old.Version {
		return "", ErrUnavailable
	}
	c := s3HistoryCursor{Binding: old.Binding, Key: key, Version: version}
	var raw bytes.Buffer
	encoder := json.NewEncoder(&raw)
	// Cursors are private base64 data; HTML escaping would inflate valid keys
	// containing '<', '>' or '&' beyond the cursor budget.
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(c); err != nil {
		return "", ErrUnavailable
	}
	encoded := base64.RawURLEncoding.EncodeToString(raw.Bytes())
	if len(encoded) > api.ObjectUploadHistoryCursorMaxBytes {
		return "", ErrUnavailable
	}
	return encoded, nil
}

func (p *S3) confirmTrackedVersion(ctx context.Context, bucket string, r ObjectHistoryProofRequest, version string) (UploadResult, error) {
	out, err := p.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(bucket), Key: aws.String(r.Key), VersionId: aws.String(version)}, func(o *s3.Options) { o.RetryMaxAttempts = 1 })
	if err != nil {
		return UploadResult{}, normalizeVersionHistoryError(err)
	}
	if out == nil || out.ContentLength == nil || !validCopySnapshotVersion(out.ResultMetadata, aws.ToString(out.VersionId), version) || aws.ToBool(out.DeleteMarker) || !validUploadETag(aws.ToString(out.ETag)) {
		return UploadResult{}, ErrUnavailable
	}
	metadataKey := ReservedUploadReceiptMetadataKey
	if r.MultipartSession {
		metadataKey = ReservedMultipartSessionMetadataKey
	}
	if !validTrackedProofHeaders(out.ResultMetadata, metadataKey) {
		return UploadResult{}, ErrUnavailable
	}
	if *out.ContentLength != r.SizeBytes || out.Metadata[metadataKey] != r.Receipt {
		return UploadResult{}, ErrConflict
	}
	if !validEncryptionResponse(out.ResultMetadata, r.Encryption) || !validProtectedHead(ctx, out) || !validStoredEncryptionResponse(out.Metadata, out.ResultMetadata, r.Encryption) {
		return UploadResult{}, ErrUnavailable
	}
	return UploadResult{VerifiedProtection: capturedWriteProtection(ctx).Proof(), Encryption: publicObjectEncryption(r.Encryption), ETag: aws.ToString(out.ETag), ProviderVersionID: version}, nil
}

func normalizeVersionHistoryError(err error) error {
	var service smithy.APIError
	if errors.As(err, &service) {
		switch service.ErrorCode() {
		case "NotImplemented", "UnsupportedOperation":
			return ErrUnsupported
		case "NoSuchVersion":
			return ErrNotFound
		}
	}
	return normalize(err)
}
