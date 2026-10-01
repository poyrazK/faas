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
	PartRevision                    int64
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

// ObjectMultipartCapacityStore reserves incomplete parts before provider writes.
// Reservations survive failures and are released only after confirmed completion. Abort reclamation requires provider reconciliation.
type ObjectMultipartCapacityStore interface {
	AdmitObjectMultipartPart(context.Context, string, string, string, int32, int64, int64, api.ObjectStoragePolicy) error
	AdmitObjectMultipartCompletion(context.Context, string, string, string, string, int64, api.ObjectStoragePolicy) error
}

// ObjectS3MultipartLister excludes terminal history and uses S3 key/upload markers.
type ObjectS3MultipartLister interface {
	ListObjectS3MultipartUploads(context.Context, string, string, string, string, string, string, int32) ([]ObjectMultipartUpload, error)
}

func validMultipartCapacityUpload(u ObjectMultipartUpload, account, bucket string, completion bool, now time.Time) bool {
	if u.AccountID != account || u.BucketID != bucket || u.PartCount != 0 {
		return false
	}
	if completion {
		return u.State == ObjectMultipartActive || u.State == ObjectMultipartCompleting
	}
	return u.State == ObjectMultipartActive && u.ExpiresAt.After(now)
}

func withoutMultipartReservation(s ObjectUsageSnapshot, bucket string, reserved int64) ObjectUsageSnapshot {
	for i := range s.Buckets {
		if s.Buckets[i].Bucket.ID == bucket {
			s.Buckets[i].MultipartBytes -= reserved
		}
	}
	return s
}

// ObjectMultipartTransferStore fences provider writes and atomically releases
// tracked reservations only after the caller verifies provider cleanup.
// Failed/uncertain writes retain a deadline; expiry alone never releases quota.
type ObjectMultipartTransferStore interface {
	BeginObjectMultipartPart(context.Context, string, string, string, string, int32, int64, int64, api.ObjectStoragePolicy) error
	SettleObjectMultipartPart(context.Context, string, string, int32, string) error
	PrepareObjectMultipartCompletion(context.Context, ObjectMultipartUpload, string, int64, []api.ObjectMultipartCompletedPart, api.ObjectStoragePolicy) (ObjectMultipartUpload, error)
	ObjectMultipartAbortReady(context.Context, string, string) (bool, error)
	FinishVerifiedObjectMultipartAbort(context.Context, string, string) error
}

type multipartPartTransfer struct {
	token       string
	unsafeUntil time.Time
	tracked     bool
}

func multipartTransferWindow() time.Duration {
	return api.ObjectTransferTimeout + time.Duration(api.ObjectMultipartPartURLTTLSeconds)*time.Second + api.ObjectMultipartCleanupGrace
}
