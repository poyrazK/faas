package state

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// A replacement binding can have an unchanged intent plan while its former
// Job was edited or deleted after detach. Reconcile those Jobs under the new
// controller lease before freezing the new graph's physical Job identities.
func (m *MemStore) reconcileReleasedCandidateJobsLocked(source EnvironmentGitSource, revision EnvironmentDesiredRevision, inputs []Deployment, snapshot gitOpsIntentSnapshot) (gitOpsIntentSnapshot, error) {
	desired, err := desiredEnvironmentRevision(revision)
	if err != nil {
		return snapshot, err
	}
	for i, app := range snapshot.Apps {
		if !candidateHasScheduledJob(inputs, app) {
			continue
		}
		row := *app.WorkloadIntent
		if !m.environmentGitOpsJobReleasedLocked(m.jobs[row.JobID]) {
			continue
		}
		_, workload, _, matched := environmentGitOpsAppForWorkload(snapshot, desired, app.ID)
		if !matched || workload.Schedule == nil {
			continue
		}
		row, err = m.syncEnvironmentGitOpsJobLocked(source, row, app, workload, snapshot.Plan, true)
		if err != nil {
			return snapshot, err
		}
		row = m.putWorkloadIntentLocked(row)
		snapshot.Apps[i].WorkloadIntent = &row
	}
	return snapshot, nil
}

func reconcileReleasedCandidateJobsTx(ctx context.Context, tx pgx.Tx, source EnvironmentGitSource, revision EnvironmentDesiredRevision, inputs []Deployment, snapshot gitOpsIntentSnapshot) (gitOpsIntentSnapshot, error) {
	desired, err := desiredEnvironmentRevision(revision)
	if err != nil {
		return snapshot, err
	}
	for i, app := range snapshot.Apps {
		if !candidateHasScheduledJob(inputs, app) {
			continue
		}
		row := *app.WorkloadIntent
		released, err := sqlc.New().EnvironmentJobIsReleased(ctx, tx, mustPgUUID(row.JobID))
		if err != nil {
			return snapshot, mapErr(err)
		}
		if !released {
			continue
		}
		_, workload, _, matched := environmentGitOpsAppForWorkload(snapshot, desired, app.ID)
		if !matched || workload.Schedule == nil {
			continue
		}
		row, err = syncEnvironmentGitOpsJobTx(ctx, tx, source, app, row, workload, snapshot.Plan, true)
		if err != nil {
			return snapshot, err
		}
		if _, err := putWorkloadIntentTx(ctx, tx, row); err != nil {
			return snapshot, err
		}
		snapshot.Apps[i].WorkloadIntent = &row
	}
	return snapshot, nil
}

func candidateHasScheduledJob(inputs []Deployment, app gitOpsIntentApp) bool {
	if app.WorkloadIntent == nil || app.WorkloadIntent.JobID == "" || app.WorkloadIntent.Schedule == nil {
		return false
	}
	for _, input := range inputs {
		if input.AppID == app.ID {
			return true
		}
	}
	return false
}
