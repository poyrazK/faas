package state

import (
	"context"
	"time"
)

const (
	ObjectS3CredentialStatusActive  = "active"
	ObjectS3CredentialStatusRevoked = "revoked"
)

// ObjectS3Credential contains the recoverable signing material only in its
// sealed form. SecretSealed must never be projected into an API response or
// log. A credential is deliberately bound to one logical bucket so duplicate
// bucket names in different apps cannot become ambiguous at s3.gregale.dev.
type ObjectS3Credential struct {
	ID           string
	AccountID    string
	BucketID     string
	AccessKeyID  string
	SecretSealed []byte
	KID          string
	Label        string
	Permission   string
	Status       string
	CreatedAt    time.Time
	LastUsedAt   *time.Time
	RevokedAt    *time.Time
}

// ObjectS3CredentialStore is kept separate from Store so unrelated daemon
// and test implementations do not need to understand the S3 data plane.
type ObjectS3CredentialStore interface {
	CreateObjectS3Credential(context.Context, ObjectS3Credential, int) (ObjectS3Credential, error)
	ListObjectS3Credentials(context.Context, string, string) ([]ObjectS3Credential, error)
	RevokeObjectS3Credential(context.Context, string, string, string) error
	ResolveObjectS3Credential(context.Context, string) (ObjectS3Credential, ObjectBucket, error)
	TouchObjectS3Credential(context.Context, string, time.Time) error
}

// ObjectS3CredentialRekeyStore is the maintenance-only surface used by the
// S3 gateway before it starts accepting traffic. The compare-and-swap reseal
// prevents concurrent gateway starts or revocation from restoring stale data.
type ObjectS3CredentialRekeyStore interface {
	ListObjectS3CredentialsForRekey(context.Context, int, string) ([]ObjectS3Credential, error)
	ResealObjectS3Credential(context.Context, string, string, string, []byte) error
}

func validObjectS3Credential(c ObjectS3Credential) bool {
	if c.ID == "" || c.AccountID == "" || c.BucketID == "" || c.AccessKeyID == "" || len(c.SecretSealed) == 0 || c.KID == "" || len(c.Label) < 1 || len(c.Label) > 64 {
		return false
	}
	if c.Status != ObjectS3CredentialStatusActive {
		return false
	}
	return validObjectBucketPermission(c.Permission)
}
