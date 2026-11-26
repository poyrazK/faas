package state

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var ErrProjectRollbackResumePlanChanged = errors.New("reviewed project rollback resume plan changed")

// ProjectRollbackResumePin records the production period checked before resume.
// A pending cutover also pins its target and the release runs it crosses.
type ProjectRollbackResumePin struct {
	AppID, Slug, Scope, CurrentID, TargetID string
	CurrentServedAt                         time.Time
	Releases                                []ReleaseRun
}

type projectRollbackResumeReviewKey struct{}

func WithProjectRollbackResumeReview(ctx context.Context, pins []ProjectRollbackResumePin) context.Context {
	return context.WithValue(ctx, projectRollbackResumeReviewKey{}, pins)
}

func projectRollbackResumeReview(ctx context.Context) []ProjectRollbackResumePin {
	pins, _ := ctx.Value(projectRollbackResumeReviewKey{}).([]ProjectRollbackResumePin)
	return pins
}

func checkPgProjectRollbackResumeReview(ctx context.Context, tx pgx.Tx, project string) error {
	for _, pin := range projectRollbackResumeReview(ctx) {
		var appID string
		err := tx.QueryRow(ctx, `
			SELECT id::text FROM apps WHERE id = $1 AND project_id = $2 AND slug = $3 AND status <> 'deleted'
			FOR UPDATE NOWAIT`, pin.AppID, project, pin.Slug).Scan(&appID)
		if err != nil {
			return projectRollbackResumeReviewError(err)
		}
		live, err := readLiveDeployments(ctx, tx, pin.AppID)
		if err != nil {
			return err
		}
		if !matchesProjectRollbackReviewedServing(pin.Scope, pin.CurrentID, &pin.CurrentServedAt, live) {
			return ErrProjectRollbackResumePlanChanged
		}
		if pin.TargetID != "" {
			var scope string
			err := tx.QueryRow(ctx, `
				SELECT scope FROM deployments WHERE id = $1 AND app_id = $2 AND deleted_at IS NULL
				AND (status = 'superseded' OR (status = 'live' AND traffic_percent = 0))`,
				pin.TargetID, pin.AppID).Scan(&scope)
			if err != nil {
				return projectRollbackResumeReviewError(err)
			}
			scope = normalizedDeploymentScope(scope)
			if err := lockProjectRollbackReviewedReleaseHistory(ctx, tx, pin.AppID, scope); err != nil {
				return projectRollbackResumeReviewError(err)
			}
			matches, err := pgProjectRollbackReviewedReleasesMatch(ctx, tx, pin.AppID, pin.TargetID, scope, pin.Releases)
			if err != nil {
				return err
			}
			if !matches {
				return ErrProjectRollbackResumePlanChanged
			}
		}
	}
	return nil
}

func (m *MemStore) checkProjectRollbackResumeReviewLocked(ctx context.Context, project string) error {
	for _, pin := range projectRollbackResumeReview(ctx) {
		app := m.apps[pin.AppID]
		if app.ProjectID != project || app.Slug != pin.Slug || app.Status == AppDeleted ||
			!matchesProjectRollbackReviewedServing(pin.Scope, pin.CurrentID, &pin.CurrentServedAt, m.liveDeploymentsLocked(pin.AppID)) {
			return ErrProjectRollbackResumePlanChanged
		}
		if pin.TargetID != "" {
			target := m.deployments[pin.TargetID]
			if target.AppID != pin.AppID || target.DeletedAt != nil ||
				(target.Status != DeploySuperseded && (target.Status != DeployLive || target.TrafficPercent != 0)) ||
				!matchesProjectRollbackUndoReleases(pin.TargetID, m.releaseRunsCrossedByLocked(pin.AppID, pin.TargetID),
					m.releasesUndoneByDatabaseSwitchesLocked(pin.AppID, target.Scope), pin.Releases) {
				return ErrProjectRollbackResumePlanChanged
			}
		}
	}
	return nil
}

func projectRollbackResumeReviewError(err error) error {
	var pgErr *pgconn.PgError
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return ErrProjectRollbackResumePlanChanged
	case errors.As(err, &pgErr) && pgErr.Code == pgerrcode.LockNotAvailable:
		return ErrConflict
	default:
		return err
	}
}

func lockProjectRollbackReviewedReleaseHistory(ctx context.Context, tx pgx.Tx, app, scope string) error {
	// Release starts share the app lock with traffic writes, but an already
	// running task and an in-flight database switch can finish independently.
	if _, err := tx.Exec(ctx, `
		SELECT id FROM app_tasks WHERE app_id = $1 AND kind = 'release' AND status = 'running'
		FOR SHARE NOWAIT`, app); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `
		SELECT id FROM release_database_switches WHERE app_id = $1 AND scope = $2 AND finished_at IS NULL
		FOR SHARE NOWAIT`, app, scope)
	return err
}

func pgProjectRollbackReviewedReleasesMatch(ctx context.Context, tx pgx.Tx, app, target, scope string, review []ReleaseRun) (bool, error) {
	runs, err := releaseRunsCrossedByDBTX(ctx, tx, app, target)
	if err != nil {
		return false, err
	}
	undone, err := sqlc.New().ListReleasesUndoneByDatabaseSwitches(ctx, tx, sqlc.ListReleasesUndoneByDatabaseSwitchesParams{
		AppID: mustPgUUID(app), Scope: scope,
	})
	if err != nil {
		return false, err
	}
	return matchesProjectRollbackUndoReleases(target, runs, undone, review), nil
}

func matchesProjectRollbackReviewedServing(scope, id string, servedAt *time.Time, live []Deployment) bool {
	found := false
	for _, d := range live {
		deploymentScope := normalizedDeploymentScope(d.Scope)
		if d.DeletedAt != nil || d.TrafficPercent == 0 || (deploymentScope != api.DefaultEnvScope && deploymentScope != "production") {
			continue
		}
		if d.ID != id || deploymentScope != scope || d.TrafficPercent != 100 || d.RolloutState != "complete" ||
			!sameProjectServingPeriod(d.RolloutCompletedAt, servedAt) {
			return false
		}
		found = true
	}
	return found
}
