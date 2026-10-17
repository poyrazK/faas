package state

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// Only the scheduler supplies the fence of the actual prepared task VM.
// Release preparation captures it once; cleanup must reproduce that same input.
type ProjectMigrationTaskRuntimeStore interface {
	BindProjectMigrationTaskRuntime(context.Context, AppTask, RuntimeAppConfigFence) error
	ProjectMigrationTaskIsContract(context.Context, AppTask) (bool, error)
}

func (m *MemStore) migrationContractForTaskLocked(task AppTask) (string, projectMigrationContract, bool) {
	for key, c := range m.migrationContracts {
		account, attempt, _ := strings.Cut(key, "\x00")
		if account != task.AccountID || c.AppID != task.AppID {
			continue
		}
		if c.TaskID == task.ID {
			return key, c, true
		}
		if task.Kind == AppTaskKindRelease {
			receipt := m.deploymentSubmissionReceipts[submissionKey(task.AppID, ProjectWorkloadAttempt(attempt, task.AppID))]
			if receipt.DeploymentID == task.DeploymentID {
				return key, c, true
			}
		}
	}
	return "", projectMigrationContract{}, false
}
func (m *MemStore) migrationRuntimeCurrentLocked(task AppTask) bool {
	if m.guardPromotionMigrationRuntimeLocked(task, true) != nil {
		return false
	}
	_, c, found := m.migrationContractForTaskLocked(task)
	if !found {
		return true
	}
	if c.RuntimeFingerprint == "" {
		return false
	}
	if c.TaskID == task.ID && (task.LeaseToken == nil || c.PreparedLeaseToken != *task.LeaseToken) {
		return false
	}
	if task.Kind == AppTaskKindRelease && c.ExpansionTaskID != task.ID {
		return false
	}
	snapshot, err := m.runtimeAppValuesLocked(task.AccountID, task.AppID, task.DeploymentID)
	if err != nil {
		return false
	}
	fence, err := NewRuntimeAppConfigFence(snapshot)
	return err == nil && fence.Fingerprint == c.RuntimeFingerprint
}
func (m *MemStore) BindProjectMigrationTaskRuntime(_ context.Context, passed AppTask, fence RuntimeAppConfigFence) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	task, found := m.appTasks[passed.ID]
	if !found {
		return ErrNotFound
	}
	if handled, err := m.bindPromotionMigrationRuntimeLocked(task, passed, fence); handled {
		return err
	}
	key, c, planned := m.migrationContractForTaskLocked(task)
	if !planned {
		return nil
	}
	if task.Status != AppTaskRestoring || task.LeaseToken == nil || passed.LeaseToken == nil || *task.LeaseToken != *passed.LeaseToken || task.LeaseExpiresAt == nil || !task.LeaseExpiresAt.After(time.Now()) || !validRuntimeAppConfigFence(fence) {
		return ErrConflict
	}
	snapshot, err := m.runtimeAppValuesLocked(task.AccountID, task.AppID, task.DeploymentID)
	if err != nil {
		return err
	}
	current, err := NewRuntimeAppConfigFence(snapshot)
	if err != nil || current != fence {
		return ErrConflict
	}
	if c.TaskID == task.ID {
		if c.RuntimeFingerprint != fence.Fingerprint {
			return ErrConflict
		}
		c.PreparedLeaseToken = *task.LeaseToken
	} else {
		var r api.ProjectSubmissionReceipt
		if json.Unmarshal(m.projectSubmissions[key].Payload, &r) != nil || r.Apply.SafeRelease == nil || r.Apply.SafeRelease.ReleaseCommand == nil || api.ProjectReleaseCommandHash(task.Command, task.CommandShell) != r.Apply.SafeRelease.ReleaseCommand.CommandHash || c.TaskID != "" {
			return ErrConflict
		}
		if c.ExpansionTaskID != "" && (c.ExpansionTaskID != task.ID || c.RuntimeFingerprint != fence.Fingerprint) {
			return ErrConflict
		}
		c.ExpansionTaskID, c.RuntimeFingerprint = task.ID, fence.Fingerprint
	}
	m.migrationContracts[key] = c
	return nil
}
func guardMigrationTaskRuntimeDB(ctx context.Context, db sqlc.DBTX, task AppTask) error {
	if err := guardPromotionMigrationDispatchDB(ctx, db, task); err != nil {
		return err
	}
	if err := guardPromotionMigrationRuntimeDB(ctx, db, task, true); err != nil {
		return err
	}
	q := sqlc.New()
	c, err := q.MigrationContractForTask(ctx, db, task.ID)
	if errors.Is(mapErr(err), ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if c.RuntimeFingerprint == "" || task.Kind == AppTaskKindRelease && c.ExpansionTaskID != task.ID {
		return ErrConflict
	}
	snapshot, err := runtimeAppValuesDB(ctx, db, task.AccountID, task.AppID, task.DeploymentID)
	if err != nil {
		return err
	}
	fence, err := NewRuntimeAppConfigFence(snapshot)
	if err != nil || fence.Fingerprint != c.RuntimeFingerprint {
		return ErrConflict
	}
	return nil
}
func (s *PgStore) BindProjectMigrationTaskRuntime(ctx context.Context, passed AppTask, fence RuntimeAppConfigFence) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	row, err := q.ReadMigrationTaskForUpdate(ctx, tx, passed.ID)
	if err != nil {
		return mapErr(err)
	}
	task, err := appTaskFromSQLC(row)
	if err != nil {
		return err
	}
	if handled, err := bindPromotionMigrationRuntimeDB(ctx, tx, task, passed, fence); handled {
		if err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	c, err := q.MigrationContractForTask(ctx, tx, task.ID)
	if errors.Is(mapErr(err), ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if task.Status != AppTaskRestoring || task.LeaseToken == nil || passed.LeaseToken == nil || *task.LeaseToken != *passed.LeaseToken || task.LeaseExpiresAt == nil || !task.LeaseExpiresAt.After(time.Now()) || !validRuntimeAppConfigFence(fence) {
		return ErrConflict
	}
	if err = q.LockSafeProjectReleaseProject(ctx, tx, sqlc.LockSafeProjectReleaseProjectParams{ProjectID: c.CProjectID, AccountID: task.AccountID}); err != nil {
		return err
	}
	if err = lockSecretRuntimeApp(ctx, tx, task.AccountID, task.AppID); err != nil {
		return err
	}
	snapshot, err := runtimeAppValuesDB(ctx, tx, task.AccountID, task.AppID, task.DeploymentID)
	if err != nil {
		return err
	}
	current, err := NewRuntimeAppConfigFence(snapshot)
	if err != nil || current != fence {
		return ErrConflict
	}
	var n int64
	if c.TaskID == task.ID {
		n, err = q.PrepareMigrationContractRuntime(ctx, tx, sqlc.PrepareMigrationContractRuntimeParams{TaskID: task.ID, Token: *task.LeaseToken, Fingerprint: fence.Fingerprint})
	} else {
		if api.ProjectReleaseCommandHash(task.Command, task.CommandShell) != c.ExpansionHash {
			return ErrConflict
		}
		n, err = q.BindMigrationExpansionRuntime(ctx, tx, sqlc.BindMigrationExpansionRuntimeParams{TaskID: task.ID, Fingerprint: fence.Fingerprint, AccountID: task.AccountID, AttemptID: c.AttemptID})
	}
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	return tx.Commit(ctx)
}

func (m *MemStore) ProjectMigrationTaskIsContract(_ context.Context, task AppTask) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	current, ok := m.appTasks[task.ID]
	if !ok || current.AccountID != task.AccountID || current.AppID != task.AppID || current.Kind != AppTaskKindManual {
		return false, nil
	}
	_, c, found := m.migrationContractForTaskLocked(current)
	return found && c.TaskID == current.ID, nil
}
func (s *PgStore) ProjectMigrationTaskIsContract(ctx context.Context, task AppTask) (bool, error) {
	return sqlc.New().MigrationTaskIsContract(ctx, s.pool, sqlc.MigrationTaskIsContractParams{ID: task.ID, AccountID: task.AccountID, AppID: task.AppID})
}
