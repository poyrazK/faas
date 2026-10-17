package state

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func readPromotionAdmissionCountsDB(ctx context.Context, db sqlc.DBTX, account, id string) (promotionAdmissionCounts, error) {
	r, err := sqlc.New().ReadPromotionAdmissionCounts(ctx, db, sqlc.ReadPromotionAdmissionCountsParams{AccountID: account, PromotionID: id})
	return promotionAdmissionCounts{r.FleetActive, r.AccountActive, r.FleetQueued, r.AccountQueued, r.Older}, mapErr(err)
}
func consumePromotionAdmissionApprovalDB(ctx context.Context, tx pgx.Tx, p ProjectEnvironmentPromotion) (*ProjectEnvironmentApproval, error) {
	r, err := sqlc.New().ConsumePromotionAdmissionApproval(ctx, tx, sqlc.ConsumePromotionAdmissionApprovalParams{AccountID: p.AccountID, ProjectSlug: p.ProjectSlug, Environment: p.ToEnvironment, PlanTokenHash: p.Approval.PlanTokenHash, ApprovalTokenHash: p.Approval.ApprovalTokenHash})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, &PromotionApprovalError{}
	}
	if err != nil {
		return nil, mapErr(err)
	}
	return &ProjectEnvironmentApproval{ID: r.ID, AccountID: p.AccountID, ProjectSlug: p.ProjectSlug, EnvironmentSlug: p.ToEnvironment, TokenKind: r.TokenKind, ConsumedAt: &r.ConsumedAt.Time}, nil
}

func validateQueuedPromotionDB(ctx context.Context, tx pgx.Tx, p ProjectEnvironmentPromotion) error {
	if p.TargetReleaseSetID != "" || p.TargetConfigVersion != 0 || p.RestoredTargetReleaseSetID != "" {
		return ErrConflict
	}
	if err := lockPromotionWorkloadEnvironmentsTx(ctx, tx, p.ID); err != nil {
		return err
	}
	for _, scope := range []struct{ environment, id string }{{p.FromEnvironment, p.SourceReleaseSetID}, {p.ToEnvironment, p.PreviousTargetReleaseSetID}} {
		active, err := activeProjectReleaseSetIDTx(ctx, tx, p.ProjectID, scope.environment)
		if err != nil {
			return err
		}
		if active != scope.id {
			return ErrConflict
		}
		graph, err := readProjectReleaseSetTx(ctx, tx, p.AccountID, p.ProjectID, scope.environment, scope.id)
		if err != nil {
			return err
		}
		if !graph.Active || time.Duration(graph.TTLSeconds)*time.Second < api.ProjectPromotionHealthMinRetention {
			return ErrConflict
		}
	}
	if err := verifyProjectEnvironmentPromotionQualificationTx(ctx, tx, p); err != nil {
		return err
	}
	captured, err := readPromotionReleaseCaptureDB(ctx, tx, p.AccountID, p.ID)
	if err != nil {
		return err
	}
	if captured == nil {
		return ErrConflict
	}
	if err = lockPromotionReleaseInputsDB(ctx, tx, p); err != nil {
		return err
	}
	p.Workers, p.Migrate, p.ResolveServiceBindings = len(captured.workers()) > 0, captured.Migration != nil, captured.DestinationConfig != nil
	current, err := promotionReleaseInputsDB(ctx, tx, p)
	if err != nil {
		return err
	}
	view, err := current.prepare(p, promotionRuntimeDB(ctx, tx))
	if err != nil {
		return err
	}
	if view == nil || view.Hash != captured.Hash {
		return ErrConflict
	}
	flags, err := readPromotionFeatureFlags(ctx, tx, p.AccountID, p.ID)
	if err != nil {
		return err
	}
	source, err := readCloneFeatureFlagsTx(ctx, tx, p.AccountID, p.ProjectID, p.FromEnvironment)
	if err != nil {
		return err
	}
	target, err := readCloneFeatureFlagsTx(ctx, tx, p.AccountID, p.ProjectID, p.ToEnvironment)
	if err != nil {
		return err
	}
	next, err := preparePromotionFeatureFlagActivation(flags, source, target, false, false)
	if err != nil {
		return err
	}
	for _, id := range flagCustomerIDs(next.Config) {
		if _, err = sqlc.New().LockPromotionFeatureFlagCustomer(ctx, tx, sqlc.LockPromotionFeatureFlagCustomerParams{AccountID: mustPgUUID(p.AccountID), TenantID: mustPgUUID(id)}); err != nil {
			return mapErr(err)
		}
	}
	return nil
}
func queueIntentConflict(err error) bool {
	return errors.Is(err, ErrConflict) || errors.Is(err, ErrNotFound) || errors.Is(err, ErrInvalidArgument) || errors.Is(err, ErrReleaseConfigurationTransferUnsupported) || errors.Is(err, ErrPromotionWorkerNotReady)
}

