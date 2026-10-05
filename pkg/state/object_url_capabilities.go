package state

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// ObjectURLCapability freezes the authority behind a branded SigV4 URL.
// The credential secret remains sealed. Ordinary PUT authority names one
// durable receipt; multipart PUT authority names one fixed session and part.
type ObjectURLCapability struct {
	Request   api.ObjectSignRequest
	APIKeyID  string
	ReceiptID string
	ExpiresAt time.Time
	Multipart *ObjectURLMultipartPart
}

// ObjectURLMultipartPart identifies an owned fixed-size session, never a
// native provider upload ID. Its layout and encryption live on that session.
type ObjectURLMultipartPart struct {
	UploadID   string `json:"upload_id"`
	PartNumber int32  `json:"part_number"`
}

type objectURLRequestEnvelope struct {
	api.ObjectSignRequest
	Multipart *ObjectURLMultipartPart `json:"multipart,omitempty"`
}

type ObjectMultipartURLCapabilityStore interface {
	IssueObjectMultipartURLCredential(context.Context, ObjectS3Credential, ObjectMultipartUpload, api.ObjectStoragePolicy) (ObjectS3Credential, error)
	BeginObjectURLMultipartPart(context.Context, string, string, api.ObjectStoragePolicy) error
}

type ObjectURLCapabilityStore interface {
	IssueObjectURLCredential(context.Context, ObjectS3Credential, ObjectUploadCompletion, api.ObjectStoragePolicy) (ObjectS3Credential, ObjectUploadCompletion, error)
	// DispatchObjectURLUpload meters the winning attempt at the same commit
	// boundary as its single-dispatch fence. Losing retries spend no request.
	DispatchObjectURLUpload(context.Context, string, string, string) (ObjectUploadCompletion, error)
}

func objectURLCapabilityLimitError(n int64) error {
	return &ObjectStorageLimitError{Kind: "active_url_capabilities", Limit: api.MaxObjectURLCapabilitiesPerBucket, Observed: n + 1, Cause: ErrConflict}
}

func (u *ObjectURLCapability) Clone() *ObjectURLCapability {
	if u == nil {
		return nil
	}
	out := *u
	if u.Multipart != nil {
		part := *u.Multipart
		out.Multipart = &part
	}
	out.Request.Metadata = maps.Clone(u.Request.Metadata)
	out.Request.Tags = maps.Clone(u.Request.Tags)
	if u.Request.SizeBytes != nil {
		size := *u.Request.SizeBytes
		out.Request.SizeBytes = &size
	}
	if u.Request.Encryption != nil {
		e := cloneEncryptionSelection(*u.Request.Encryption)
		out.Request.Encryption = &e
	}
	return &out
}

func validObjectURLRequest(r api.ObjectSignRequest) bool {
	if r.ExpiresIn < 1 || r.ExpiresIn > int64(api.MaxObjectSignedURLTTL/time.Second) || r.Key == "" || len(r.Key) > 1024 || !utf8.ValidString(r.Key) || strings.ContainsAny(r.Key, "\x00\r\n") {
		return false
	}
	for _, ch := range r.Key {
		if ch < 32 || ch == 127 {
			return false
		}
	}
	if r.Method != http.MethodPut {
		return (r.Method == http.MethodGet || r.Method == http.MethodHead) && r.SizeBytes == nil && r.Encryption == nil && r.ContentType == "" && r.CacheControl == "" && r.ContentDisposition == "" && r.ContentEncoding == "" && r.ContentLanguage == "" && len(r.Metadata) == 0 && len(r.Tags) == 0
	}
	if r.SizeBytes == nil || *r.SizeBytes < 0 || *r.SizeBytes > api.MaxObjectSinglePutBytes || r.Encryption != nil && (!r.Encryption.Valid() || r.Encryption.Empty()) {
		return false
	}
	for _, v := range []string{r.ContentType, r.CacheControl, r.ContentDisposition, r.ContentEncoding, r.ContentLanguage} {
		if !utf8.ValidString(v) || strings.ContainsAny(v, "\x00\r\n") {
			return false
		}
	}
	for _, entries := range []map[string]string{r.Metadata, r.Tags} {
		for k, v := range entries {
			if k == "" || !utf8.ValidString(k) || !utf8.ValidString(v) || strings.ContainsAny(k+v, "\x00\r\n") {
				return false
			}
		}
	}
	return true
}

func objectURLRequestJSON(u *ObjectURLCapability) ([]byte, error) {
	if u == nil || !validObjectURLRequest(u.Request) {
		return nil, ErrInvalidArgument
	}
	if u.Multipart != nil && !validObjectURLMultipartRequest(u) {
		return nil, ErrInvalidArgument
	}
	b, err := json.Marshal(objectURLRequestEnvelope{ObjectSignRequest: u.Request, Multipart: u.Multipart})
	// PostgreSQL's jsonb text includes field separators. Reserve their
	// worst-case whitespace too, so a memory-admitted descriptor also fits
	// the bounded SQL decoder after its canonical serialization.
	if err != nil || len(b)+2*bytes.Count(b, []byte("\":")) > api.MaxObjectURLRequestBytes {
		return nil, ErrInvalidArgument
	}
	return b, nil
}

