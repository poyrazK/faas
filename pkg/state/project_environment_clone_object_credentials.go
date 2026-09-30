package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"time"

	"github.com/google/uuid"
)

// This private preparation receipt contains sealed material. It is never an
// API response. Credentials and their prepared runtime envelopes commit with
// the receipt, so retries cannot regenerate a different target identity.
type ProjectEnvironmentCloneObjectCredentialPreparation struct {
	OperationID, AppID, SourceBucketID, SourceCredentialID string
	Credential                                             ObjectS3Credential
	Secrets                                                []AppSecret
	Hash                                                   string `json:"-"`
}

type ProjectEnvironmentCloneObjectCredentialRequest struct {
	AppID, SourceBucketID, SourceCredentialID string
	Target                                    ObjectS3ComputeBindingCreateRequest
}

type ProjectEnvironmentCloneObjectCredentialStore interface {
	PrepareProjectEnvironmentCloneObjectCredential(context.Context, ProjectEnvironmentCloneLease, ProjectEnvironmentCloneObjectCredentialRequest) (ProjectEnvironmentCloneObjectCredentialPreparation, error)
	ProjectEnvironmentCloneObjectCredentialForLease(context.Context, ProjectEnvironmentCloneLease, string) (ProjectEnvironmentCloneObjectCredentialPreparation, error)
}

func validCloneCredentialSourceID(id string) bool {
	parsed, err := uuid.Parse(id)
	return err == nil && parsed != uuid.Nil
}

func capturedCloneObjectCredential(views []ProjectEnvironmentCloneBindings, sourceID string) (string, ProjectEnvironmentCloneObjectBucket, ProjectEnvironmentCloneObjectCredential, error) {
	for _, view := range views {
		for _, bucket := range view.Buckets {
			for _, credential := range bucket.Credentials {
				if credential.ID == sourceID {
					return view.AppID, bucket, credential, nil
				}
			}
		}
	}
	return "", ProjectEnvironmentCloneObjectBucket{}, ProjectEnvironmentCloneObjectCredential{}, ErrNotFound
}

func validateCloneObjectCredentialRequest(op ProjectEnvironmentCloneOperation, views []ProjectEnvironmentCloneBindings, request ProjectEnvironmentCloneObjectCredentialRequest) error {
	c := request.Target.Credential
	for _, id := range []string{request.AppID, request.SourceBucketID, request.SourceCredentialID, c.ID, c.AccountID, c.BucketID} {
		if parsed, err := uuid.Parse(id); err != nil || parsed == uuid.Nil {
			return ErrInvalidArgument
		}
	}
	appID, sourceBucket, source, err := capturedCloneObjectCredential(views, request.SourceCredentialID)
	if err != nil {
		return err
	}
	if appID != request.AppID || sourceBucket.ID != request.SourceBucketID || c.ID == source.ID || c.AccountID != op.AccountID || c.BucketID == sourceBucket.ID ||
		c.Label != source.Label || c.Permission != source.Permission || c.ManagedAppID != source.ManagedAppID || c.ManagedPrefix != source.ManagedPrefix ||
		c.RotationParentID != "" || c.RotationWakeID != "" || c.RotationStampedAt != nil || !validObjectS3Credential(c) || request.Target.MaxCredentialsPerBucket < 1 {
		return ErrConflict
	}
	if source.ManagedAppID == "" {
		if c.ManagedScope != "" || len(request.Target.Secrets) != 0 {
			return ErrConflict
		}
	} else if c.ManagedScope != op.TargetEnvironment || !validObjectS3ComputeBindingCreateRequest(request.Target) {
		return ErrConflict
	}
	for _, secret := range request.Target.Secrets {
		if secret.ManagedPostgresBindingID != "" || secret.ManagedCredentialRef != "" || secret.ManagedCredentialGeneration != 0 ||
			secret.SecretClass != "" && secret.SecretClass != SecretClassPersistent || secret.SecretVersion != 0 && secret.SecretVersion != 1 || len(secret.ValueHash) > 16 {
			return ErrConflict
		}
	}
	return nil
}

