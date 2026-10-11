package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
)

// Private provider intent and preparation, never public status DTOs. The
// database placement and data receipt are authenticated again on every read.
type ProjectEnvironmentClonePostgresBindingTarget struct {
	OperationID, SourceBindingID, ID, AccountID, AppID, DatabaseID  string
	Scope, EnvironmentKey, Access, BackendID, BackendFingerprint    string
	DatabaseProviderResourceID, SourceDatabaseVersion, CapturePoint string
	CredentialGeneration                                            int64
	State, ProviderIdentityID, CredentialRef                        string
}

type ProjectEnvironmentClonePostgresBindingPreparation struct {
	Binding ProjectEnvironmentClonePostgresBindingTarget
	Secret  AppSecret
	Hash    string `json:"-"`
}

type ProjectEnvironmentClonePostgresBindingRequest struct {
	SourceBindingID, TargetBindingID, ProviderIdentityID, CredentialRef string
	Secret                                                              AppSecret
	MaxSecretsPerApp                                                    int
}

type ProjectEnvironmentClonePostgresBindingStore interface {
	ReserveProjectEnvironmentClonePostgresBinding(context.Context, ProjectEnvironmentCloneLease, string) (ProjectEnvironmentClonePostgresBindingTarget, bool, error)
	PrepareProjectEnvironmentClonePostgresBinding(context.Context, ProjectEnvironmentCloneLease, ProjectEnvironmentClonePostgresBindingRequest) (ProjectEnvironmentClonePostgresBindingPreparation, error)
	ProjectEnvironmentClonePostgresBindingForLease(context.Context, ProjectEnvironmentCloneLease, string) (ProjectEnvironmentClonePostgresBindingTarget, *ProjectEnvironmentClonePostgresBindingPreparation, error)
}

func capturedClonePostgresBinding(views []ProjectEnvironmentCloneBindings, sourceID string) (string, ProjectEnvironmentClonePostgresBinding, error) {
	for _, view := range views {
		for _, binding := range view.Postgres {
			if binding.ID == sourceID {
				return view.AppID, binding, nil
			}
		}
	}
	return "", ProjectEnvironmentClonePostgresBinding{}, ErrNotFound
}

func clonePostgresBindingReservationHash(binding ProjectEnvironmentClonePostgresBindingTarget) (string, error) {
	binding.State, binding.ProviderIdentityID, binding.CredentialRef = "", "", ""
	raw, err := json.Marshal(binding)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:]), nil
}

func clonePostgresCredentialRef(bindingID string, generation int64) string {
	hash := sha256.Sum256([]byte(bindingID + "\x00" + strconv.FormatInt(generation, 10)))
	return "managed-postgres-" + hex.EncodeToString(hash[:])
}

func normalizeClonePostgresPreparation(prepared ProjectEnvironmentClonePostgresBindingPreparation) (ProjectEnvironmentClonePostgresBindingPreparation, []byte, error) {
	s := prepared.Secret
	class, version := s.SecretClass, s.SecretVersion
	if class == "" {
		class = SecretClassPersistent
	}
	if version == 0 {
		version = 1
	}
	prepared.Secret = AppSecret{AccountID: s.AccountID, AppID: s.AppID, Scope: s.Scope, Key: s.Key,
		Ciphertext: append([]byte(nil), s.Ciphertext...), Kid: s.Kid, ValueHash: s.ValueHash, SecretClass: class, SecretVersion: version,
		ManagedPostgresBindingID: s.ManagedPostgresBindingID, ManagedCredentialRef: s.ManagedCredentialRef, ManagedCredentialGeneration: s.ManagedCredentialGeneration}
	raw, err := json.Marshal(prepared)
	if err != nil {
		return prepared, nil, err
	}
	hash := sha256.Sum256(raw)
	prepared.Hash = hex.EncodeToString(hash[:])
	return prepared, raw, nil
}

func validateClonePostgresPreparationSecret(binding ProjectEnvironmentClonePostgresBindingTarget, secret AppSecret) error {
	if secret.AccountID != binding.AccountID || secret.AppID != binding.AppID || secret.Scope != binding.Scope || secret.Key != binding.EnvironmentKey ||
		secret.ManagedPostgresBindingID != binding.ID || secret.ManagedCredentialRef != clonePostgresCredentialRef(binding.ID, 1) || secret.ManagedCredentialGeneration != 1 ||
		secret.ManagedObjectStorageCredentialID != "" || len(secret.Ciphertext) == 0 || secret.Kid == "" || len(secret.ValueHash) > 16 ||
		secret.SecretClass != "" && secret.SecretClass != SecretClassPersistent || secret.SecretVersion != 0 && secret.SecretVersion != 1 {
		return ErrConflict
	}
	return nil
}

var _ ProjectEnvironmentClonePostgresBindingStore = (*PgStore)(nil)
