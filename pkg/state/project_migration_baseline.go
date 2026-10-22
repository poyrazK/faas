package state

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ProjectMigrationBaselineRound is internal controller evidence, never accepted
// from the public deploy API. Probing begins after observing task completion.
type ProjectMigrationBaselineRound struct {
	TaskID    string
	StartedAt time.Time
	Evidence  []api.ProjectReleaseProbeEvidence
}

type ProjectMigrationBaselineStore interface {
	RecordProjectMigrationBaseline(context.Context, string, string, ProjectMigrationBaselineRound) (*api.ProjectSafeRelease, error)
}

func migrationBaselineVerdict(probes []api.ProjectReleaseProbe, evidence []api.ProjectReleaseProbeEvidence) string {
	if len(probes) == 0 || len(probes) != len(evidence) {
		return "unknown"
	}
	failed := false
	for i, p := range probes {
		e := evidence[i]
		if e.Probe != p || e.StableRequests < api.RouteHealthProbeRequestsPerMinute || e.StableErrors < 0 || e.StableErrors > e.StableRequests || e.Unauthenticated != 0 || e.UnexpectedResponses != 0 {
			return "unknown"
		}
		failed = failed || e.StableErrors > 0
	}
	if failed {
		return "failed"
	}
	return "healthy"
}

func projectReleaseCommandOutcome(accountID string, r api.ProjectSubmissionReceipt, deployments map[string]Deployment, tasks map[string]AppTask) (string, string) {
	status, message := projectReleaseTaskOutcome(accountID, r, deployments, tasks)
	command := r.Apply.SafeRelease.ReleaseCommand
	if status != "" || command == nil || !command.VerifyBaseline {
		return status, message
	}
	proof := command.BaselineVerification
	var task AppTask
	for _, t := range tasks {
		if t.ID == command.TaskID {
			task = t
			break
		}
	}
	if proof == nil || task.FinishedAt == nil || proof.TaskID != task.ID || proof.ReleaseID != r.Apply.SafeRelease.PreviousReleaseID || proof.StartedAt.Before(*task.FinishedAt) || proof.CheckedAt.Before(proof.StartedAt) || proof.CheckedAt.After(time.Now().UTC()) {
		return "building", "Migration completed; waiting for post-expansion production baseline probes."
	}
	verdict := migrationBaselineVerdict(r.Apply.SafeRelease.Verification.Probes, proof.Evidence)
	if verdict == "failed" {
		return "failed", "Production baseline failed critical probes after migration. Production was not switched. Inspect the task and database; schema changes are not rolled back and the migration will not be replayed."
	}
	if verdict != "healthy" || time.Since(proof.CheckedAt) > api.ProjectMigrationBaselineEvidenceMaxAge {
		return "building", "Post-expansion baseline health is unknown or stale; production was not switched."
	}
	return "", ""
}

func checkpointMigrationBaseline(accountID string, r *api.ProjectSubmissionReceipt, deployments map[string]Deployment, tasks map[string]AppTask, active string, round ProjectMigrationBaselineRound, now time.Time) (bool, error) {
	safe := r.Apply.SafeRelease
	if safe == nil || safe.Status != "building" || safe.ReleaseCommand == nil || !safe.ReleaseCommand.VerifyBaseline || safe.Verification == nil || active != safe.PreviousReleaseID || !now.Before(safe.Deadline) {
		return false, ErrConflict
	}
	if status, _ := projectReleaseTaskOutcome(accountID, *r, deployments, tasks); status != "" {
		return false, ErrConflict
	}
	command := safe.ReleaseCommand
	var task AppTask
	for _, t := range tasks {
		if t.ID == command.TaskID {
			task = t
			break
		}
	}
	if round.TaskID != task.ID || task.FinishedAt == nil || round.StartedAt.Before(*task.FinishedAt) || round.StartedAt.After(now) || now.Sub(round.StartedAt) > api.ProjectMigrationBaselineEvidenceMaxAge {
		return false, ErrConflict
	}
	if prior := command.BaselineVerification; prior != nil && !round.StartedAt.After(prior.CheckedAt) {
		return false, nil
	}
	command.BaselineVerification = &api.ProjectMigrationBaselineVerification{TaskID: task.ID, ReleaseID: active, StartedAt: round.StartedAt, CheckedAt: now, Status: migrationBaselineVerdict(safe.Verification.Probes, round.Evidence), Evidence: append([]api.ProjectReleaseProbeEvidence{}, round.Evidence...)}
	return true, nil
}

