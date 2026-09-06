// Package objectstorage implements customer object storage independently of
// the image/snapshot backend in pkg/storage. The S3 data API is portable;
// customer credential issuance and provider billing APIs are not.
package objectstorage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"time"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
)

var (
	ErrUnavailable   = errors.New("object storage unavailable")
	ErrNotFound      = errors.New("object storage resource not found")
	ErrConflict      = errors.New("object storage resource conflict")
	ErrNotEmpty      = errors.New("bucket is not empty")
	ErrInvalid       = errors.New("invalid object storage request")
	ErrConfiguration = errors.New("object storage provider configuration requires attention")
)

// Provider owns data operations for a single immutable backend placement.
// Implementations must never return provider credentials in errors or results.
// Native S3 credentials deliberately are not part of this interface: adding
// a provider's IAM adapter must not change the portable bucket/data service.
type Provider interface {
	CreateBucket(context.Context, string) error
	DeleteBucket(context.Context, string) error
	ListObjects(context.Context, string, string, string, int32) (ObjectPage, error)
	DeleteObject(context.Context, string, string) error
	Presign(context.Context, string, SignRequest) (SignedRequest, error)
	EnsureMultipartUpload(context.Context, string, MultipartCreateRequest) (string, error)
	PresignMultipartPart(context.Context, string, MultipartPartRequest) (SignedRequest, error)
	CompleteMultipartUpload(context.Context, string, MultipartCompleteRequest) error
	AbortMultipartUpload(context.Context, string, MultipartAbortRequest) error
}

type Object struct {
	Key          string    `json:"key"`
	Size         int64     `json:"size_bytes"`
	LastModified time.Time `json:"last_modified"`
}

type ObjectPage struct {
	Items      []Object `json:"items"`
	NextCursor string   `json:"next_cursor,omitempty"`
}

// PUT sizes are signed, not merely advisory client-side limits.
type SignRequest api.ObjectSignRequest

func (r SignRequest) Validate(maxBytes int64) error {
	if !ValidKey(r.Key) || (r.Method != http.MethodGet && r.Method != http.MethodPut) || r.ExpiresIn < 0 || r.ExpiresIn > 900 {
		return ErrInvalid
	}
	if r.Method == http.MethodPut && (r.SizeBytes == nil || *r.SizeBytes < 0 || *r.SizeBytes > maxBytes) {
		return ErrInvalid
	}
	if r.Method == http.MethodGet && (r.SizeBytes != nil || r.ContentType != "") {
		return ErrInvalid
	}
	return ValidateContentType(r.ContentType)
}

func ValidKey(key string) bool {
	if len(key) == 0 || len(key) > 1024 || !utf8.ValidString(key) {
		return false
	}
	for _, c := range key {
		if c < 32 || c == 127 {
			return false
		}
	}
	return true
}

type SignedRequest = api.ObjectSignedRequest

// MultipartCreateRequest contains only provider-facing data. SessionID is
// persisted as object metadata so a completion response lost between S3 and
// Gregale can be verified without exposing the provider upload ID.
type MultipartCreateRequest struct {
	SessionID   string
	Key         string
	SizeBytes   int64
	ContentType string
}

type MultipartPartRequest struct {
	Key              string
	ProviderUploadID string
	PartNumber       int32
	SizeBytes        int64
	ExpiresIn        int64
}

type CompletedPart struct {
	PartNumber int32
	ETag       string
}

type MultipartCompleteRequest struct {
	SessionID        string
	Key              string
	ProviderUploadID string
	SizeBytes        int64
	Parts            []CompletedPart
}

type MultipartAbortRequest struct {
	Key              string
	ProviderUploadID string
}

func ValidateContentType(contentType string) error {
	if len(contentType) > 255 {
		return ErrInvalid
	}
	for _, c := range contentType {
		if c < 32 || c == 127 {
			return ErrInvalid
		}
	}
	return nil
}

type Backend struct {
	ID          string
	Region      string
	Fingerprint string
	Provider    Provider
}

func fingerprint(c BackendConfig) string {
	sum := sha256.Sum256([]byte(c.Endpoint + "\x00" + c.S3Region + "\x00" + c.Namespace + "\x00" + c.Driver + "\x00" + c.Region))
	return hex.EncodeToString(sum[:])
}
