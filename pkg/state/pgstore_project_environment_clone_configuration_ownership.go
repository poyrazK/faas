package state

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// This closed list covers the rows copied by leased materialization. It is not
// a whole-stage deletion inventory or authority to remove unrelated resources.
func cloneConfigurationOwnershipManifest(raw []byte) (map[string]string, error) {
	tables := []string{
		"app_environment_secret_ref_suppressions", "app_environment_secret_refs", "app_envs", "app_secrets",
		"feature_flag_versions", "project_environment_config_versions", "project_environment_edge_policies",
		"project_environment_route_policies", "project_environment_workload_heads", "project_environment_workload_specs", "project_environments",
	}

	var manifest map[string]string
	if len(raw) > api.CloneConfigurationOwnershipMaxBytes || json.Unmarshal(raw, &manifest) != nil || len(manifest) != len(tables) {
		return nil, ErrConflict
	}
	for _, table := range tables {
		if !validCloneObjectSHA256(manifest[table]) {
			return nil, ErrConflict
		}
	}
	return manifest, nil
}

func requireCloneConfigurationOwnershipScopeTx(ctx context.Context, tx pgx.Tx, clone ProjectEnvironmentClone) error {
	foreign, err := sqlc.New().CloneConfigurationOwnershipScopeIsForeign(ctx, tx, sqlc.CloneConfigurationOwnershipScopeIsForeignParams{ProjectID: mustPgUUID(clone.ProjectID), Environment: clone.TargetSlug, ManagedPrepared: clone.ManagedBindingsPrepared, OperationID: mustPgUUID(clone.CloneOperationID)})
	if err != nil {
		return mapErr(err)
	}
	if !foreign.Valid || foreign.Bool {
		return ErrConflict
	}
	return nil
}

func saveCloneConfigurationOwnershipTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, environmentID string) error {
	q := sqlc.New()
	raw, err := q.ReadCloneConfigurationOwnershipSnapshot(ctx, tx, sqlc.ReadCloneConfigurationOwnershipSnapshotParams{ProjectID: mustPgUUID(op.ProjectID), Environment: op.TargetEnvironment, EnvironmentID: mustPgUUID(environmentID)})
	if err != nil {
		return mapErr(err)
	}
	manifest, err := cloneConfigurationOwnershipManifest(raw)
	if err != nil {
		return err
	}
	// Only the original materialization transaction can save this receipt. Replay
	// never backfills old rows or adopts configuration found later by an observer.
	return mapErr(q.InsertCloneConfigurationOwnership(ctx, tx, sqlc.InsertCloneConfigurationOwnershipParams{OperationID: mustPgUUID(op.ID), EnvironmentID: mustPgUUID(environmentID), Manifest: raw, ManifestSha256: cloneCheckpointSHA(manifest)}))
}

func verifyCloneConfigurationOwnershipTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, environmentID string) (string, error) {
	q := sqlc.New()
	row, err := q.ReadCloneConfigurationOwnership(ctx, tx, mustPgUUID(op.ID))
	if errors.Is(mapErr(err), ErrNotFound) {
		return "", nil
	} // Legacy receipts gain no deletion authority.
	if err != nil {
		return "", mapErr(err)
	}
	manifest, err := cloneConfigurationOwnershipManifest(row.Manifest)
	if err != nil || pgUUIDString(row.OperationID) != op.ID || pgUUIDString(row.EnvironmentID) != environmentID || !row.CreatedAt.Valid || !checkpointObjectRetirementTime(row.CreatedAt.Time.UTC()) || row.CreatedAt.Time.After(time.Now()) || cloneCheckpointSHA(manifest) != row.ManifestSha256 {
		return "", ErrConflict
	}
	actual, err := q.ReadCloneConfigurationOwnershipSnapshot(ctx, tx, sqlc.ReadCloneConfigurationOwnershipSnapshotParams{ProjectID: mustPgUUID(op.ProjectID), Environment: op.TargetEnvironment, EnvironmentID: mustPgUUID(environmentID)})
	if err != nil {
		return "", mapErr(err)
	}
	current, err := cloneConfigurationOwnershipManifest(actual)
	if err != nil {
		return "", ErrConflict
	}
	ownership := cloneCheckpointSHA(struct {
		EnvironmentID, ManifestSHA256 string
		CreatedAt                     time.Time
	}{environmentID, row.ManifestSha256, row.CreatedAt.Time.UTC()})
	normalized, err := clonePostgresSecretWithdrawalManifestTx(ctx, tx, op, ownership, row.CreatedAt.Time, manifest)
	if err != nil {
		return "", err
	}
	normalized, err = cloneObjectSecretWithdrawalManifestTx(ctx, tx, op, ownership, row.CreatedAt.Time, normalized)
	if err != nil {
		return "", err
	}
	// A saved cleanup must authenticate its exact post-state even if an observer
	// restores the original rows; absence or equality alone is not authority.
	_, cleanupErr := q.ReadCloneConfigurationCleanup(ctx, tx, mustPgUUID(op.ID))
	if cleanupErr == nil {
		if err = verifyCloneConfigurationCleanupTx(ctx, tx, op, environmentID, ownership, manifest, current); err != nil {
			return "", err
		}
	} else if !errors.Is(mapErr(cleanupErr), ErrNotFound) {
		return "", mapErr(cleanupErr)
	} else if !reflect.DeepEqual(normalized, current) {
		return "", ErrConflict
	}
	return ownership, nil
}