func (m *MemStore) RecordProjectMigrationBaseline(_ context.Context, accountID, attemptID string, round ProjectMigrationBaselineRound) (*api.ProjectSafeRelease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := accountID + "\x00" + attemptID
	entry, ok := m.projectSubmissions[key]
	if !ok {
		return nil, ErrNotFound
	}
	var r api.ProjectSubmissionReceipt
	if err := json.Unmarshal(entry.Payload, &r); err != nil {
		return nil, err
	}
	if r.Apply.SafeRelease == nil {
		return nil, ErrConflict
	}
	changed, err := checkpointMigrationBaseline(accountID, &r, m.deployments, m.projectReleaseCommandTasksLocked(r), m.activeProjectReleaseSets[releaseKey(r.Apply.ProjectID, "production")], round, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	if changed {
		payload, err := json.Marshal(r)
		if err != nil {
			return nil, err
		}
		entry.Payload = payload
		m.projectSubmissions[key] = entry
	}
	return r.Apply.SafeRelease, nil
}

func (s *PgStore) RecordProjectMigrationBaseline(ctx context.Context, accountID, attemptID string, round ProjectMigrationBaselineRound) (*api.ProjectSafeRelease, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	entry, err := q.LockSafeProjectSubmission(ctx, tx, sqlc.LockSafeProjectSubmissionParams{AccountID: accountID, AttemptID: attemptID})
	if err != nil {
		return nil, mapErr(err)
	}
	var r api.ProjectSubmissionReceipt
	if err = json.Unmarshal(entry.Payload, &r); err != nil {
		return nil, err
	}
	if r.Apply.SafeRelease == nil {
		return nil, ErrConflict
	}
	if err = q.LockSafeProjectReleaseProject(ctx, tx, sqlc.LockSafeProjectReleaseProjectParams{ProjectID: r.Apply.ProjectID, AccountID: accountID}); err != nil {
		return nil, err
	}
	active, err := q.ActiveSafeProjectReleaseID(ctx, tx, r.Apply.ProjectID)
	if err != nil {
		return nil, err
	}
	deployments := map[string]Deployment{}
	builds := append([]api.AppliedBuild(nil), r.Apply.Builds...)
	sort.Slice(builds, func(i, j int) bool { return builds[i].AppID < builds[j].AppID })
	for _, b := range builds {
		if b.DeploymentID == "" {
			continue
		}
		row, readErr := q.LockSafeProjectReleaseCandidate(ctx, tx, b.DeploymentID)
		if readErr != nil {
			return nil, mapErr(readErr)
		}
		deployments[b.DeploymentID] = Deployment{AppID: row.AppID, RootfsKey: row.RootfsKey, ImageDigest: row.ImageDigest, Scope: row.Scope, ReleaseCommand: row.ReleaseCommand, ReleaseCommandShell: row.ReleaseCommandShell}
	}
	tasks, err := readProjectReleaseCommandTasks(ctx, tx, r)
	if err != nil {
		return nil, err
	}
	changed, err := checkpointMigrationBaseline(accountID, &r, deployments, tasks, active, round, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	if changed {
		payload, marshalErr := json.Marshal(r.Apply.SafeRelease)
		if marshalErr != nil {
			return nil, marshalErr
		}
		if err = q.FinishSafeProjectSubmission(ctx, tx, sqlc.FinishSafeProjectSubmissionParams{AccountID: accountID, AttemptID: attemptID, SafeRelease: payload}); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return r.Apply.SafeRelease, nil
}
