package state

import (
	"bytes"
	"context"
	"encoding/json"
	"time"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ RuntimeAppValuesStore = (*PgStore)(nil)

func (s *PgStore) RuntimeAppValuesForDeployment(ctx context.Context, accountID, appID, deploymentID string) (RuntimeAppValuesSnapshot, error) {
	if err := validateRuntimeAppEnvIDs(accountID, appID, deploymentID); err != nil {
		return RuntimeAppValuesSnapshot{}, err
	}
	return runtimeAppValuesDB(ctx, s.pool, accountID, appID, deploymentID)
}

func runtimeAppValuesDB(ctx context.Context, db sqlc.DBTX, accountID, appID, deploymentID string) (RuntimeAppValuesSnapshot, error) {
	row, err := sqlc.New().ReadRuntimeAppValuesForDeployment(ctx, db, sqlc.ReadRuntimeAppValuesForDeploymentParams{
		AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID), DeploymentID: mustPgUUID(deploymentID),
	})
	if err != nil {
		return RuntimeAppValuesSnapshot{}, mapErr(err)
	}
	owner, err := decodeRuntimeAppEnvSnapshot(accountID, appID, deploymentID, row.Scope, row.EnvironmentID, row.Values)
	if err != nil {
		return RuntimeAppValuesSnapshot{}, err
	}
	result := RuntimeAppValuesSnapshot{RuntimeAppEnvSnapshot: owner, Secrets: []AppSecret{}, SecretGrants: RuntimeAppSecretGrants{
		OverrideEnvSecrets: row.OverrideEnvSecrets, Sidecars: row.Sidecars, ReloadSignal: row.ReloadSignal,
		SidecarReloadSignals: map[string]string{},
	}}
	if err := json.Unmarshal(row.ReloadSignals, &result.SecretGrants.SidecarReloadSignals); err != nil {
		return RuntimeAppValuesSnapshot{}, ErrConflict
	}
	var secrets []runtimeAppSealedSecretRow
	if err := json.Unmarshal(row.Secrets, &secrets); err != nil {
		return RuntimeAppValuesSnapshot{}, ErrConflict
	}
	for _, value := range secrets {
		result.Secrets = append(result.Secrets, AppSecret{AccountID: accountID, AppID: appID, Scope: owner.Scope, Key: value.Key,
			Ciphertext: value.Ciphertext, SecretClass: value.SecretClass, Kid: value.Kid, ValueHash: value.ValueHash,
			SecretVersion: value.SecretVersion, DeliveryVersion: value.DeliveryVersion, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
			ManagedPostgresBindingID: value.ManagedPostgresBindingID, ManagedPostgresAccess: value.ManagedPostgresAccess, ManagedCredentialRef: value.ManagedCredentialRef,
			ManagedCredentialGeneration: value.ManagedCredentialGeneration, ManagedObjectStorageCredentialID: value.ManagedObjectStorageCredentialID,
		})
	}
	var settings ProjectEnvironmentWorkloadSettings
	var artifact projectCloneArtifact
	result.SidecarLayers = []DeploymentSidecarLayer{}
	decoder := json.NewDecoder(bytes.NewReader(row.Settings))
	if row.SpecID != "" {
		decoder.DisallowUnknownFields()
	}
	if decoder.Decode(&settings) != nil || json.Unmarshal(row.Artifact, &artifact) != nil ||
		json.Unmarshal(row.Layers, &result.SidecarLayers) != nil {
		return RuntimeAppValuesSnapshot{}, ErrConflict
	}
	if row.SpecID != "" {
		hash, err := WorkloadSettingsHash(settings)
		if err != nil || hash != row.SettingsHash {
			return RuntimeAppValuesSnapshot{}, ErrConflict
		}
	}
	result.Configuration, err = runtimeAppConfiguration(settings, row.SpecID, artifact, result.SidecarLayers)
	if err != nil {
		return RuntimeAppValuesSnapshot{}, err
	}
	return result, nil
}

type runtimeAppSealedSecretRow struct {
	ManagedPostgresAccess            string    `json:"managed_postgres_access"`
	Key                              string    `json:"key"`
	Ciphertext                       []byte    `json:"ciphertext"`
	SecretClass                      string    `json:"secret_class"`
	Kid                              string    `json:"kid"`
	ValueHash                        string    `json:"value_hash"`
	SecretVersion                    int64     `json:"secret_version"`
	DeliveryVersion                  int64     `json:"delivery_version"`
	CreatedAt                        time.Time `json:"created_at"`
	UpdatedAt                        time.Time `json:"updated_at"`
	ManagedPostgresBindingID         string    `json:"managed_postgres_binding_id"`
	ManagedCredentialRef             string    `json:"managed_credential_ref"`
	ManagedCredentialGeneration      int64     `json:"managed_credential_generation"`
	ManagedObjectStorageCredentialID string    `json:"managed_object_storage_credential_id"`
}
