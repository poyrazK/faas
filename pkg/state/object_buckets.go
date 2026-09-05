package state

import (
	"context"
	"time"
)

// ObjectBucket is a durable placement record, not an upstream S3 bucket name
// supplied by a customer. BackendID/Fingerprint and PhysicalName never change
// during ordinary CRUD; migration must explicitly copy and verify the data.
type ObjectBucket struct {
	ID                 string
	AccountID          string
	AppID              string
	Name               string
	Scope              string
	Region             string
	BackendID          string
	BackendFingerprint string
	PhysicalName       string
	State              string
	CreatedAt          time.Time
	UpdatedAt          time.Time
	LeaseToken         string
	LeaseUntil         time.Time
}

// ObjectBucketStore is separate from Store so test doubles unrelated to
// customer storage remain compatible. PgStore and MemStore implement both.
type ObjectBucketStore interface {
	ReserveObjectBucket(context.Context, ObjectBucket, int) (ObjectBucket, error)
	ListObjectBuckets(context.Context, string, string) ([]ObjectBucket, error)
	GetObjectBucket(context.Context, string, string, string) (ObjectBucket, error)
	ClaimObjectBucket(context.Context, string, string, string, string, string) (ObjectBucket, error)
	FinishObjectBucket(context.Context, string, string, string) error
}

const ObjectBucketLeaseDuration = 2 * time.Minute
