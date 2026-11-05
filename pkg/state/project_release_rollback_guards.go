package state

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// Graph routing overrides weighted fallback traffic. Its serving cutoff comes
// from the last graph that served the member, even when its fallback stayed 100%.
func (m *MemStore) checkProjectRollbackReleasesLocked(project, scope, activeID string, members []ProjectReleaseMember, fallback bool) error {
	active := m.projectReleaseSets[activeID]
	staged := make(map[string]bool)
	for _, canary := range m.projectCanaries {
		if canary.ProjectID == project && canary.Policy.Percent != api.ProjectCanaryFinalPercent {
			staged[canary.Policy.CandidateReleaseID] = true
		}
	}
	for _, member := range members {
		if releaseMemberForApp(active, member.AppID) == member.DeploymentID || activeID == "" && m.deployments[member.DeploymentID].TrafficPercent > 0 {
			continue
		}
		var since time.Time
		if fallback {
			since = active.fallbackSince
			if since.IsZero() {
				since = m.deployments[member.DeploymentID].CreatedAt
			}
		}
		for _, release := range m.projectReleaseSets {
			if release.ProjectID == project && release.EnvironmentSlug == scope && !release.Active && !staged[release.ID] && release.ExpiresAt != nil && releaseMemberForApp(release, member.AppID) == member.DeploymentID {
				ended := release.ExpiresAt.Add(-time.Duration(release.TTLSeconds) * time.Second)
				if ended.After(since) {
					since = ended
				}
			}
		}
		if since.IsZero() {
			continue // A new member has no earlier graph serving history.
		}
		runs := m.releaseRunsAfterLocked(member.AppID, scope, member.DeploymentID, since)
		if err := checkRollbackReleaseReview(member.DeploymentID, runs, m.releasesUndoneByDatabaseSwitchesLocked(member.AppID, scope), nil); err != nil {
			return err
		}
	}
	return nil
}

func checkProjectRollbackReleasesTx(ctx context.Context, tx pgx.Tx, account, project, scope, activeID string, members []ProjectReleaseMember, fallback bool) error {
	var active ProjectReleaseSet
	if activeID != "" {
		var err error
		active, err = readProjectReleaseSetTx(ctx, tx, account, project, scope, activeID)
		if err != nil {
			return err
		}
	}
	q := sqlc.New()
	for _, member := range members {
		// The workflow admission epoch also changes when a release task starts.
		// A repeatable-read config transaction must retry a stale history snapshot.
		if err := q.GuardWorkflowAdmissionSnapshot(ctx, tx, member.AppID); err != nil {
			return err
		}
		if releaseMemberForApp(active, member.AppID) == member.DeploymentID {
			continue
		}
		if activeID == "" {
			traffic, err := q.ServiceRolloutRecipientTraffic(ctx, tx, mustPgUUID(member.DeploymentID))
			if err != nil {
				return err
			}
			if traffic > 0 {
				continue
			}
		}
		ended, err := q.LastGraphMemberServingEnd(ctx, tx, sqlc.LastGraphMemberServingEndParams{ProjectID: mustPgUUID(project), Scope: scope, AppID: mustPgUUID(member.AppID), DeploymentID: mustPgUUID(member.DeploymentID)})
		if err != nil {
			return err
		}
		if fallback {
			cutoff, err := q.ProjectFallbackServingEnd(ctx, tx, sqlc.ProjectFallbackServingEndParams{ReleaseID: mustPgUUID(activeID), TargetID: mustPgUUID(member.DeploymentID)})
			if err != nil {
				return err
			}
			if !ended.Valid || cutoff.Time.After(ended.Time) {
				ended = cutoff
			}
		}
		if !ended.Valid {
			continue
		}
		rows, err := q.RollbackReleaseRunsAfter(ctx, tx, sqlc.RollbackReleaseRunsAfterParams{AppID: mustPgUUID(member.AppID), Scope: normalizedDeploymentScope(scope), TargetID: mustPgUUID(member.DeploymentID), Since: ended})
		if err != nil {
			return err
		}
		runs := make([]ReleaseRun, 0, len(rows))
		for _, row := range rows {
			runs = append(runs, ReleaseRun{DeploymentID: row.DeploymentID, StartedAt: row.StartedAt.Time})
		}
		if err := checkRollbackRunsTx(ctx, tx, member.AppID, scope, member.DeploymentID, runs); err != nil {
			return err
		}
	}
	return nil
}

func recordProjectFallbackWindowTx(ctx context.Context, tx pgx.Tx, release, previous string) error {
	return sqlc.New().RecordProjectFallbackWindow(ctx, tx, sqlc.RecordProjectFallbackWindowParams{ReleaseID: mustPgUUID(release), PreviousID: mustPgUUID(previous)})
}
