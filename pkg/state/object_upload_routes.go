package state

import (
	"context"
	"time"
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
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// ObjectUploadCompletion is an append-only receipt for an edge upload. It is
// safe to return a projection of this record to the caller without exposing
// provider placement or credentials.
type ObjectUploadCompletion struct {
	ID          string
	RouteID     string
	AccountID   string
	AppID       string
	BucketID    string
	SubjectID   string
	Key         string
	Bytes       int64
	ContentType string
	ETag        string
	Status      string
	ErrorCode   string
	RequestID   string
	CreatedAt   time.Time
}

type ObjectUploadRouteStore interface {
	ListObjectUploadRoutes(context.Context, string, string) ([]ObjectUploadRoute, error)
	GetObjectUploadRoute(context.Context, string, string, string) (ObjectUploadRoute, error)
	UpsertObjectUploadRoute(context.Context, ObjectUploadRoute) (ObjectUploadRoute, error)
	DeleteObjectUploadRoute(context.Context, string, string, string) error
	RecordObjectUploadCompletion(context.Context, ObjectUploadCompletion) (ObjectUploadCompletion, error)
}
