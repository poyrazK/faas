package state

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

const projectRollbackUndoArtifactWindow = 24 * time.Hour

var ErrProjectRollbackUndoArtifactsUnavailable = errors.New("state: could not retain the original revision for rollback undo")

type ProjectRollbackUndoArtifactStore interface {
	ProjectRollbackUndoArtifactHolds(context.Context, string) (map[string]api.ProjectRollbackUndoArtifacts, error)
}

func projectRollbackUndoArtifactKeys(layers []string) []string {
	keys := make([]string, 0, 2*len(layers))
	for _, key := range layers {
		keys = append(keys, key, "sigs/"+key+".sig")
	}
	slices.Sort(keys)
	return slices.Compact(keys)
}

func holdProjectRollbackUndoArtifactsTx(ctx context.Context, tx pgx.Tx, child api.RollbackOperation) error {
	var rootfs string
	if err := tx.QueryRow(ctx, `SELECT coalesce(rootfs_key, '') FROM deployments WHERE id = $1`, child.CurrentDeploymentID).Scan(&rootfs); err != nil {
		return mapErr(err)
	}
	if rootfs == "" {
		return ErrProjectRollbackUndoArtifactsUnavailable
	}
	layers, err := sqlc.New().ReadDeploymentLayerArtifactKeys(ctx, tx, mustPgUUID(child.CurrentDeploymentID))
	if err != nil {
		return mapErr(err)
	}
	keys := projectRollbackUndoArtifactKeys(layers)
	if err := requireLayerArtifactsRetainedTx(ctx, tx, keys); err != nil {
		return projectRollbackUndoArtifactsError(err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO project_rollback_undo_artifact_holds (operation_id, deployment_id, storage_keys)
		VALUES ($1, $2, $3) ON CONFLICT (operation_id, deployment_id) DO NOTHING`,
		child.ProjectRollbackID, child.CurrentDeploymentID, keys)
	return err
}

func projectRollbackUndoArtifactsError(err error) error {
	if errors.Is(err, ErrLayerArtifactRetired) || errors.Is(err, ErrInvalidArgument) {
		return ErrProjectRollbackUndoArtifactsUnavailable
	}
	return err
}

// Resume keeps a still-protected revision held through execution. Undo moves
// those same holds to its own receipt. Recheck expiry after taking the
// garbage collector's locks so neither operation revives an expired hold.
func continueProjectRollbackUndoArtifactHoldsTx(ctx context.Context, tx pgx.Tx, source, next string) error {
	rows, err := tx.Query(ctx, `
		SELECT DISTINCT key FROM project_rollback_undo_artifact_holds h, unnest(h.storage_keys) AS key
		WHERE operation_id = $1 AND expires_at > transaction_timestamp()`, source)
	if err != nil {
		return err
	}
	keys, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	// If a hold expires before locking completes it is excluded below, even
	// if its keys are still on disk. Keys from a live hold cannot be claimed.
	slices.Sort(keys)
	for _, key := range keys {
		if _, err := lockLayerArtifactTx(ctx, tx, key); err != nil {
			return err
		}
	}
	_, err = tx.Exec(ctx, `
		UPDATE project_rollback_undo_artifact_holds SET operation_id = $2, expires_at = NULL
		WHERE operation_id = $1 AND expires_at > transaction_timestamp() AND expires_at > clock_timestamp()`, source, next)
	return err
}

func (s *PgStore) ProjectRollbackUndoArtifactHolds(ctx context.Context, operation string) (map[string]api.ProjectRollbackUndoArtifacts, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT deployment_id::text, expires_at FROM project_rollback_undo_artifact_holds
		WHERE operation_id = $1`, operation)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]api.ProjectRollbackUndoArtifacts)
	for rows.Next() {
		var hold api.ProjectRollbackUndoArtifacts
		if err := rows.Scan(&hold.DeploymentID, &hold.ExpiresAt); err != nil {
			return nil, err
		}
		out[hold.DeploymentID] = hold
	}
	return out, rows.Err()
}

type projectRollbackUndoArtifactHold struct {
	keys      []string
	expiresAt time.Time
}

func (m *MemStore) projectRollbackUndoArtifactKeysLocked(child api.RollbackOperation) ([]string, error) {
	if m.projectRollbackOperations[child.ProjectRollbackID].UndoOf != "" {
		return nil, nil
	}
	d := m.deployments[child.CurrentDeploymentID]
	if d.RootfsKey == "" {
		return nil, ErrProjectRollbackUndoArtifactsUnavailable
	}
	keys := projectRollbackUndoArtifactKeys(m.deploymentLayerKeysLocked(d))
	return keys, projectRollbackUndoArtifactsError(m.requireLayerArtifactsRetainedLocked(keys))
}

func (m *MemStore) holdProjectRollbackUndoArtifactsLocked(operation, deployment string, keys []string) {
	if len(keys) == 0 {
		return
	}
	if m.projectRollbackUndoArtifactHolds == nil {
		m.projectRollbackUndoArtifactHolds = make(map[string]map[string]projectRollbackUndoArtifactHold)
	}
	holds := m.projectRollbackUndoArtifactHolds[operation]
	if holds == nil {
		holds = make(map[string]projectRollbackUndoArtifactHold)
		m.projectRollbackUndoArtifactHolds[operation] = holds
	}
	holds[deployment] = projectRollbackUndoArtifactHold{keys: keys}
}

func (m *MemStore) finishProjectRollbackUndoArtifactHoldsLocked(operation, status string, now time.Time) {
	switch status {
	case "blocked":
		holds := m.projectRollbackUndoArtifactHolds[operation]
		for deployment, hold := range holds {
			if hold.expiresAt.IsZero() {
				hold.expiresAt = now.Add(projectRollbackUndoArtifactWindow)
				holds[deployment] = hold
			}
		}
	case "complete":
		delete(m.projectRollbackUndoArtifactHolds, operation)
	}
}

func (m *MemStore) continueProjectRollbackUndoArtifactHoldsLocked(source, next string, now time.Time) {
	for deployment, hold := range m.projectRollbackUndoArtifactHolds[source] {
		if hold.expiresAt.After(now) {
			if source != next {
				delete(m.projectRollbackUndoArtifactHolds[source], deployment)
			}
			m.holdProjectRollbackUndoArtifactsLocked(next, deployment, hold.keys)
		}
	}
}

func (m *MemStore) projectRollbackUndoHoldsArtifactLocked(key string) bool {
	now := time.Now()
	for _, holds := range m.projectRollbackUndoArtifactHolds {
		for _, hold := range holds {
			if hold.expiresAt.IsZero() || hold.expiresAt.After(now) {
				if _, ok := slices.BinarySearch(hold.keys, key); ok {
					return true
				}
			}
		}
	}
	return false
}

func (m *MemStore) ProjectRollbackUndoArtifactHolds(_ context.Context, operation string) (map[string]api.ProjectRollbackUndoArtifacts, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]api.ProjectRollbackUndoArtifacts)
	for deployment, hold := range m.projectRollbackUndoArtifactHolds[operation] {
		item := api.ProjectRollbackUndoArtifacts{DeploymentID: deployment}
		if !hold.expiresAt.IsZero() {
			at := hold.expiresAt.UTC()
			item.ExpiresAt = &at
		}
		out[deployment] = item
	}
	return out, nil
}
