package state

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

const (
	ObjectUploadPrepared   = "prepared"
	ObjectUploadDispatched = "dispatched"
	ObjectUploadSettled    = "settled"
)

// ObjectTrackedUploadStore owns the single provider attempt and its capacity
// reservation. Begin returns created=false for a replay, without spending quota.
// Dispatch must succeed before contacting the provider. Recovery never reuploads
// a body, and only a positive provider receipt can settle a dispatched intent.
type ObjectTrackedUploadStore interface {
	GetObjectUploadReceipt(context.Context, string, string, string, string, string) (ObjectUploadCompletion, error)
	BeginTrackedObjectUpload(context.Context, ObjectUploadCompletion, api.ObjectStoragePolicy) (ObjectUploadCompletion, bool, error)
	DispatchTrackedObjectUpload(context.Context, string, string, string) (ObjectUploadCompletion, error)
	FinishTrackedObjectUpload(context.Context, ObjectUploadCompletion) (ObjectUploadCompletion, error)
	DueTrackedObjectUploads(context.Context, int32) ([]ObjectUploadCompletion, error)
	ClaimTrackedObjectUploadRecovery(context.Context, string, string, string, string) (ObjectUploadCompletion, error)
	FinishTrackedObjectUploadRecovery(context.Context, ObjectUploadCompletion) (ObjectUploadCompletion, error)
	RetryTrackedObjectUploadRecovery(context.Context, ObjectUploadCompletion, string) error
}

func validTrackedObjectUpload(c ObjectUploadCompletion) bool {
	for _, id := range []string{c.ID, c.RouteID, c.AccountID, c.AppID, c.BucketID} {
		if _, err := uuid.Parse(id); err != nil {
			return false
		}
	}
	return c.Status == "pending" && c.Bytes >= 0 && c.Bytes <= api.MaxObjectSinglePutBytes && c.Key != "" && len(c.Key) <= 1024 && c.SubjectID != "" && len(c.SubjectID) <= 128 && len(c.IdempotencyKey) <= 128 && len(c.RequestFingerprint) <= 64 && (c.IdempotencyKey == "" || c.RequestFingerprint != "")
}
func validTrackedUploadFinish(c ObjectUploadCompletion) bool {
	if c.Status == "completed" {
		return validObjectUploadETag(c.ETag) && c.ErrorCode == ""
	}
	if c.Status != "failed" || c.ETag != "" {
		return false
	}
	switch c.ErrorCode {
	case "preparation_expired", "usage_unavailable", "dispatch_failed", "provider_write_rejected":
		return true
	default:
		return false
	}
}
func validObjectUploadETag(etag string) bool {
	return strings.TrimSpace(etag) != "" && len(etag) <= api.MaxObjectWriteETagBytes && !strings.ContainsAny(etag, "\r\n")
}
func validTrackedUploadRecovery(c ObjectUploadCompletion, now time.Time) bool {
	return c.WritePhase == ObjectUploadDispatched && c.RecoveryToken != "" && c.RecoveryLeaseUntil.After(now)
}
func validTrackedUploadRetry(code string) bool {
	switch code {
	case "provider_write_uncertain", "configuration":
		return true
	default:
		return false
	}
}
