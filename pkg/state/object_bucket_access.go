package state

import (
	"context"
	"time"
)

const (
	ObjectBucketPermissionRead      = "read"
	ObjectBucketPermissionWrite     = "write"
	ObjectBucketPermissionReadWrite = "read_write"
)

type ObjectBucketAccessGrant struct {
	AccountID  string
	BucketID   string
	APIKeyID   string
	KeyLabel   string
	KeyStatus  string
	Permission string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// ObjectBucketAccessStore is separate from Store so unrelated test doubles
// remain source compatible. PgStore and MemStore implement both interfaces.
type ObjectBucketAccessStore interface {
	ListObjectBucketAccessGrants(context.Context, string, string) ([]ObjectBucketAccessGrant, error)
	SetObjectBucketAccessGrant(context.Context, string, string, string, string) (ObjectBucketAccessGrant, error)
	DeleteObjectBucketAccessGrant(context.Context, string, string, string) error
	ObjectBucketKeyCan(context.Context, string, string, string, string) (bool, error)
	ListObjectBucketsForKey(context.Context, string, string, string) ([]ObjectBucket, error)
}

func validObjectBucketPermission(permission string) bool {
	switch permission {
	case ObjectBucketPermissionRead, ObjectBucketPermissionWrite, ObjectBucketPermissionReadWrite:
		return true
	default:
		return false
	}
}
