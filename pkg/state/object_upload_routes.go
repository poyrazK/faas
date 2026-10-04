package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// ObjectUploadRoute is the durable policy for an application-owned upload
// endpoint. The public edge resolves the route before waking an application,
// so the route is intentionally independent from deployments and runtime
// code.
type ObjectUploadRoute struct {
	ID                  string
	AccountID           string
	AppID               string
	Name                string
	BucketID            string
	KeyPrefix           string
	MaxBytes            int64
	AllowedContentTypes []string
	Enabled             bool
	Encryption          ObjectEncryptionSnapshot `json:"-"`
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// ObjectUploadCompletion is the durable receipt for an edge upload. An
// idempotent upload is first persisted as pending, then updated to completed
// or failed after the provider call. It is safe to return a projection of
// this record to the caller without exposing provider placement or
// credentials.
type ObjectUploadCompletion struct {
	ID                 string
	RouteID            string
	AccountID          string
	AppID              string
	BucketID           string
	SubjectID          string
	Key                string
	Bytes              int64
	ContentType        string
	ETag               string
	Status             string
	ErrorCode          string
	RequestID          string
	IdempotencyKey     string
	RequestFingerprint string
	CreatedAt          time.Time
	// Provider dispatch and recovery fields are internal; uploadResponse projects only the receipt.
	Origin                   string
	SourceKey                string
	SourceETag               string
	WritePhase               string
	RecoveryToken            string
	RecoveryLeaseUntil       time.Time
	RecoveryRetryAt          time.Time
	RecoveryCursor           string `json:"-"`
	RecoveryVersionsObserved bool   `json:"-"`
	VersionID                string `json:"-"`
	// ProviderVersionID is transient completion input, never a public payload.
	ProviderVersionID string                   `json:"-"`
	Encryption        ObjectEncryptionSnapshot `json:"-"`
	// VerifiedEncryption is transient provider proof; snapshots alone cannot settle a write.
	VerifiedEncryption api.ObjectEncryption `json:"-"`
}

type ObjectUploadRouteStore interface {
	ListObjectUploadRoutes(context.Context, string, string) ([]ObjectUploadRoute, error)
	GetObjectUploadRoute(context.Context, string, string, string) (ObjectUploadRoute, error)
	UpsertObjectUploadRoute(context.Context, ObjectUploadRoute) (ObjectUploadRoute, error)
	DeleteObjectUploadRoute(context.Context, string, string, string) error
	RecordObjectUploadCompletion(context.Context, ObjectUploadCompletion) (ObjectUploadCompletion, error)
	CreateObjectUploadIntent(context.Context, ObjectUploadCompletion) (ObjectUploadCompletion, error)
	GetObjectUploadIntent(context.Context, string, string, string) (ObjectUploadCompletion, error)
	UpdateObjectUploadCompletion(context.Context, ObjectUploadCompletion) (ObjectUploadCompletion, error)
}
