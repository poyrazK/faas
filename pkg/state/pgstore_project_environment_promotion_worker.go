package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func observePromotionWorkerDB(ctx context.Context, db sqlc.DBTX, p ProjectEnvironmentPromotion, h *api.ProjectPromotionHealth, now time.Time) error {
	if len(api.ProjectPromotionWorkers(&h.Policy)) == 0 {
		return nil
	}
	c, err := readPromotionReleaseCaptureDB(ctx, db, p.AccountID, p.ID)
	if err != nil {
		return err
	}
	if c == nil {
		return ErrConflict
	}
	p.Workers = true
	current, err := promotionReleaseInputsDB(ctx, db, p)
	if err != nil {
		return err
	}
	if api.ProjectPromotionWorkerSetID(current.Worker, current.Workers) != api.ProjectPromotionWorkerSetID(c.Worker, c.Workers) {
		return ErrConflict
	}
	return visitPromotionWorkers(h, func(one *api.ProjectPromotionHealth) error {
		return observePromotionWorkerSingleDB(ctx, db, p, one, now)
	})
}
func observePromotionWorkerSingleDB(ctx context.Context, db sqlc.DBTX, p ProjectEnvironmentPromotion, h *api.ProjectPromotionHealth, now time.Time) error {
	id, err := sqlc.New().PromotionWorkerDeployment(ctx, db, sqlc.PromotionWorkerDeploymentParams{PromotionID: p.ID, AppID: h.Policy.Worker.AppID})
	if err != nil {
		return mapErr(err)
	}
	h.Worker = copyPromotionWorker(h.Policy.Worker)
	h.Worker.DeploymentID = id
	h.Worker.Status = "building"
	if id != "" {
		h.Worker.InstanceID, err = releaseWorkerReadyDB(ctx, db, p.AccountID, h.Worker.AppID, id)
		if err != nil {
			return err
		}
	}
	if h.Worker.InstanceID != "" {
		h.Worker.Status = "ready"
		if p.TargetReleaseSetID != "" {
			h.Worker.Status = "active"
		}
	}
	if h.WorkerHealth == nil {
		h.WorkerHealth = &api.ProjectWorkerHealth{Status: "insufficient_evidence"}
	}
	if h.Phase == "finalizing" {
		return nil
	}
	if err = readWorkerHealthDB(ctx, db, promotionWorkerSample(h, now), now); err != nil {
		return err
	}
	if h.Phase == "restoring" {
		if err = guardPromotionWorkerReadyDB(ctx, db, p, h, true); err != nil {
			if !errors.Is(err, ErrPromotionWorkerNotReady) {
				return err
			}
			h.WorkerHealth.Baseline = api.ProjectWorkerJobSample{}
		}
	}
	return nil
}
func guardPromotionWorkerReadyDB(ctx context.Context, db sqlc.DBTX, p ProjectEnvironmentPromotion, h *api.ProjectPromotionHealth, rollback bool) error {
	for _, one := range promotionWorkerHealthViews(h) {
		if err := guardPromotionWorkerSingleReadyDB(ctx, db, p, one, rollback); err != nil {
			return err
		}
	}
	if h != nil && len(h.Policy.Workers) > 0 {
		return sqlc.New().SetPromotionWorkerSetPublicationGuard(ctx, db, fmt.Sprintf("%s:%d:%t", p.ID, h.Generation, rollback))
	}
	return nil
}
func guardPromotionWorkerSingleReadyDB(ctx context.Context, db sqlc.DBTX, p ProjectEnvironmentPromotion, h *api.ProjectPromotionHealth, rollback bool) error {
	if h == nil || h.Policy.Worker == nil {
		return nil
	}
	id := h.Policy.Worker.PreviousDeploymentID
	var err error
	if !rollback {
		id, err = sqlc.New().PromotionWorkerDeployment(ctx, db, sqlc.PromotionWorkerDeploymentParams{PromotionID: p.ID, AppID: h.Policy.Worker.AppID})
		if err != nil {
			return mapErr(err)
		}
	}
	// Hold the selected runtime stable through a multi-worker publication.
	// Publication callers already hold project and environment intent locks.
	if _, err = sqlc.New().LockPromotionRecoveryWorkerInstances(ctx, db, id); err != nil {
		return mapErr(err)
	}
	ready, err := releaseWorkerReadyDB(ctx, db, p.AccountID, h.Policy.Worker.AppID, id)
	if err != nil {
		return err
	}
	if ready == "" {
		return ErrPromotionWorkerNotReady
	}
	return nil
}

func refreshPromotionWorkerBacklogDB(ctx context.Context, db sqlc.DBTX, h *api.ProjectPromotionHealth) error {
	return visitPromotionWorkers(h, func(one *api.ProjectPromotionHealth) error {
		return refreshPromotionWorkerSingleBacklogDB(ctx, db, one)
	})
}
func refreshPromotionWorkerSingleBacklogDB(ctx context.Context, db sqlc.DBTX, h *api.ProjectPromotionHealth) error {
	if h == nil || h.Policy.Worker == nil {
		return nil
	}
	b, err := sqlc.New().CountProjectWorkerHealthPending(ctx, db, sqlc.CountProjectWorkerHealthPendingParams{AppID: mustPgUUID(h.Policy.Worker.AppID), BindingID: mustPgUUID(h.Policy.Worker.BindingID)})
	if err != nil {
		return err
	}
	h.WorkerHealth.PendingJobs, h.WorkerHealth.OldestPendingAgeMS = b.Pending, b.OldestAgeMs
	return nil
}
