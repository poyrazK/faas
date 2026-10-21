package state

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// StageProjectReleaseCommand serializes pending migration admissions for a
// project. Execution remains owned by imaged/schedd and their unique app task.
type ProjectReleaseCommandStore interface {
	StageProjectReleaseCommand(context.Context, string, string, string, *api.ProjectSafeRelease) error
}

func validProjectReleaseCommand(safe *api.ProjectSafeRelease) bool {
	return safe != nil && safe.ReleaseCommand != nil && safe.Verification != nil && safe.ReleaseCommand.AppID != "" && safe.ReleaseCommand.Slug != "" && len(safe.ReleaseCommand.CommandHash) == 64 && safe.ReleaseCommand.Compatibility == "backward-compatible"
}

func stagedProjectReleaseCommandMatches(receipt api.ProjectSubmissionReceipt, projectID string, safe *api.ProjectSafeRelease) bool {
	if receipt.Apply.ProjectID != projectID || receipt.Apply.SafeRelease == nil || receipt.Apply.SafeRelease.PreviousReleaseID != safe.PreviousReleaseID || receipt.Apply.SafeRelease.ReleaseCommand == nil {
		return false
	}
	prior := receipt.Apply.SafeRelease.ReleaseCommand
	return prior.Compatibility == "backward-compatible" && prior.VerifyBaseline == safe.ReleaseCommand.VerifyBaseline && (prior.CommandHash == "" || api.ProjectReleaseCommandPolicyID(prior) == api.ProjectReleaseCommandPolicyID(safe.ReleaseCommand))
}

func pendingProjectMigration(payload []byte, projectID string) bool {
	var r api.ProjectSubmissionReceipt
	return json.Unmarshal(payload, &r) == nil && r.Apply.ProjectID == projectID && r.Apply.SafeRelease != nil && r.Apply.SafeRelease.ReleaseCommand != nil && pendingSafeProjectRelease(r.Apply.SafeRelease.Status)
}

func (m *MemStore) StageProjectReleaseCommand(_ context.Context, accountID, attemptID, projectID string, safe *api.ProjectSafeRelease) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !validProjectReleaseCommand(safe) {
		return ErrInvalidArgument
	}
	key := accountID + "\x00" + attemptID
	entry, ok := m.projectSubmissions[key]
	app := m.apps[safe.ReleaseCommand.AppID]
	project := m.projects[projectID]
	if !ok || entry.Status != "processing" || project.AccountID != accountID || app.AccountID != accountID || app.ProjectID != projectID || app.Slug != safe.ReleaseCommand.Slug || m.activeProjectReleaseSets[releaseKey(projectID, "production")] != safe.PreviousReleaseID {
		return ErrConflict
	}
	if m.pendingPromotionMigrationLocked(projectID) {
		return ErrConflict
	}
	for otherKey, other := range m.projectSubmissions {
		if otherKey != key && pendingProjectMigration(other.Payload, projectID) {
			return ErrConflict
		}
	}
	for _, task := range m.appTasks {
		if task.Kind == AppTaskKindRelease && !task.Status.Terminal() && m.apps[task.AppID].ProjectID == projectID {
			return ErrConflict
		}
	}
	var receipt api.ProjectSubmissionReceipt
	if err := json.Unmarshal(entry.Payload, &receipt); err != nil {
		return err
	}
	if !stagedProjectReleaseCommandMatches(receipt, projectID, safe) {
		return ErrConflict
	}
	receipt.Apply.SafeRelease.ReleaseCommand = safe.ReleaseCommand
	payload, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	entry.Payload = payload
	m.projectSubmissions[key] = entry
	return nil
}