func (s *PgStore) DispatchProjectEnvironmentPromotion(ctx context.Context, account, id, token string, generation int64) (*api.ProjectPromotionHealth, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	project, err := q.PromotionHealthProject(ctx, tx, sqlc.PromotionHealthProjectParams{AccountID: account, PromotionID: id})
	if err != nil {
		return nil, mapErr(err)
	}
	if err = lockProjectForReleaseSetTx(ctx, tx, account, project); err != nil {
		return nil, err
	}
	// Match creation's project -> admission -> promotion lock order. Counts use
	// the fresh READ COMMITTED snapshot after another dispatch has committed.
	if err = q.LockPromotionAdmission(ctx, tx); err != nil {
		return nil, mapErr(err)
	}
	row, err := q.LockPromotionHealthOwner(ctx, tx, sqlc.LockPromotionHealthOwnerParams{AccountID: account, PromotionID: id})
	if err != nil {
		return nil, mapErr(err)
	}
	current, err := q.PromotionHealthLeaseCurrent(ctx, tx, sqlc.PromotionHealthLeaseCurrentParams{AccountID: account, PromotionID: id, LeaseToken: token})
	if err != nil {
		return nil, mapErr(err)
	}
	h, err := readPromotionHealthDB(ctx, tx, account, id)
	if err != nil {
		return nil, err
	}
	if !current || h == nil || h.Generation != generation || h.Phase != "queued" || h.Admission == nil || row.Status != "running" {
		return nil, ErrConflict
	}
	now := time.Now().UTC()
	next := queuedPromotionDecision(h, "", now)
	if next.Generation == h.Generation {
		counts, err := readPromotionAdmissionCountsDB(ctx, tx, account, id)
		if err != nil {
			return nil, err
		}
		if counts.waitingReason() != "" {
			return clonePromotionHealth(h), nil
		}
		if err = validateQueuedPromotionDB(ctx, tx, promotionFromHealthOwner(row)); err != nil {
			if !queueIntentConflict(err) {
				return nil, err
			}
			next = queuedPromotionDecision(h, "conflict", now)
		} else {
			// Validation cannot extend either the queue wait or the claim ownership.
			now = time.Now().UTC()
			next = queuedPromotionDecision(h, "", now)
			current, err = q.PromotionHealthLeaseCurrent(ctx, tx, sqlc.PromotionHealthLeaseCurrentParams{AccountID: account, PromotionID: id, LeaseToken: token})
			if err != nil {
				return nil, mapErr(err)
			}
			if !current {
				return nil, ErrConflict
			}
			if next.Generation == h.Generation {
				next = startQueuedPromotion(h, now)
				if err = q.AuthorizePromotionQueueDispatch(ctx, tx, sqlc.AuthorizePromotionQueueDispatchParams{PromotionID: id, Generation: generation, LeaseToken: token}); err != nil {
					return nil, mapErr(err)
				}
			}
		}
	}
	current, err = q.PromotionHealthLeaseCurrent(ctx, tx, sqlc.PromotionHealthLeaseCurrentParams{AccountID: account, PromotionID: id, LeaseToken: token})
	if err != nil {
		return nil, mapErr(err)
	}
	if !current {
		return nil, ErrConflict
	}

	if err = savePromotionHealthDB(ctx, tx, id, h.Generation, next); err != nil {
		return nil, err
	}
	status, verification, message := promotionHealthResult(next)
	if err = q.UpdatePromotionHealthResult(ctx, tx, sqlc.UpdatePromotionHealthResultParams{PromotionID: id, Status: status, VerificationStatus: verification, ErrorMessage: message}); err != nil {
		return nil, mapErr(err)
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return clonePromotionHealth(next), nil
}
