package state

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func graphVerificationSQL(row sqlc.ProjectBindingVerificationRun) (graphVerificationRecord, error) {
	var r graphVerificationRecord
	err := json.Unmarshal(row.Snapshot, &r)
	r.Report.Status = row.Status
	r.Report.CreatedAt = row.CreatedAt.Time
	r.Report.ExpiresAt = row.ExpiresAt.Time
	return r, err
}
func lockGraphVerificationCatalog(ctx context.Context, tx pgx.Tx, account, project, environment string) error {
	q := sqlc.New()
	if _, err := q.LockProjectEnvironmentCloneProject(ctx, tx, sqlc.LockProjectEnvironmentCloneProjectParams{AccountID: mustPgUUID(account), ProjectID: mustPgUUID(project)}); err != nil {
		return mapErr(err)
	}
	ids, err := q.LockProjectEnvironmentCloneApps(ctx, tx, sqlc.LockProjectEnvironmentCloneAppsParams{AccountID: mustPgUUID(account), ProjectID: mustPgUUID(project)})
	if err != nil {
		return err
	}
	appIDs := make([]pgtype.UUID, 0, len(ids))
	for _, id := range ids {
		appIDs = append(appIDs, mustPgUUID(id))
	}
	if _, err = q.LockProjectReleaseStageDeployments(ctx, tx, appIDs); err != nil {
		return err
	}
	_, err = q.LockProjectReleaseStagePolicies(ctx, tx, sqlc.LockProjectReleaseStagePoliciesParams{AppIds: appIDs, Scope: environment})
	return err
}
func checkGraphVerificationDB(ctx context.Context, db sqlc.DBTX, r graphVerificationRecord, captures ...map[string][]VerificationRunAdmission) error {
	if err := graphVerificationActive(r.Report); err != nil {
		return err
	}
	snapshot, err := releaseStageSnapshotPG(ctx, db, r.AccountID, r.ProjectID, r.Report.Environment, r.Report.Manifest.RollbackReleaseID)
	if err != nil {
		return err
	}
	if err = validateGraphVerificationSnapshot(snapshot, r.Report.Manifest); err != nil {
		return err
	}
	for _, child := range r.Report.Members {
		row, err := sqlc.New().ReadBindingVerificationRun(ctx, db, sqlc.ReadBindingVerificationRunParams{ID: mustPgUUID(child.ID), AccountID: mustPgUUID(r.AccountID), AppID: mustPgUUID(child.Policy.AppID)})
		if err != nil {
			return mapErr(err)
		}
		stored, err := decodeVerificationRun(row)
		if err != nil {
			return err
		}
		if stored.Report.GraphRunID != r.Report.ID || !graphMemberAdmissionsMatch(stored, child.App, captures, len(r.Report.Members)) {
			return ErrConflict
		}
		if err = graphMemberReviewActive(stored.Report); err != nil {
			return err
		}
		if err = checkVerificationRunAuthorityDB(ctx, db, r.AccountID, child.Policy.AppID, stored); err != nil {
			return err
		}
	}
	return nil
}
func lockVerificationMemberGraph(ctx context.Context, tx pgx.Tx, account, app, id string, validate bool) error {
	q := sqlc.New()
	row, err := q.ReadBindingVerificationRun(ctx, tx, sqlc.ReadBindingVerificationRunParams{ID: mustPgUUID(id), AccountID: mustPgUUID(account), AppID: mustPgUUID(app)})
	if err != nil {
		return mapErr(err)
	}
	if !row.GraphRunID.Valid {
		return nil
	}
	parent, err := q.LockProjectBindingVerificationRun(ctx, tx, sqlc.LockProjectBindingVerificationRunParams{ID: row.GraphRunID, AccountID: mustPgUUID(account)})
	if err != nil {
		return mapErr(err)
	}
	r, err := graphVerificationSQL(parent)
	if err != nil {
		return err
	}
	if err = lockGraphVerificationCatalog(ctx, tx, account, r.ProjectID, r.Report.Environment); err != nil {
		return err
	}
	if validate {
		return checkGraphVerificationDB(ctx, tx, r)
	}
	return nil
}
func (s *PgStore) CreateProjectBindingVerificationRun(ctx context.Context, account, project string, in GraphVerificationCreate) (api.ProjectBindingVerificationRun, error) {
	if err := validateGraphVerificationCreate(account, in); err != nil {
		return api.ProjectBindingVerificationRun{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.ProjectBindingVerificationRun{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	old, err := q.ReadProjectBindingVerificationRun(ctx, tx, sqlc.ReadProjectBindingVerificationRunParams{ID: mustPgUUID(in.Report.ID), AccountID: mustPgUUID(account), ProjectID: mustPgUUID(project), Environment: in.Report.Environment})
	if err == nil {
		r, err := graphVerificationSQL(old)
		if err != nil {
			return api.ProjectBindingVerificationRun{}, err
		}
		if api.BindingVerificationConcurrency(r.Report.MaxConcurrency) != api.BindingVerificationConcurrency(in.Report.MaxConcurrency) || r.Report.ReviewDigest != in.Report.ReviewDigest {
			return api.ProjectBindingVerificationRun{}, ErrConflict
		}
		return publicGraphVerification(r), nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return api.ProjectBindingVerificationRun{}, mapErr(err)
	}
	if err = lockGraphVerificationCatalog(ctx, tx, account, project, in.Report.Environment); err != nil {
		return api.ProjectBindingVerificationRun{}, err
	}
	// A concurrent creator may have committed while this transaction waited.
	old, err = q.ReadProjectBindingVerificationRun(ctx, tx, sqlc.ReadProjectBindingVerificationRunParams{ID: mustPgUUID(in.Report.ID), AccountID: mustPgUUID(account), ProjectID: mustPgUUID(project), Environment: in.Report.Environment})
	if err == nil {
		r, err := graphVerificationSQL(old)
		if err != nil {
			return api.ProjectBindingVerificationRun{}, err
		}
		if api.BindingVerificationConcurrency(r.Report.MaxConcurrency) != api.BindingVerificationConcurrency(in.Report.MaxConcurrency) || r.Report.ReviewDigest != in.Report.ReviewDigest {
			return api.ProjectBindingVerificationRun{}, ErrConflict
		}
		return publicGraphVerification(r), nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return api.ProjectBindingVerificationRun{}, mapErr(err)
	}
	snapshot, err := releaseStageSnapshotPG(ctx, tx, account, project, in.Report.Environment, in.Report.Manifest.RollbackReleaseID)
	if err != nil {
		return api.ProjectBindingVerificationRun{}, err
	}
	if err = validateGraphVerificationSnapshot(snapshot, in.Report.Manifest); err != nil {
		return api.ProjectBindingVerificationRun{}, err
	}
	if err = q.PruneProjectBindingVerificationRuns(ctx, tx, mustPgUUID(project)); err != nil {
		return api.ProjectBindingVerificationRun{}, err
	}
	if err = q.PruneProjectBindingVerificationRunReceipts(ctx, tx, mustPgUUID(project)); err != nil {
		return api.ProjectBindingVerificationRun{}, err
	}
	count, err := q.CountActiveProjectBindingVerificationRuns(ctx, tx, sqlc.CountActiveProjectBindingVerificationRunsParams{ProjectID: mustPgUUID(project), Environment: in.Report.Environment})
	if err != nil {
		return api.ProjectBindingVerificationRun{}, err
	}
	if count >= int64(api.ProjectBindingVerificationRunMaxActive) {
		return api.ProjectBindingVerificationRun{}, &VerificationRunCapacityError{Limit: api.ProjectBindingVerificationRunMaxActive, Observed: int(count)}
	}
	in.Report.Status = "pending"
	r := graphVerificationRecord{AccountID: account, ProjectID: project, Report: in.Report}
	raw, err := json.Marshal(r)
	if err != nil {
		return api.ProjectBindingVerificationRun{}, err
	}
	err = q.InsertProjectBindingVerificationRun(ctx, tx, sqlc.InsertProjectBindingVerificationRunParams{ID: mustPgUUID(r.Report.ID), AccountID: mustPgUUID(account), ProjectID: mustPgUUID(project), Environment: r.Report.Environment, Snapshot: raw, CreatedAt: pgtype.Timestamptz{Time: r.Report.CreatedAt, Valid: true}, ExpiresAt: pgtype.Timestamptz{Time: r.Report.ExpiresAt, Valid: true}})
	if err != nil {
		return api.ProjectBindingVerificationRun{}, mapErr(err)
	}
	for _, slug := range graphVerificationMemberNames(in.Report) {
		child := in.Members[slug]
		_, err = q.ReadBindingVerificationRun(ctx, tx, sqlc.ReadBindingVerificationRunParams{ID: mustPgUUID(child.Report.ID), AccountID: mustPgUUID(account), AppID: mustPgUUID(child.Report.Policy.AppID)})
		if err == nil {
			return api.ProjectBindingVerificationRun{}, ErrConflict
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return api.ProjectBindingVerificationRun{}, mapErr(err)
		}
		if _, err = createBindingVerificationRunTx(ctx, tx, account, child.Report.Policy.AppID, child); err != nil {
			return api.ProjectBindingVerificationRun{}, err
		}
		count, err := q.LinkProjectBindingVerificationMember(ctx, tx, sqlc.LinkProjectBindingVerificationMemberParams{ID: mustPgUUID(child.Report.ID), GraphRunID: pgtype.UUID{Bytes: mustPgUUID(r.Report.ID).Bytes, Valid: true}})
		if err != nil {
			return api.ProjectBindingVerificationRun{}, err
		}
		if count != 1 {
			return api.ProjectBindingVerificationRun{}, ErrConflict
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return api.ProjectBindingVerificationRun{}, mapErr(err)
	}
	return publicGraphVerification(r), nil
}
func (s *PgStore) GetProjectBindingVerificationRun(ctx context.Context, account, project, environment, id string) (api.ProjectBindingVerificationRun, error) {
	if !canonicalStateUUID(id) {
		return api.ProjectBindingVerificationRun{}, ErrInvalidArgument
	}
	row, err := sqlc.New().ReadProjectBindingVerificationRun(ctx, s.pool, sqlc.ReadProjectBindingVerificationRunParams{ID: mustPgUUID(id), AccountID: mustPgUUID(account), ProjectID: mustPgUUID(project), Environment: environment})
	if err != nil {
		return api.ProjectBindingVerificationRun{}, mapErr(err)
	}
	r, err := graphVerificationSQL(row)
	return publicGraphVerification(r), err
}
func (s *PgStore) graphVerificationTx(ctx context.Context, account, project, environment, id string, action func(pgx.Tx, graphVerificationRecord) error) error {
	if !canonicalStateUUID(id) {
		return ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	row, err := sqlc.New().LockProjectBindingVerificationRun(ctx, tx, sqlc.LockProjectBindingVerificationRunParams{ID: mustPgUUID(id), AccountID: mustPgUUID(account)})
	if err != nil {
		return mapErr(err)
	}
	r, err := graphVerificationSQL(row)
	if err != nil {
		return err
	}
	if r.ProjectID != project || r.Report.Environment != environment {
		return ErrNotFound
	}
	if err = action(tx, r); err != nil {
		return err
	}
	return mapErr(tx.Commit(ctx))
}
func (s *PgStore) CheckProjectBindingVerificationRun(ctx context.Context, account, project, environment, id string, captures ...map[string][]VerificationRunAdmission) error {
	return s.graphVerificationTx(ctx, account, project, environment, id, func(tx pgx.Tx, r graphVerificationRecord) error {
		if err := lockGraphVerificationCatalog(ctx, tx, account, project, environment); err != nil {
			return err
		}
		return checkGraphVerificationDB(ctx, tx, r, captures...)
	})
}
func (s *PgStore) SetProjectBindingVerificationRunStatus(ctx context.Context, account, project, environment, id, status string) error {
	if status != "passed" && status != "cancelled" {
		return ErrInvalidArgument
	}
	return s.graphVerificationTx(ctx, account, project, environment, id, func(tx pgx.Tx, r graphVerificationRecord) error {
		if status == "passed" {
			if err := lockGraphVerificationCatalog(ctx, tx, account, project, environment); err != nil {
				return err
			}
			if err := checkGraphVerificationDB(ctx, tx, r); err != nil {
				return err
			}
			for _, child := range r.Report.Members {
				row, err := sqlc.New().ReadBindingVerificationRun(ctx, tx, sqlc.ReadBindingVerificationRunParams{ID: mustPgUUID(child.ID), AccountID: mustPgUUID(account), AppID: mustPgUUID(child.Policy.AppID)})
				if err != nil {
					return mapErr(err)
				}
				if row.Status != "passed" {
					return ErrConflict
				}
			}
		}
		return sqlc.New().SetProjectBindingVerificationRunStatus(ctx, tx, sqlc.SetProjectBindingVerificationRunStatusParams{ID: mustPgUUID(id), Status: status})
	})
}