func objectURLCapabilityFromJSON(b []byte, key, receipt string, expiry time.Time) (*ObjectURLCapability, error) {
	if len(b) == 0 {
		if key != "" || receipt != "" || !expiry.IsZero() {
			return nil, ErrConflict
		}
		return nil, nil
	}
	if len(b) > api.MaxObjectURLRequestBytes || !utf8.Valid(b) {
		return nil, ErrConflict
	}
	u := &ObjectURLCapability{APIKeyID: key, ReceiptID: receipt, ExpiresAt: expiry}
	envelope := objectURLRequestEnvelope{}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(&envelope); err != nil || !validObjectURLRequest(envelope.ObjectSignRequest) || expiry.IsZero() {
		return nil, ErrConflict
	}
	u.Request, u.Multipart = envelope.ObjectSignRequest, envelope.Multipart
	if err := d.Decode(new(any)); err != io.EOF {
		return nil, ErrConflict
	}
	if (u.Request.Method == http.MethodPut && u.Multipart == nil) != (receipt != "") {
		return nil, ErrConflict
	}
	if _, err := objectURLRequestJSON(u); err != nil {
		return nil, ErrConflict
	}
	return u, nil
}

func validObjectURLCredential(c ObjectS3Credential, receipt ObjectUploadCompletion, now time.Time) bool {
	if !validObjectURLCredentialIdentity(c, now) || c.URL.Multipart != nil {
		return false
	}
	u := c.URL
	if u.Request.Method == http.MethodPut {
		return c.Permission == ObjectBucketPermissionWrite && validTrackedGatewayUpload(receipt) && validObjectURLReceipt(c, receipt)
	}
	return c.Permission == ObjectBucketPermissionRead && u.ReceiptID == "" && receipt.ID == ""
}

func validObjectURLCredentialIdentity(c ObjectS3Credential, now time.Time) bool {
	u := c.URL
	plain := c
	plain.URL = nil
	if u == nil || !validObjectS3Credential(plain) || c.ManagedAppID != "" || c.RotationParentID != "" || !u.ExpiresAt.After(now) || u.ExpiresAt.After(now.Add(time.Duration(u.Request.ExpiresIn)*time.Second)) {
		return false
	}
	for _, id := range []string{c.ID, c.AccountID, c.BucketID} {
		if _, err := uuid.Parse(id); err != nil {
			return false
		}
	}
	if u.APIKeyID != "" {
		if _, err := uuid.Parse(u.APIKeyID); err != nil {
			return false
		}
	}
	if _, err := objectURLRequestJSON(u); err != nil {
		return false
	}
	return true
}

func validObjectURLMultipartRequest(u *ObjectURLCapability) bool {
	if u.Multipart == nil || u.ReceiptID != "" || u.Multipart.PartNumber < 1 || u.Multipart.PartNumber > api.MaxMultipartParts {
		return false
	}
	id, err := uuid.Parse(u.Multipart.UploadID)
	r := u.Request
	return err == nil && id != uuid.Nil && r.Method == http.MethodPut && r.SizeBytes != nil && *r.SizeBytes > 0 && r.ContentType == "application/octet-stream" && r.Encryption == nil && len(r.Metadata) == 0 && len(r.Tags) == 0 && r.CacheControl == "" && r.ContentDisposition == "" && r.ContentEncoding == "" && r.ContentLanguage == ""
}

func validObjectURLMultipartUpload(c ObjectS3Credential, u ObjectMultipartUpload, now time.Time) bool {
	if !validObjectURLCredentialIdentity(c, now) || c.Permission != ObjectBucketPermissionWrite || !validObjectURLMultipartRequest(c.URL) || c.AccountID != u.AccountID || c.BucketID != u.BucketID || c.URL.Multipart.UploadID != u.ID || c.URL.Request.Key != u.Key || u.PartCount < 1 || u.PartSizeBytes < 1 || u.SizeBytes < 1 || u.State != ObjectMultipartActive || !u.ExpiresAt.After(now) || c.URL.ExpiresAt.After(u.ExpiresAt) || u.ProviderUploadID == "" {
		return false
	}
	part := c.URL.Multipart.PartNumber
	return part <= u.PartCount && *c.URL.Request.SizeBytes == min(u.PartSizeBytes, u.SizeBytes-int64(part-1)*u.PartSizeBytes)
}

func validObjectURLReceipt(c ObjectS3Credential, receipt ObjectUploadCompletion) bool {
	u := c.URL
	if u == nil || u.Request.Method != http.MethodPut || u.Request.SizeBytes == nil || u.ReceiptID != receipt.ID || c.ID != receipt.SubjectID || c.AccountID != receipt.AccountID || c.BucketID != receipt.BucketID || u.Request.Key != receipt.Key || *u.Request.SizeBytes != receipt.Bytes || u.Request.ContentType != receipt.ContentType || receipt.Origin != "" && receipt.Origin != "gateway" || !validGatewayReceiptShape(receipt) {
		return false
	}
	e := api.ObjectEncryption{}
	if u.Request.Encryption != nil {
		e = cloneEncryptionSelection(*u.Request.Encryption)
	}
	return equalEncryptionSelection(receipt.Encryption.Selection, e)
}
