package state

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
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
	// ManagedAppID, ManagedScope and ManagedPrefix are set only for a
	// compute binding. Empty values preserve the legacy customer-created
	// credential shape.
	ManagedAppID  string
	ManagedScope  string
	ManagedPrefix string
	// A rotation stage temporarily keeps the previous key valid while the
	// binding's current key is delivered to replacement workloads. Stages are
	// hidden from customer credential inventories.
	RotationParentID  string
	RotationWakeID    string
	RotationStampedAt *time.Time
}

// ObjectS3CredentialRotationRequest switches the binding's current key and
// its two sealed runtime secrets in one transaction. The previous key is
// staged as a hidden credential until the rolling refresh finishes.
type ObjectS3CredentialRotationRequest struct {
	AccountID, BucketID, BindingID, WakeID string
	AccessKeyID, KID                       string
	SecretSealed                           []byte
	Secrets                                []AppSecret
}

func validObjectS3CredentialRotationRequest(req ObjectS3CredentialRotationRequest) bool {
	for _, id := range []string{req.AccountID, req.BucketID, req.BindingID, req.WakeID} {
		if _, err := uuid.Parse(id); err != nil {
			return false
		}
	}
	return req.AccessKeyID != "" && req.KID != "" && len(req.SecretSealed) > 0 && len(req.Secrets) == 2
}

func validateObjectS3CredentialRotationSecrets(req ObjectS3CredentialRotationRequest, parent ObjectS3Credential) error {
	if parent.ManagedAppID == "" || parent.ManagedPrefix == "" || parent.ManagedScope == "" {
		return ErrConflict
	}
	want := map[string]bool{
		parent.ManagedPrefix + "_ACCESS_KEY_ID":     true,
		parent.ManagedPrefix + "_SECRET_ACCESS_KEY": true,
	}
	for _, secret := range req.Secrets {
		if !want[secret.Key] || secret.AccountID != req.AccountID || secret.AppID != parent.ManagedAppID ||
			secret.Scope != parent.ManagedScope || secret.ManagedObjectStorageCredentialID != req.BindingID ||
			len(secret.Ciphertext) == 0 || strings.TrimSpace(secret.Kid) == "" || secret.ValueHash == "" {
			return ErrInvalidArgument
		}
		delete(want, secret.Key)
	}
	if len(want) != 0 {
		return ErrInvalidArgument
	}
	return nil
}

type ObjectS3CredentialRotationStore interface {
	StageObjectS3CredentialRotation(context.Context, ObjectS3CredentialRotationRequest) (ObjectS3Credential, error)
	PendingObjectS3CredentialRotation(context.Context, string, string, string) (string, error)
	StampObjectS3CredentialRotation(context.Context, string, string) error
	FinalizeObjectS3CredentialRotationsForApp(context.Context, string, string) error
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

// ObjectS3CredentialBindingStore is the lifecycle surface used by the
// compute-binding control plane. It is deliberately separate from the data
// plane interface so gateway fakes and integrations only need the credential
// operations they actually use.
type ObjectS3CredentialBindingStore interface {
	ObjectS3CredentialStore
	ObjectS3CredentialRotationStore
	GetObjectS3Credential(context.Context, string, string, string) (ObjectS3Credential, error)
}

func validObjectS3Credential(c ObjectS3Credential) bool {
	if c.ID == "" || c.AccountID == "" || c.BucketID == "" || c.AccessKeyID == "" || len(c.SecretSealed) == 0 || c.KID == "" || len(c.Label) < 1 || len(c.Label) > 64 {
		return false
	}
	if c.Status != ObjectS3CredentialStatusActive {
		return false
	}
	if c.ManagedAppID == "" && (c.ManagedScope != "" || c.ManagedPrefix != "") {
		return false
	}
	if c.ManagedAppID != "" && (c.ManagedScope == "" || c.ManagedPrefix == "") {
		return false
	}
	return validObjectBucketPermission(c.Permission)
}
