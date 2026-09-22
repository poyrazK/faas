package state

import (
	"context"
	"maps"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// ObjectMultipartMetadata is the durable, provider-neutral object metadata
// captured when a multipart upload is initiated. ContentType remains a
// top-level upload field for compatibility with the original control-plane
// multipart API.
type ObjectMultipartMetadata struct {
	CacheControl       string            `json:"cache_control,omitempty"`
	ContentDisposition string            `json:"content_disposition,omitempty"`
	ContentEncoding    string            `json:"content_encoding,omitempty"`
	ContentLanguage    string            `json:"content_language,omitempty"`
	UserMetadata       map[string]string `json:"metadata,omitempty"`
	Tags               map[string]string `json:"tags,omitempty"`
}

const ObjectMultipartLeaseDuration = 2 * time.Minute

const (
	ObjectMultipartInitiating = "initiating"
	ObjectMultipartActive     = "active"
	ObjectMultipartCompleting = "completing"
	ObjectMultipartAborting   = "aborting"
	ObjectMultipartCompleted  = "completed"
	ObjectMultipartAborted    = "aborted"
)

type ObjectMultipartUpload struct {
	ID, AccountID, AppID, BucketID  string
	Key                             string
	SizeBytes, PartSizeBytes        int64
	PartCount                       int32
	ContentType                     string
	Metadata                        ObjectMultipartMetadata
	ProviderUploadID                string
	Parts                           []api.ObjectMultipartCompletedPart
	State                           string
	ExpiresAt, CreatedAt, UpdatedAt time.Time
	LeaseToken                      string
	LeaseUntil, RetryAt             time.Time
	AttemptCount                    int32
	LastErrorCode                   string
}

type ObjectMultipartUploadStore interface {
	ReserveObjectMultipartUpload(context.Context, ObjectMultipartUpload, int) (ObjectMultipartUpload, error)
	ListObjectMultipartUploads(context.Context, string, string, string, int32, string) ([]ObjectMultipartUpload, string, error)
	GetObjectMultipartUpload(context.Context, string, string, string, string) (ObjectMultipartUpload, error)
	ClaimObjectMultipartUpload(context.Context, string, string, string, string, string, string, []api.ObjectMultipartCompletedPart, bool) (ObjectMultipartUpload, error)
	ActivateObjectMultipartUpload(context.Context, string, string, string) error
	SetObjectMultipartUploadSize(context.Context, string, string, int64) error
	FinishObjectMultipartUpload(context.Context, string, string, string) error
	RetryObjectMultipartUpload(context.Context, string, string, string, time.Duration) error
	DueObjectMultipartUploads(context.Context, int32) ([]ObjectMultipartUpload, error)
}

func validObjectMultipartOperation(operation string) bool {
	return operation == ObjectMultipartInitiating || operation == ObjectMultipartCompleting || operation == ObjectMultipartAborting
}

func validObjectMultipartRetry(code string, delay time.Duration) bool {
	return code != "" && len(code) <= 32 && delay >= time.Second && delay <= time.Hour
}

func cloneMultipartParts(parts []api.ObjectMultipartCompletedPart) []api.ObjectMultipartCompletedPart {
	return append([]api.ObjectMultipartCompletedPart(nil), parts...)
}

func cloneObjectMultipartMetadata(metadata ObjectMultipartMetadata) ObjectMultipartMetadata {
	metadata.UserMetadata = maps.Clone(metadata.UserMetadata)
	metadata.Tags = maps.Clone(metadata.Tags)
	return metadata
}

func equalObjectMultipartMetadata(a, b ObjectMultipartMetadata) bool {
	return a.CacheControl == b.CacheControl &&
		a.ContentDisposition == b.ContentDisposition &&
		a.ContentEncoding == b.ContentEncoding &&
		a.ContentLanguage == b.ContentLanguage &&
		maps.Equal(a.UserMetadata, b.UserMetadata) && maps.Equal(a.Tags, b.Tags)
}