func validateCloneObjectCredentialCopy(op ProjectEnvironmentCloneOperation, sourceID, targetID string, manifest ProjectEnvironmentCloneObjectManifest) error {
	var resource ProjectEnvironmentCloneResource
	matches := 0
	for _, item := range op.Resources {
		if item.Kind == "object_storage" && item.SourceID == sourceID {
			resource, matches = item, matches+1
		}
	}
	if matches != 1 || resource.TargetID != targetID || resource.Status != "ready" || manifest.OperationID != op.ID || manifest.SourceBucketID != sourceID ||
		manifest.TargetBucketID != targetID || resource.SourceVersion != manifest.Hash || resource.CapturePoint != manifest.CapturedAt.UTC().Format(time.RFC3339Nano) {
		return ErrConflict
	}
	versions := make([]ProjectEnvironmentCloneObjectVersion, len(manifest.Objects))
	for i, checkpoint := range manifest.Objects {
		if checkpoint.CopiedAt == nil || checkpoint.TargetETag == "" || !validCloneObjectSHA256(checkpoint.VerifiedSHA256) {
			return ErrConflict
		}
		versions[i] = checkpoint.Source
	}
	hash, err := ProjectEnvironmentCloneObjectManifestHash(versions)
	if err != nil || hash != manifest.Hash {
		return ErrConflict
	}
	return nil
}

func normalizeCloneObjectCredentialPreparation(prepared ProjectEnvironmentCloneObjectCredentialPreparation) (ProjectEnvironmentCloneObjectCredentialPreparation, []byte, error) {
	prepared.Credential = cloneObjectS3Credential(prepared.Credential)
	prepared.Credential.CreatedAt, prepared.Credential.LastUsedAt, prepared.Credential.RevokedAt = time.Time{}, nil, nil
	prepared.Secrets = append([]AppSecret{}, prepared.Secrets...)
	for i, secret := range prepared.Secrets {
		class, version := secret.SecretClass, secret.SecretVersion
		if class == "" {
			class = SecretClassPersistent
		}
		if version == 0 {
			version = 1
		}
		prepared.Secrets[i] = AppSecret{AccountID: secret.AccountID, AppID: secret.AppID, Scope: secret.Scope, Key: secret.Key,
			Ciphertext: append([]byte(nil), secret.Ciphertext...), Kid: secret.Kid, ValueHash: secret.ValueHash, SecretClass: class, SecretVersion: version,
			ManagedObjectStorageCredentialID: secret.ManagedObjectStorageCredentialID}
	}
	sort.Slice(prepared.Secrets, func(i, j int) bool { return prepared.Secrets[i].Key < prepared.Secrets[j].Key })
	raw, err := json.Marshal(prepared)
	if err != nil {
		return prepared, nil, err
	}
	hash := sha256.Sum256(raw)
	prepared.Hash = hex.EncodeToString(hash[:])
	return prepared, raw, nil
}

func newCloneObjectCredentialPreparation(op ProjectEnvironmentCloneOperation, request ProjectEnvironmentCloneObjectCredentialRequest) (ProjectEnvironmentCloneObjectCredentialPreparation, []byte, error) {
	return normalizeCloneObjectCredentialPreparation(ProjectEnvironmentCloneObjectCredentialPreparation{
		OperationID: op.ID, AppID: request.AppID, SourceBucketID: request.SourceBucketID, SourceCredentialID: request.SourceCredentialID,
		Credential: request.Target.Credential, Secrets: request.Target.Secrets,
	})
}

var _ ProjectEnvironmentCloneObjectCredentialStore = (*MemStore)(nil)
var _ ProjectEnvironmentCloneObjectCredentialStore = (*PgStore)(nil)
