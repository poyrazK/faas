package state

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func readReleaseAdditionsDB(ctx context.Context, db sqlc.DBTX, projectID string) (map[string]projectReleaseAdditionRecord, error) {
	rows, err := sqlc.New().ListProjectReleaseAdditions(ctx, db, projectID)
	if err != nil {
		return nil, err
	}
	out := map[string]projectReleaseAdditionRecord{}
	for _, row := range rows {
		out[row.AppID] = projectReleaseAdditionRecord{row.AppID, row.AttemptID, row.Fingerprint, row.Dormant}
	}
	return out, nil
}

func releaseAdditionAppDB(ctx context.Context, tx pgx.Tx, appID string) (App, error) {
	row, err := sqlc.New().ReadReleaseAdditionApp(ctx, tx, appID)
	if err != nil {
		return App{}, mapErr(err)
	}
	app := App{ID: row.ID, AccountID: row.AccountID, ProjectID: row.ProjectID, Slug: row.Slug,
		RootDir: row.RootDir, WorkloadName: row.WorkloadName, WorkloadClass: WorkloadClass(row.WorkloadClass),
		StartCommand: row.StartCommand, RequireAuthn: row.RequireAuthn, PublicAuthMode: row.PublicAuthMode, PlatformTenantRequired: row.PlatformTenantRequired}
	if err := json.Unmarshal(row.Manifest, &app.Manifest); err != nil {
		return App{}, err
	}
	return app, nil
}

func (s *PgStore) DormantProjectReleaseApps(ctx context.Context, accountID, projectID string) (map[string]bool, error) {
	project, err := s.ProjectByID(ctx, projectID)
	if err != nil || project.AccountID != accountID {
		return nil, ErrNotFound
	}
	records, err := readReleaseAdditionsDB(ctx, s.pool, projectID)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for id, record := range records {
		if record.Dormant {
			out[id] = true
		}
	}
	return out, nil
}

func (s *PgStore) StageProjectReleaseAdditions(ctx context.Context, accountID, attemptID, projectID string, safe *api.ProjectSafeRelease, drafts []App) ([]App, error) {
	if err := validateAdditionDrafts(accountID, projectID, safe, drafts); err != nil {
		return nil, err
	}
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
	if entry.Status != "processing" {
		return nil, ErrConflict
	}
	plan, err := q.LockReleaseAdditionAccount(ctx, tx, accountID)
	if err != nil {
		return nil, mapErr(err)
	}
	if _, err := q.LockReleaseAdditionProject(ctx, tx, sqlc.LockReleaseAdditionProjectParams{ProjectID: projectID, AccountID: accountID}); err != nil {
		return nil, mapErr(err)
	}
	baseline, err := activeProjectReleaseSetIDTx(ctx, tx, projectID, "production")
	if err != nil {
		return nil, err
	}
	if baseline != safe.PreviousReleaseID {
		return nil, ErrConflict
	}
	records, err := readReleaseAdditionsDB(ctx, tx, projectID)
	if err != nil {
		return nil, err
	}
	var out []App
	for _, draft := range drafts {
		app := draft
		if draft.ID != "" {
			record, ok := records[draft.ID]
			current, err := releaseAdditionAppDB(ctx, tx, draft.ID)
			if err != nil {
				return nil, err
			}
			if !ok || !record.Dormant || record.Fingerprint != ProjectReleaseAdditionFingerprint(current) || record.Fingerprint != ProjectReleaseAdditionFingerprint(draft) {
				return nil, ErrConflict
			}
			if record.AttemptID != attemptID {
				owner, err := q.ReadProjectSubmission(ctx, tx, sqlc.ReadProjectSubmissionParams{AccountID: accountID, AttemptID: record.AttemptID})
				if err != nil {
					return nil, err
				}
				if !additionOwnerFinished(owner.Payload) {
					return nil, ErrConflict
				}
			}
		} else {
			app, err = createAppIfUnderQuotaTx(ctx, tx, draft, api.MustLimitsFor(api.Plan(plan)))
			if err != nil {
				return nil, err
			}
		}
		if err := q.ReserveProjectReleaseAddition(ctx, tx, sqlc.ReserveProjectReleaseAdditionParams{AppID: app.ID, AccountID: accountID, ProjectID: projectID, AttemptID: attemptID, Fingerprint: ProjectReleaseAdditionFingerprint(app)}); err != nil {
			return nil, err
		}
		out = append(out, app)
	}
	capture, err := json.Marshal(captureReservedAdditions(safe, out))
	if err != nil {
		return nil, err
	}
	n, err := q.CaptureProjectReleaseAdditions(ctx, tx, sqlc.CaptureProjectReleaseAdditionsParams{AccountID: accountID, AttemptID: attemptID, SafeRelease: capture})
	if err != nil {
		return nil, err
	}
	if n != 1 {
		return nil, ErrConflict
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

func guardReleaseAdditionsDB(ctx context.Context, tx pgx.Tx, accountID, projectID string, members map[string]string) (map[string]bool, error) {
	records, err := readReleaseAdditionsDB(ctx, tx, projectID)
	if err != nil {
		return nil, err
	}
	change, _ := ctx.Value(releaseAdditionChangeKey{}).(*releaseAdditionChange)
	if change != nil {
		baseline, err := readProjectReleaseSetTx(ctx, tx, accountID, projectID, "production", change.Safe.PreviousReleaseID)
		if err != nil {
			return nil, err
		}
		if err := guardReleaseAdditionMembership(change, baseline.Members, members); err != nil {
			return nil, err
		}
		for _, addition := range change.Safe.Additions {
			app, err := releaseAdditionAppDB(ctx, tx, addition.AppID)
			if err != nil {
				return nil, err
			}
			if app.ProjectID != projectID || ProjectReleaseAdditionFingerprint(app) != addition.Fingerprint {
				return nil, ErrConflict
			}
		}
	}
	return allowedReleaseAdditionOmissions(records, members, change)
}

func guardReleaseAdditionDeploymentDB(ctx context.Context, tx pgx.Tx, deployment Deployment) error {
	row, err := sqlc.New().ReadDeploymentReleaseAddition(ctx, tx, deployment.AppID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return guardReleaseAdditionDeployment(deployment, projectReleaseAdditionRecord{AttemptID: row.AttemptID, Dormant: row.Dormant})
}

func applyReleaseAdditionsDB(ctx context.Context, tx pgx.Tx) error {
	change, _ := ctx.Value(releaseAdditionChangeKey{}).(*releaseAdditionChange)
	if change == nil {
		return nil
	}
	for _, addition := range change.Safe.Additions {
		if err := sqlc.New().SetProjectReleaseAdditionDormant(ctx, tx, sqlc.SetProjectReleaseAdditionDormantParams{AppID: addition.AppID, Dormant: change.Rollback}); err != nil {
			return err
		}
	}
	return nil
}

func (m *MemStore) publishSafeProjectReleaseLocked(accountID, attemptID, projectID string, safe *api.ProjectSafeRelease, members []ProjectReleaseMember, rollback bool) (ProjectReleaseSet, error) {
	expected := safe.PreviousReleaseID
	if rollback {
		expected = safe.ReleaseID
	}
	var change *releaseAdditionChange
	if safe.AddServices {
		change = &releaseAdditionChange{attemptID, safe, rollback}
	}
	return m.publishProjectReleaseWithIDLocked(context.Background(), accountID, projectID, "production", &expected, nil, safe.TTLSeconds, members, "", change)
}
