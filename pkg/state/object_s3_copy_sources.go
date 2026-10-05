package state

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
)

// ObjectS3CopySource grants a destination credential read-only source authority
// for copy operations. ID changes when the prefix changes or a grant is recreated.
type ObjectS3CopySource struct {
	ID, AccountID, CredentialID, BucketID, SourceBucketID, Prefix string
	CreatedAt, UpdatedAt                                          time.Time
}

type ObjectS3CopySourceStore interface {
	GetObjectS3CopySourceBucket(context.Context, string, string) (ObjectBucket, error)
	ListObjectS3CopySources(context.Context, string, string, string) ([]ObjectS3CopySource, error)
	SetObjectS3CopySource(context.Context, string, string, string, string, string) (ObjectS3CopySource, error)
	DeleteObjectS3CopySource(context.Context, string, string, string, string) error
	ResolveObjectS3CopySource(context.Context, string, string, string, string) (ObjectS3CopySource, ObjectBucket, error)
}

func validObjectCopySourcePrefix(prefix string) bool {
	return len(prefix) <= api.MaxObjectCopySourcePrefixBytes && utf8.ValidString(prefix) && !strings.ContainsAny(prefix, "\x00\r\n")
}

func copySourceCredentialID(c ObjectS3Credential) string {
	if c.RotationParentID != "" {
		return c.RotationParentID
	}
	return c.ID
}

func validCopySourceCredential(c ObjectS3Credential) bool {
	return c.Status == ObjectS3CredentialStatusActive && c.URL == nil &&
		(c.Permission == ObjectBucketPermissionWrite || c.Permission == ObjectBucketPermissionReadWrite)
}

func compatibleCopySourceBuckets(destination, source ObjectBucket) bool {
	return destination.State == "ready" && source.State == "ready" &&
		destination.ID != source.ID && destination.AccountID == source.AccountID &&
		destination.BackendID != "" && destination.BackendID == source.BackendID &&
		destination.BackendFingerprint == source.BackendFingerprint
}
