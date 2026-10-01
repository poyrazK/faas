package main

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type applicationStandardWorkerStore interface {
	state.ApplicationStandardMaterializationStore
	state.ApplicationStandardAutomaticMaterializationStore
}

// Intent projection belongs to apid. This loop only writes durable settings;
// schedd, imaged and gateways retain their runtime/verification ownership.
func (s *server) runApplicationStandardWorker(ctx context.Context) {
	worker, ok := s.store.(applicationStandardWorkerStore)
	if !ok {
		return
	}
	owner := "application-standard-" + uuid.NewString()
	run := func() { s.runApplicationStandardPass(ctx, worker, owner) }
	run()
	ticker := time.NewTicker(api.ApplicationStandardWorkerInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}

func (s *server) runApplicationStandardPass(parent context.Context, worker applicationStandardWorkerStore, owner string) {
	ctx, cancel := context.WithTimeout(parent, api.ApplicationStandardWorkerPassTimeout)
	defer cancel()
	// A reviewed target takes precedence over automatic repair of that same
	// app. Durable enrollment claims enforce this even across daemon replicas.
	claim, err := worker.ClaimApplicationStandardOperation(ctx, owner)
	if err == nil {
		for range api.ApplicationStandardWorkerPassLimit {
			operation, applyErr := worker.MaterializeNextApplicationStandardTarget(ctx, claim)
			if applyErr != nil {
				s.applicationStandardWorkerError(ctx, "operation", claim.OperationID, applyErr)
				break
			}
			if operation.State != "running" {
				break
			}
		}
		// If the bounded pass stopped mid-wave, release only this generation so
		// another pass can continue immediately instead of waiting for its TTL.
		release, cancelRelease := context.WithTimeout(context.WithoutCancel(parent), api.ApplicationStandardWorkerReleaseTimeout)
		_ = worker.ReleaseApplicationStandardOperationWorker(release, claim)
		cancelRelease()
	} else {
		s.applicationStandardWorkerError(ctx, "operation_claim", "", err)
	}
	for range api.ApplicationStandardWorkerPassLimit {
		if ctx.Err() != nil {
			return
		}
		enrollment, err := worker.ClaimApplicationStandardEnrollment(ctx, owner)
		if errors.Is(err, state.ErrNotFound) {
			return
		}
		if err != nil {
			s.applicationStandardWorkerError(ctx, "enrollment_claim", "", err)
			return
		}
		_, err = worker.MaterializeApplicationStandardEnrollment(ctx, enrollment)
		if err != nil {
			s.applicationStandardWorkerError(ctx, "enrollment", enrollment.AppID, err)
		}
	}
}

func (s *server) applicationStandardWorkerError(ctx context.Context, stage, id string, err error) {
	if err == nil || errors.Is(err, state.ErrNotFound) || errors.Is(err, state.ErrApplicationStandardLeaseLost) || errors.Is(err, state.ErrApplicationStandardOperationInProgress) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return
	}
	// Driver errors can include private restoration rows. Log only a stable
	// class and opaque identity; customer-visible blockers live in durable state.
	code := "materialization_failed"
	if errors.Is(err, state.ErrApplicationStandardReviewBusy) {
		code = "inputs_busy"
	}
	s.log.WarnContext(ctx, "application standard repair failed", "stage", stage, "id", id, "error_code", code)
}
