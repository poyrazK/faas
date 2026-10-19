package state

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"time"
)

func decodeVerificationRun(row sqlc.BindingVerificationRun) (verificationRunRecord, error) {
	var r verificationRunRecord
	if err := json.Unmarshal(row.Snapshot, &r); err != nil {
		return r, err
	}
	if row.GraphRunID.Valid != (r.Report.GraphRunID != "") || row.GraphRunID.Valid && row.GraphRunID.Bytes != mustPgUUID(r.Report.GraphRunID).Bytes {
		return r, ErrConflict
	}
	r.Report.Status = row.Status
	r.Report.CreatedAt = row.CreatedAt.Time
	r.Report.ExpiresAt = row.ExpiresAt.Time
	return r, nil
}
func loadVerificationRunResources(ctx context.Context, db sqlc.DBTX, r *verificationRunRecord) error {
	rows, err := sqlc.New().ReadBindingVerificationRunSteps(ctx, db, mustPgUUID(r.Report.ID))
	if err != nil {
		return mapErr(err)
	}
	for _, row := range rows {
		i := int(row.StepIndex)
		if i < 0 || i >= len(r.Report.Steps) {
			return ErrConflict
		}
		r.Report.Steps[i].ResourceID = pgUUIDString(row.ResourceID)
		r.Report.Steps[i].ResourceKind = row.ResourceKind
		r.Report.Steps[i].Owned = true
		r.Report.Steps[i].Status = "admitted"
	}
	return nil
}
func checkVerificationRunDB(ctx context.Context, db sqlc.DBTX, account, app string, r verificationRunRecord) error {
	if err := verificationRunActive(r.Report); err != nil {
		return err
	}
	return checkVerificationRunAuthorityDB(ctx, db, account, app, r)
}
func checkVerificationRunAuthorityDB(ctx context.Context, db sqlc.DBTX, account, app string, r verificationRunRecord) error {
	authority, err := sqlc.New().ReadBindingVerificationRunAuthority(ctx, db, sqlc.ReadBindingVerificationRunAuthorityParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), DeploymentID: mustPgUUID(r.Report.DeploymentID), Scope: r.Report.Scope})
	if err != nil {
		return mapErr(err)
	}
	artifact, err := verificationArtifactJSON(authority.Artifact)
	if err != nil {
		return err
	}
	policy, err := decodeBindingReleasePolicy(authority.Policy, app, r.Report.Scope)
	if err != nil {
		return err
	}
	if artifact != r.Artifact || policy.Revision != r.Report.Policy.Revision || policy.Mode != "enforce" || !authority.ChangedAt.Time.Equal(*r.Report.Inventory.RuntimeFreshness.ConfigChangedAt) {
		return ErrConflict
	}
	for _, admission := range r.Admissions {
		if admission.Queue != nil {
			snap, err := queueCanarySnapshotDB(ctx, db, account, app, r.Report.DeploymentID, admission.Queue.Name)
			if err != nil {
				return err
			}
			if !sameQueueCanarySnapshot(snap, *admission.Queue) {
				return ErrConflict
			}
		}
	}
	return nil
}
func (s *PgStore) CreateBindingVerificationRun(ctx context.Context, account, app string, in VerificationRunCreate) (api.BindingVerificationRun, error) {
	if err := validateVerificationRunCreate(account, app, in); err != nil {
		return api.BindingVerificationRun{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.BindingVerificationRun{}, mapErr(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	out, err := createBindingVerificationRunTx(ctx, tx, account, app, in)
	if err != nil {
		return out, err
	}
	if err = tx.Commit(ctx); err != nil {
		return out, mapErr(err)
	}
	return out, nil
}
func createBindingVerificationRunTx(ctx context.Context, tx pgx.Tx, account, app string, in VerificationRunCreate) (api.BindingVerificationRun, error) {
	var err error
	q := sqlc.New()
	if _, err = q.LockQueueCanaryApp(ctx, tx, sqlc.LockQueueCanaryAppParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app)}); err != nil {
		return api.BindingVerificationRun{}, mapErr(err)
	}
	old, err := q.ReadBindingVerificationRun(ctx, tx, sqlc.ReadBindingVerificationRunParams{ID: mustPgUUID(in.Report.ID), AccountID: mustPgUUID(account), AppID: mustPgUUID(app)})
	if err == nil {
		r, err := decodeVerificationRun(old)
		if err != nil {
			return api.BindingVerificationRun{}, err
		}
		if api.BindingVerificationConcurrency(r.Report.MaxConcurrency) != api.BindingVerificationConcurrency(in.Report.MaxConcurrency) || r.Report.ReviewDigest != in.Report.ReviewDigest || r.Report.QueuePublish != in.Report.QueuePublish || r.Report.ObjectStorageWrite != in.Report.ObjectStorageWrite {
			return api.BindingVerificationRun{}, ErrConflict
		}
		if err = loadVerificationRunResources(ctx, tx, &r); err != nil {
			return api.BindingVerificationRun{}, err
		}
		return publicVerificationRun(r), nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return api.BindingVerificationRun{}, mapErr(err)
	}
	if err = q.DeleteExpiredBindingVerificationRunReceipts(ctx, tx, mustPgUUID(app)); err != nil {
		return api.BindingVerificationRun{}, mapErr(err)
	}
	count, err := q.CountActiveBindingVerificationRuns(ctx, tx, mustPgUUID(app))
	if err != nil {
		return api.BindingVerificationRun{}, mapErr(err)
	}
	if count >= int64(api.BindingVerificationRunMaxActive) {
		return api.BindingVerificationRun{}, &VerificationRunCapacityError{Limit: api.BindingVerificationRunMaxActive, Observed: int(count)}
	}
	a, err := q.ReadBindingVerificationRunAuthority(ctx, tx, sqlc.ReadBindingVerificationRunAuthorityParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), DeploymentID: mustPgUUID(in.Report.DeploymentID), Scope: in.Report.Scope})
	if err != nil {
		return api.BindingVerificationRun{}, mapErr(err)
	}
	artifact, err := verificationArtifactJSON(a.Artifact)
	if err != nil {
		return api.BindingVerificationRun{}, err
	}
	r := cloneVerificationRecord(verificationRunRecord{Report: in.Report, Admissions: in.Admissions, Artifact: artifact})
	r.Report.Status = "pending"
	if err = checkVerificationRunAuthorityDB(ctx, tx, account, app, r); err != nil {
		return api.BindingVerificationRun{}, err
	}
	for i, step := range r.Report.Steps {
		if step.Plan.Type == api.BindingTypeQueue && step.Plan.RequiredEvidence == api.QueueEvidenceHandlerCompletion {
			snap, err := queueCanarySnapshotDB(ctx, tx, account, app, r.Report.DeploymentID, step.Plan.Binding)
			if err != nil {
				return api.BindingVerificationRun{}, err
			}
			r.Admissions[i].Queue = &snap
		}
	}
	raw, err := json.Marshal(r)
	if err != nil {
		return api.BindingVerificationRun{}, err
	}
	err = q.InsertBindingVerificationRun(ctx, tx, sqlc.InsertBindingVerificationRunParams{ID: mustPgUUID(r.Report.ID), AccountID: mustPgUUID(account), AppID: mustPgUUID(app), DeploymentID: mustPgUUID(r.Report.DeploymentID), Scope: r.Report.Scope, Snapshot: raw, CreatedAt: pgtype.Timestamptz{Time: r.Report.CreatedAt, Valid: true}, ExpiresAt: pgtype.Timestamptz{Time: r.Report.ExpiresAt, Valid: true}})
	if err != nil {
		return api.BindingVerificationRun{}, mapErr(err)
	}
	return publicVerificationRun(r), nil
}
func (s *PgStore) GetBindingVerificationRun(ctx context.Context, account, app, id string) (api.BindingVerificationRun, error) {
	if !canonicalStateUUID(id) {
		return api.BindingVerificationRun{}, ErrInvalidArgument
	}
	row, err := sqlc.New().ReadBindingVerificationRun(ctx, s.pool, sqlc.ReadBindingVerificationRunParams{ID: mustPgUUID(id), AccountID: mustPgUUID(account), AppID: mustPgUUID(app)})
	if err != nil {
		return api.BindingVerificationRun{}, mapErr(err)
	}
	r, err := decodeVerificationRun(row)
	if err != nil {
		return api.BindingVerificationRun{}, err
	}
	if err = loadVerificationRunResources(ctx, s.pool, &r); err != nil {
		return api.BindingVerificationRun{}, err
	}
	return observeVerificationRun(ctx, s, s, account, app, publicVerificationRun(r))
}
func (s *PgStore) withVerificationRunTx(ctx context.Context, account, app, id string, checkGraph bool, run func(pgx.Tx, *verificationRunRecord) error) error {
	if !canonicalStateUUID(id) {
		return ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return mapErr(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = lockVerificationMemberGraph(ctx, tx, account, app, id, checkGraph); err != nil {
		return err
	}
	q := sqlc.New()
	if _, err = q.LockQueueCanaryApp(ctx, tx, sqlc.LockQueueCanaryAppParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app)}); err != nil {
		return mapErr(err)
	}
	row, err := q.LockBindingVerificationRun(ctx, tx, sqlc.LockBindingVerificationRunParams{ID: mustPgUUID(id), AccountID: mustPgUUID(account), AppID: mustPgUUID(app)})
	if err != nil {
		return mapErr(err)
	}
	r, err := decodeVerificationRun(row)
	if err != nil {
		return err
	}
	if err = loadVerificationRunResources(ctx, tx, &r); err != nil {
		return err
	}
	if err = run(tx, &r); err != nil {
		return err
	}
	return mapErr(tx.Commit(ctx))
}
func (s *PgStore) CheckBindingVerificationRun(ctx context.Context, account, app, id string, admissions ...[]VerificationRunAdmission) error {
	return s.withVerificationRunTx(ctx, account, app, id, true, func(tx pgx.Tx, r *verificationRunRecord) error {
		if !verificationRunAdmissionsMatch(*r, admissions) {
			return ErrConflict
		}
		return checkVerificationRunDB(ctx, tx, account, app, *r)
	})
}
func (s *PgStore) SetBindingVerificationRunStatus(ctx context.Context, account, app, id, status string) error {
	if status != "passed" && status != "cancelled" {
		return ErrInvalidArgument
	}
	return s.withVerificationRunTx(ctx, account, app, id, status != "cancelled", func(tx pgx.Tx, r *verificationRunRecord) error {
		if status == "passed" {
			if r.Report.Status == "passed" {
				return nil
			}
			if err := checkVerificationRunDB(ctx, tx, account, app, *r); err != nil {
				return err
			}
		}
		return mapErr(sqlc.New().SetBindingVerificationRunStatus(ctx, tx, sqlc.SetBindingVerificationRunStatusParams{ID: mustPgUUID(id), AccountID: mustPgUUID(account), AppID: mustPgUUID(app), Status: status}))
	})
}
func (s *PgStore) AdmitBindingVerificationRunStep(ctx context.Context, account, app, id string, index int) (api.BindingVerificationRunStep, error) {
	var result api.BindingVerificationRunStep
	err := s.withVerificationRunTx(ctx, account, app, id, true, func(tx pgx.Tx, r *verificationRunRecord) error {
		if err := checkVerificationRunDB(ctx, tx, account, app, *r); err != nil {
			return err
		}
		if index < 0 || index >= len(r.Report.Steps) {
			return ErrInvalidArgument
		}
		result = r.Report.Steps[index]
		if result.ResourceID != "" {
			return nil
		}
		budget, err := verificationBudgetDB(ctx, tx, account, *r)
		if err != nil {
			return err
		}
		if budget.Full {
			return &VerificationRunBudgetError{Limit: budget.MaxConcurrency, Observed: budget.ActiveResources}
		}
		q := sqlc.New()
		admission := r.Admissions[index]
		switch result.Plan.Action {
		case "probe":
			params := *admission.Task
			params.CreatedAt = time.Now().UTC()
			resolved, err := resolveCreateAppTask(params)
			if err != nil {
				return err
			}
			pin, err := json.Marshal(resolved.BindingVerification)
			if err != nil {
				return err
			}
			resource, err := q.CreateBindingVerificationTask(ctx, tx, sqlc.CreateBindingVerificationTaskParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), DeploymentID: mustPgUUID(r.Report.DeploymentID), Command: resolved.Command, TimeoutSeconds: int32(resolved.TimeoutSeconds), MaxOutputBytes: int32(resolved.MaxOutputBytes), CreatedAt: pgtype.Timestamptz{Time: resolved.CreatedAt, Valid: true}, BindingVerification: pin, RequireLiveDeployment: true})
			if err != nil {
				return mapErr(err)
			}
			if err = q.SetVerificationRunTaskDeadline(ctx, tx, sqlc.SetVerificationRunTaskDeadlineParams{ID: resource, ExpiresAt: pgtype.Timestamptz{Time: r.Report.ExpiresAt, Valid: true}}); err != nil {
				return mapErr(err)
			}
			result.ResourceID, result.ResourceKind, result.Owned, result.Status = pgUUIDString(resource), "task", true, "queued"
		case "queue-canary":
			snap, err := queueCanarySnapshotDB(ctx, tx, account, app, r.Report.DeploymentID, result.Plan.Binding)
			if err != nil {
				return err
			}
			if admission.Queue == nil || !sameQueueCanarySnapshot(snap, *admission.Queue) {
				return ErrConflict
			}
			ttl := int(time.Until(r.Report.ExpiresAt) / time.Second)
			if ttl < 1 {
				return ErrConflict
			}
			canary, err := createQueueBindingCanaryTx(ctx, tx, account, app, api.CreateQueueBindingCanaryRequest{DeploymentID: r.Report.DeploymentID, Binding: result.Plan.Binding, TTLSeconds: ttl, PublishConsent: true})
			if err != nil {
				return err
			}
			result.ResourceID, result.ResourceKind, result.Owned, result.Status = canary.ID, "queue", true, canary.Status
		default:
			return ErrConflict
		}
		return mapErr(q.RecordBindingVerificationRunStep(ctx, tx, sqlc.RecordBindingVerificationRunStepParams{RunID: mustPgUUID(id), StepIndex: int32(index), ResourceID: mustPgUUID(result.ResourceID), ResourceKind: result.ResourceKind}))
	})
	return result, err
}
