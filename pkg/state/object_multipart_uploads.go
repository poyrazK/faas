package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

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