func (s *PgStore) StageProjectReleaseCommand(ctx context.Context, accountID, attemptID, projectID string, safe *api.ProjectSafeRelease) error {
	if !validProjectReleaseCommand(safe) {
		return ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	entry, err := q.LockSafeProjectSubmission(ctx, tx, sqlc.LockSafeProjectSubmissionParams{AccountID: accountID, AttemptID: attemptID})
	if err != nil {
		return mapErr(err)
	}
	if entry.Status != "processing" {
		return ErrConflict
	}
	var receipt api.ProjectSubmissionReceipt
	if json.Unmarshal(entry.Payload, &receipt) != nil || !stagedProjectReleaseCommandMatches(receipt, projectID, safe) {
		return ErrConflict
	}
	if err := q.LockSafeProjectReleaseProject(ctx, tx, sqlc.LockSafeProjectReleaseProjectParams{ProjectID: projectID, AccountID: accountID}); err != nil {
		return err
	}
	allowed, err := q.ProjectMigrationAdmissionAllowed(ctx, tx, sqlc.ProjectMigrationAdmissionAllowedParams{AccountID: accountID, ProjectID: projectID, AppID: safe.ReleaseCommand.AppID, Slug: safe.ReleaseCommand.Slug, BaselineID: safe.PreviousReleaseID, AttemptID: attemptID})
	if err != nil {
		return err
	}
	if !allowed.Valid || !allowed.Bool {
		return ErrConflict
	}
	payload, err := json.Marshal(safe.ReleaseCommand)
	if err != nil {
		return err
	}
	if err := q.StageProjectReleaseCommand(ctx, tx, sqlc.StageProjectReleaseCommandParams{AccountID: accountID, AttemptID: attemptID, ReleaseCommand: payload}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Release tasks are terminal and immutable, so a success read in the graph
// transaction remains sufficient after commit. Missing or uncertain evidence
// never permits cutover, even if a candidate is already marked live.
func projectReleaseTaskOutcome(accountID string, r api.ProjectSubmissionReceipt, deployments map[string]Deployment, tasks map[string]AppTask) (string, string) {
	command := r.Apply.SafeRelease.ReleaseCommand
	if command == nil {
		for _, build := range r.Apply.Builds {
			dep := deployments[build.DeploymentID]
			if len(dep.ReleaseCommand) > 0 {
				return "conflicted", "Unplanned release command; production was not switched."
			}
		}
		return "", ""
	}
	if !validProjectReleaseCommand(r.Apply.SafeRelease) {
		return "conflicted", "Migration plan is incomplete; production was not switched."
	}
	var selected Deployment
	var deploymentID string
	for _, build := range r.Apply.Builds {
		dep, found := deployments[build.DeploymentID]
		if build.AppID == command.AppID {
			if !found {
				continue
			}
			if deploymentID != "" {
				return "conflicted", "Migration workload appears more than once."
			}
			selected, deploymentID = dep, build.DeploymentID
		} else if len(dep.ReleaseCommand) > 0 {
			return "conflicted", "Multiple migration commands are unsupported."
		}
	}
	if deploymentID == "" {
		return "building", ""
	}
	if selected.AppID != command.AppID || len(selected.ReleaseCommand) == 0 || api.ProjectReleaseCommandHash(selected.ReleaseCommand, selected.ReleaseCommandShell) != command.CommandHash {
		return "conflicted", "Candidate migration differs from its plan; production was not switched."
	}
	task, found := tasks[deploymentID]
	if !found {
		command.Status = "waiting"
		return "building", ""
	}
	if task.Kind != AppTaskKindRelease || task.AccountID != accountID || task.AppID != command.AppID || task.DeploymentID != deploymentID || task.ArtifactKey != selected.RootfsKey || task.ImageDigest != selected.ImageDigest || task.DeploymentScope != selected.Scope || api.ProjectReleaseCommandHash(task.Command, task.CommandShell) != command.CommandHash {
		return "conflicted", "Release task does not match the planned candidate artifact."
	}
	command.TaskID, command.Status = task.ID, string(task.Status)
	command.ExitCode = task.ExitCode
	if task.FailureCode != nil {
		command.FailureCode = *task.FailureCode
	}
	switch task.Status {
	case AppTaskSucceeded:
		if task.ExitCode == nil || *task.ExitCode != 0 {
			return "conflicted", "Release task success lacks a zero exit receipt; production was not switched."
		}
		return "", ""
	case AppTaskFailed, AppTaskTimedOut, AppTaskCancelled:
		if task.StartedAt != nil && task.ExitCode == nil {
			command.Status = "uncertain"
			return "failed", "Migration outcome is uncertain after dispatch; the command was not replayed. Inspect the task and database before a new deploy. Production was not switched."
		}
		return "failed", "Release command did not succeed; production was not switched. Database changes are not rolled back."
	default:
		return "building", ""
	}
}

func (m *MemStore) projectReleaseCommandTasksLocked(r api.ProjectSubmissionReceipt) map[string]AppTask {
	tasks := map[string]AppTask{}
	for _, build := range r.Apply.Builds {
		for _, task := range m.appTasks {
			if task.DeploymentID == build.DeploymentID && task.Kind == AppTaskKindRelease {
				tasks[build.DeploymentID] = task
			}
		}
	}
	return tasks
}

func readProjectReleaseCommandTasks(ctx context.Context, tx sqlc.DBTX, r api.ProjectSubmissionReceipt) (map[string]AppTask, error) {
	tasks := map[string]AppTask{}
	if r.Apply.SafeRelease.ReleaseCommand == nil {
		return tasks, nil
	}
	for _, build := range r.Apply.Builds {
		if build.DeploymentID == "" || build.AppID != r.Apply.SafeRelease.ReleaseCommand.AppID {
			continue
		}
		row, err := sqlc.New().ReadProjectReleaseCommandTask(ctx, tx, build.DeploymentID)
		if errors.Is(mapErr(err), ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		task := AppTask{ID: row.ID, AccountID: row.AccountID, AppID: row.AppID, DeploymentID: row.DeploymentID, Kind: AppTaskKind(row.Kind), Command: row.Command, CommandShell: row.CommandShell, DeploymentScope: row.DeploymentScope, ArtifactKey: row.ArtifactKey, ImageDigest: row.ImageDigest, Status: AppTaskStatus(row.Status), ExitCode: executionIntPtr(row.ExitCode), FailureCode: executionStringPtr(row.FailureCode), StartedAt: timestamptzToTimePtr(row.StartedAt), FinishedAt: timestamptzToTimePtr(row.FinishedAt)}
		tasks[build.DeploymentID] = task
	}
	return tasks, nil
}
