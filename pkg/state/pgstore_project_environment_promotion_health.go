package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) ClaimProjectEnvironmentPromotionHealth(ctx context.Context, accountID, promotionID, token string) (bool, error) {
	return s.claimPromotionHealth(ctx, accountID, promotionID, token)
}

func (s *PgStore) ReleaseProjectEnvironmentPromotionHealth(ctx context.Context, promotionID, token string) error {
	return s.finishPromotionHealth(ctx, promotionID, token)
}

func readPromotionHealthDB(ctx context.Context, db sqlc.DBTX, accountID, promotionID string) (*api.ProjectPromotionHealth, error) {
	raw, err := sqlc.New().ReadPromotionHealth(ctx, db, sqlc.ReadPromotionHealthParams{AccountID: accountID, PromotionID: promotionID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, mapErr(err)
	}
	var h api.ProjectPromotionHealth
	if json.Unmarshal(raw, &h) != nil || ValidatePromotionHealthPolicy(&h.Policy) != nil {
		return nil, ErrConflict
	}
	return &h, nil
}

func (s *PgStore) ProjectEnvironmentPromotionHealth(ctx context.Context, accountID, promotionID string) (*api.ProjectPromotionHealth, error) {
	h, err := readPromotionHealthDB(ctx, s.pool, accountID, promotionID)
	if err != nil || h == nil {
		return h, err
	}
	h.Recovery, err = s.ProjectPromotionMigrationRecovery(ctx, accountID, promotionID)
	return h, err
}

func (s *PgStore) PendingProjectEnvironmentPromotionHealth(ctx context.Context) ([]PromotionHealthJob, error) {
	rows, err := sqlc.New().PendingPromotionHealth(ctx, s.pool, api.ProjectPromotionHealthBatchSize)
	if err != nil {
		return nil, mapErr(err)
	}
	jobs := make([]PromotionHealthJob, 0, len(rows))
	for _, r := range rows {
		jobs = append(jobs, PromotionHealthJob{r.PromotionID, r.AccountID, r.ProjectSlug, r.ToEnvironment})
	}
	return jobs, nil
}

func savePromotionHealthDB(ctx context.Context, db sqlc.DBTX, promotionID string, previous int64, h *api.ProjectPromotionHealth) error {
	if err := savePromotionControlDB(ctx, db, promotionID, h.Control); err != nil {
		return err
	}
	raw, err := encodePromotionHealth(h)
	if err != nil {
		return err
	}
	changed, err := sqlc.New().SavePromotionHealth(ctx, db, sqlc.SavePromotionHealthParams{PromotionID: promotionID, PreviousGeneration: previous, Generation: h.Generation, Phase: h.Phase, Payload: raw})
	if err != nil {
		return mapErr(err)
	}
	if changed != 1 {
		return ErrConflict
	}
	return nil
}

func (s *PgStore) AdvanceProjectEnvironmentPromotionHealth(ctx context.Context, accountID, promotionID string, o PromotionHealthObservation) (*api.ProjectPromotionHealth, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	projectID, err := q.PromotionHealthProject(ctx, tx, sqlc.PromotionHealthProjectParams{AccountID: accountID, PromotionID: promotionID})
	if err != nil {
		return nil, mapErr(err)
	}
	if err = lockProjectForReleaseSetTx(ctx, tx, accountID, projectID); err != nil {
		return nil, err
	}
	r, err := q.LockPromotionHealthOwner(ctx, tx, sqlc.LockPromotionHealthOwnerParams{AccountID: accountID, PromotionID: promotionID})
	if err != nil {
		return nil, mapErr(err)
	}
	p := promotionFromHealthOwner(r)
	if o.LeaseToken != "" {
		current, leaseErr := q.PromotionHealthLeaseCurrent(ctx, tx, sqlc.PromotionHealthLeaseCurrentParams{AccountID: accountID, PromotionID: promotionID, LeaseToken: o.LeaseToken})
		if leaseErr != nil {
			return nil, mapErr(leaseErr)
		}
		if !current {
			return nil, ErrConflict
		}
	}
	h, err := readPromotionHealthDB(ctx, tx, accountID, promotionID)
	if err != nil || h == nil {
		return nil, err
	}
	active, err := activeProjectReleaseSetIDTx(ctx, tx, projectID, p.ToEnvironment)
	if err != nil {
		return nil, err
	}
	expected := p.PreviousTargetReleaseSetID
	if p.TargetReleaseSetID != "" {
		expected = p.TargetReleaseSetID
	}
	if p.RestoredTargetReleaseSetID != "" {
		expected = p.RestoredTargetReleaseSetID
	}
	if active != expected {
		o.Action = "conflict"
	}
	if p.Status != "running" && promotionHealthPending(h.Phase) {
		o.Action = "fail"
	}
	if h.Phase != "queued" && len(api.ProjectPromotionWorkers(&h.Policy)) > 0 && h.Generation == o.Generation && (o.Action == "" || o.Action == "complete") {
		h = clonePromotionHealth(h)
		if err := observePromotionWorkerDB(ctx, tx, p, h, time.Now().UTC()); err != nil {
			if !errors.Is(err, ErrConflict) {
				return nil, err
			}
			o.Action = "conflict"
		}
		if o.Action == "complete" && !promotionWorkersReady(h) {
			o.Action = ""
		}
	}
	if o.Action == "complete" {
		if guardPromotionMigrationPublishDB(ctx, tx, p, h) != nil {
			o.Action = "conflict"
		}
		if err = lockPromotionWorkloadEnvironmentsTx(ctx, tx, p.ID); err != nil {
			return nil, err
		}
		release, readErr := readProjectReleaseSetTx(ctx, tx, accountID, projectID, p.ToEnvironment, active)
		if readErr != nil {
			return nil, readErr
		}
		if verifyProjectEnvironmentPromotionConfigTx(ctx, tx, p, false) != nil || guardPromotionReleaseConfigurationDB(ctx, tx, p, release.Members, false, true) != nil {
			o.Action = "conflict"
		}
		if promotionWorkloadActivationsTx(ctx, tx, p, release.Members, false, true) != nil || promotionFeatureFlagsActivationTx(ctx, tx, p, false, true) != nil {
			o.Action = "conflict"
		}
	}
	next, err := decidePromotionHealth(h, p, o, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	if next.Generation != h.Generation {
		if err = savePromotionHealthDB(ctx, tx, promotionID, h.Generation, next); err != nil {
			return nil, err
		}
		status, verification, message := promotionHealthResult(next)
		if err = q.UpdatePromotionHealthResult(ctx, tx, sqlc.UpdatePromotionHealthResultParams{PromotionID: promotionID, Status: status, VerificationStatus: verification, ErrorMessage: message}); err != nil {
			return nil, mapErr(err)
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return next, nil
}

func guardPromotionHealthPublishDB(ctx context.Context, tx pgx.Tx, p ProjectEnvironmentPromotion) (*api.ProjectPromotionHealth, error) {
	h, err := readPromotionHealthDB(ctx, tx, p.AccountID, p.ID)
	if err != nil || h == nil || p.TargetReleaseSetID != "" {
		return h, err
	}
	if err := guardPromotionWorkerReadyDB(ctx, tx, p, h, false); err != nil {
		return nil, err
	}
	if err := refreshPromotionWorkerBacklogDB(ctx, tx, h); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	if p.Status != "running" || h.Phase != "cutover" || !now.Before(h.Deadline) || h.Verdict != "healthy" || !promotionHealthBaselineHealthy(h, now) {
		return nil, ErrConflict
	}
	if err := guardPromotionMigrationPublishDB(ctx, tx, p, h); err != nil {
		return nil, err
	}
	return h, nil
}

func insertPromotionHealthDB(ctx context.Context, tx pgx.Tx, p ProjectEnvironmentPromotion) error {
	h, err := newPromotionHealth(p, time.Now().UTC())
	if err != nil || h == nil {
		return err
	}
	counts, err := readPromotionAdmissionCountsDB(ctx, tx, p.AccountID, p.ID)
	if err != nil {
		return err
	}
	if err = admitPromotionHealth(h, counts, time.Now().UTC()); err != nil {
		return err
	}
	raw, err := encodePromotionHealth(h)
	if err != nil {
		return err
	}
	return mapErr(sqlc.New().InsertPromotionHealth(ctx, tx, sqlc.InsertPromotionHealthParams{PromotionID: p.ID, Generation: h.Generation, Phase: h.Phase, Payload: raw}))
}

func promotionFromHealthOwner(r sqlc.LockPromotionHealthOwnerRow) ProjectEnvironmentPromotion {
	return ProjectEnvironmentPromotion{ID: r.ID, AccountID: r.AccountID, ProjectID: r.ProjectID, ProjectSlug: r.ProjectSlug, FromEnvironment: r.FromEnvironment, ToEnvironment: r.ToEnvironment, Status: r.Status, ReleaseGraphMode: r.ReleaseGraphMode,
		SourceReleaseSetID: r.SourceReleaseID, TargetReleaseSetID: r.TargetReleaseID, PreviousTargetReleaseSetID: r.PreviousReleaseID, RestoredTargetReleaseSetID: r.RestoredReleaseID,
		SourceQualificationID: r.QualificationID, ReleaseTTLSeconds: int(r.ReleaseTtlSeconds), RollbackStatus: r.RollbackStatus, SyncConfig: r.SyncConfig, SourceConfigHash: r.SourceConfigHash, PreviousTargetConfigHash: r.PreviousTargetConfigHash,
		TargetConfigVersion: r.TargetConfigVersion, RollbackConfigVersion: r.RollbackConfigVersion, SourceConfigSnapshot: r.SourceConfigSnapshot, PreviousTargetConfigSnapshot: r.PreviousTargetConfigSnapshot}
}
