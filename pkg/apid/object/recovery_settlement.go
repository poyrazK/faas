package object

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) objectNodeSettlementEnrollment(b state.ObjectBucket) (bool, error) {
	if s.Host.ObjectStorage() == nil {
		return false, objectstorage.ErrConfiguration
	}
	backend, err := s.Host.ObjectStorage().Resolve(b.BackendID, b.BackendFingerprint)
	if err != nil {
		return false, objectstorage.ErrConfiguration
	}
	return objectstorage.QueueOwnedNodeSettlement(backend.Provider), nil
}

// ReconcileObjectNodeSettlements delivers terminal evidence only. The saved
// target survives deletion and configuration changes never retarget a job.
func (s *Service) ReconcileObjectNodeSettlements(parent context.Context, observe func(string, string)) error {
	st, ok := s.Host.Store().(state.ObjectNodeSettlementStore)
	if !ok {
		return nil
	}
	ctx, cancel := context.WithTimeout(parent, api.ObjectNodeSettlementSweepTimeout)
	defer cancel()
	jobs, err := st.ClaimObjectNodeSettlements(ctx, uuid.NewString(), api.ObjectNodeSettlementBatch)
	if err != nil {
		return err
	}
	for _, job := range jobs {
		if err := ctx.Err(); err != nil {
			return err // Unstarted jobs become claimable when their lease expires.
		}
		outcome, err := s.deliverObjectNodeSettlement(ctx, st, job)
		if observe != nil {
			observe("settlement", outcome)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func nodeSettlementRetry(cause error, attempt int32) (string, time.Duration) {
	if errors.Is(cause, objectstorage.ErrConfiguration) {
		return "configuration", api.ObjectNodeSettlementRetryMax
	}
	delay := api.ObjectNodeSettlementRetryMin
	for i := int32(1); i < attempt && delay < api.ObjectNodeSettlementRetryMax; i++ {
		delay *= 2
	}
	return "unavailable", min(delay, api.ObjectNodeSettlementRetryMax)
}

func (s *Service) deliverObjectNodeSettlement(ctx context.Context, st state.ObjectNodeSettlementStore, job state.ObjectNodeSettlement) (string, error) {
	cause := objectstorage.ErrConfiguration
	if registry := s.Host.ObjectStorage(); registry != nil {
		backend, err := registry.Resolve(job.BackendID, job.BackendFingerprint)
		if err == nil {
			if provider, owned := backend.Provider.(*objectstorage.S3); owned {
				cause = provider.AcknowledgeOwnedSettlement(ctx, job.Settlement)
			}
		}
	}
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.ObjectUploadSettlementTimeout)
	defer cancel()
	if cause == nil {
		err := st.FinishObjectNodeSettlement(finishCtx, job.ID, job.LeaseToken)
		if errors.Is(err, state.ErrConflict) {
			return "stale", nil
		}
		return "delivered", err
	}
	code, delay := nodeSettlementRetry(cause, job.Attempts)
	err := st.RetryObjectNodeSettlement(finishCtx, job.ID, job.LeaseToken, code, delay)
	if errors.Is(err, state.ErrConflict) {
		return "stale", nil
	}
	if err == nil {
		s.Host.Logger().Warn("owned object settlement acknowledgement deferred", "backend_id", job.BackendID, "error_code", code, "attempt", job.Attempts, "retry_in", delay)
	}
	return code, err
}
