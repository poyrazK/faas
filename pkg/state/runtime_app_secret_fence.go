package state

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// RuntimeAppSecretFence is trusted host context for an observation write.
// It never comes from a guest selector or an application acknowledgement.
type RuntimeAppSecretFence struct {
	DeploymentID  string
	EnvironmentID string
	Scope         string
	Fingerprint   string
}

func (f RuntimeAppSecretFence) empty() bool { return f == (RuntimeAppSecretFence{}) }

func validRuntimeAppSecretFence(f RuntimeAppSecretFence) bool {
	if f.empty() {
		return true // historical production callers; owned runtimes require a fence
	}
	dep, err := uuid.Parse(f.DeploymentID)
	if err != nil || dep == uuid.Nil || api.ValidateScope(f.Scope) != nil || !validSecretRevision(f.Fingerprint) {
		return false
	}
	if f.EnvironmentID != "" {
		env, err := uuid.Parse(f.EnvironmentID)
		return err == nil && env != uuid.Nil
	}
	return true
}

// NewRuntimeAppSecretFence fingerprints only owned sealed configuration, not
// mutable delivery observations or plaintext environment values. CreatedAt
// distinguishes deletion/recreation even if the same envelope is copied back.
func NewRuntimeAppSecretFence(snapshot RuntimeAppValuesSnapshot) (RuntimeAppSecretFence, error) {
	if snapshot.AccountID == "" || snapshot.AppID == "" || snapshot.DeploymentID == "" || api.ValidateScope(snapshot.Scope) != nil {
		return RuntimeAppSecretFence{}, ErrConflict
	}
	grants := snapshot.SecretGrants
	var err error
	grants.OverrideEnvSecrets, err = canonicalRuntimeSecretJSON(grants.OverrideEnvSecrets)
	if err != nil {
		return RuntimeAppSecretFence{}, ErrConflict
	}
	grants.Sidecars, err = canonicalRuntimeSecretJSON(grants.Sidecars)
	if err != nil {
		return RuntimeAppSecretFence{}, ErrConflict
	}
	if grants.SidecarReloadSignals == nil {
		grants.SidecarReloadSignals = map[string]string{}
	}
	rows := make([]runtimeSecretFingerprintRow, 0, len(snapshot.Secrets))
	keys := map[string]bool{}
	for _, secret := range snapshot.Secrets {
		if secret.AccountID != snapshot.AccountID || secret.AppID != snapshot.AppID || secret.Scope != snapshot.Scope || api.ValidateEnvKey(secret.Key) != nil || keys[secret.Key] {
			return RuntimeAppSecretFence{}, ErrConflict
		}
		keys[secret.Key] = true
		rows = append(rows, runtimeSecretFingerprintRow{Key: secret.Key, Ciphertext: secret.Ciphertext,
			Class: secret.SecretClass, Kid: secret.Kid, ValueHash: secret.ValueHash, SecretVersion: secret.SecretVersion,
			DeliveryVersion: secret.DeliveryVersion, CreatedAt: secret.CreatedAt.UTC(),
			ManagedPostgresBindingID: secret.ManagedPostgresBindingID, ManagedPostgresAccess: secret.ManagedPostgresAccess, ManagedCredentialRef: secret.ManagedCredentialRef,
			ManagedCredentialGeneration: secret.ManagedCredentialGeneration, ManagedObjectStorageCredentialID: secret.ManagedObjectStorageCredentialID})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Key < rows[j].Key })
	fence := RuntimeAppSecretFence{DeploymentID: snapshot.DeploymentID, EnvironmentID: snapshot.EnvironmentID, Scope: snapshot.Scope}
	raw, err := json.Marshal(struct {
		AccountID, AppID string
		Owner            RuntimeAppSecretFence
		Grants           RuntimeAppSecretGrants
		Secrets          []runtimeSecretFingerprintRow
	}{snapshot.AccountID, snapshot.AppID, fence, grants, rows})
	if err != nil {
		return RuntimeAppSecretFence{}, ErrConflict
	}
	digest := sha256.Sum256(raw)
	fence.Fingerprint = hex.EncodeToString(digest[:])
	return fence, nil
}

func canonicalRuntimeSecretJSON(raw []byte) (json.RawMessage, error) {
	if len(raw) == 0 {
		raw = []byte("null")
	}
	if !json.Valid(raw) {
		return nil, ErrConflict
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return json.Marshal(value)
}

type runtimeSecretFingerprintRow struct {
	Key, Class, Kid, ValueHash, ManagedPostgresAccess, ManagedPostgresBindingID, ManagedCredentialRef, ManagedObjectStorageCredentialID string
	Ciphertext                                                                                                                          []byte
	SecretVersion, DeliveryVersion, ManagedCredentialGeneration                                                                         int64
	CreatedAt                                                                                                                           time.Time
}
