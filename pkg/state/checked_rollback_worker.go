package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ErrWorkerRollbackNotReady leaves the serving release untouched while a
// checked rollback waits for a worker running the current configuration.
var ErrWorkerRollbackNotReady = errors.New("state: checked rollback needs a current running worker")

func rollbackWorkerPreparedAt(d Deployment, r api.RollbackOperation) (time.Time, error) {
	var stages StageState
	if len(d.StageState) != 0 {
		if err := json.Unmarshal(d.StageState, &stages); err != nil {
			return time.Time{}, err
		}
	}
	if stages.Current == StageSnapshotPrepare && stages.CurrentStartedAt != nil {
		return *stages.CurrentStartedAt, nil
	}
	for index := len(stages.History) - 1; index >= 0; index-- {
		stage := stages.History[index]
		if stage.Name == StageSnapshotPrepare && stage.StartedAt != nil {
			return *stage.StartedAt, nil
		}
	}
	return r.CreatedAt, nil // Legacy receipts without stage history.
}

func (m *MemStore) checkRollbackWorkerLocked(d Deployment, r api.RollbackOperation) error {
	preparedAt, err := rollbackWorkerPreparedAt(d, r)
	if err != nil {
		return err
	}
	values, err := m.runtimeAppValuesLocked(m.apps[d.AppID].AccountID, d.AppID, d.ID)
	if err != nil {
		return err
	}
	current, err := NewRuntimeAppConfigFence(values)
	if err != nil {
		return err
	}
	for _, i := range m.instances {
		if i.AppID != d.AppID || i.DeploymentID != d.ID || i.Mode != string(InstanceModeWorker) ||
			i.State != string(StateRunning) || i.StartedAt.Before(preparedAt) {
			continue
		}
		proof, ok := m.runtimeInstanceConfigProofs[i.ID]
		if ok && proof.WakeID == i.WakeID && proof.NodeID == i.NodeID && proof.Fence == current {
			return nil
		}
	}
	return ErrWorkerRollbackNotReady
}

func checkRollbackWorkerTx(ctx context.Context, tx pgx.Tx, d Deployment, r api.RollbackOperation) error {
	q := sqlc.New()
	preparedAt, err := rollbackWorkerPreparedAt(d, r)
	if err != nil {
		return err
	}
	// Hold the recipients through cutover so a concurrent stop cannot consume
	// the readiness checked here. The caller already holds the app/deployment.
	rows, err := q.CheckedRollbackWorkerInstances(ctx, tx, sqlc.CheckedRollbackWorkerInstancesParams{
		DeploymentID: mustPgUUID(d.ID), PreparedAt: gitOpsTime(preparedAt),
	})
	if err != nil {
		return fmt.Errorf("checked rollback workers: %w", err)
	}
	if len(rows) == 0 {
		return ErrWorkerRollbackNotReady
	}
	values, err := runtimeAppValuesDB(ctx, tx, rows[0].AccountID, d.AppID, d.ID)
	if err != nil {
		return err
	}
	current, err := NewRuntimeAppConfigFence(values)
	if err != nil {
		return err
	}
	for _, i := range rows {
		proof, err := q.ReadRuntimeInstanceConfigProof(ctx, tx, sqlc.ReadRuntimeInstanceConfigProofParams{
			AccountID: mustPgUUID(i.AccountID), AppID: mustPgUUID(d.AppID), InstanceID: mustPgUUID(i.ID),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		stored := RuntimeAppConfigFence{SecretFence: RuntimeAppSecretFence{DeploymentID: proof.DeploymentID, EnvironmentID: proof.EnvironmentID, Scope: proof.Scope, Fingerprint: proof.SecretFingerprint}, Fingerprint: proof.ConfigFingerprint}
		if stored == current {
			return nil
		}
	}
	return ErrWorkerRollbackNotReady
}
