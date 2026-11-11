package state

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) StartStoredWorkflowLiveSuite(ctx context.Context, appID, name, id string, request api.StartStoredWorkflowLiveSuiteRequest, runtimeEnabled bool) (api.StoredWorkflowLiveSuite, error) {
	id, plan, err := managedSuitePlan(id, request)
	if err != nil {
		return api.StoredWorkflowLiveSuite{}, err
	}
	return s.mutateManagedSuite(ctx, appID, name, id, &plan, runtimeEnabled)
}

func (s *PgStore) AdvanceStoredWorkflowLiveSuite(ctx context.Context, appID, name, id string, request api.AdvanceStoredWorkflowLiveSuiteRequest, runtimeEnabled bool) (api.StoredWorkflowLiveSuite, error) {
	if !request.ConfirmRealActions {
		return api.StoredWorkflowLiveSuite{}, api.ErrValidation("Confirm real actions before continuing this suite.")
	}
	parsed, err := uuid.Parse(id)
	if err != nil {
		return api.StoredWorkflowLiveSuite{}, ErrNotFound
	}
	return s.mutateManagedSuite(ctx, appID, name, parsed.String(), nil, runtimeEnabled)
}

func (s *PgStore) mutateManagedSuite(ctx context.Context, appID, name, id string, plan *api.SaveStoredWorkflowLiveSuiteRequest, runtimeEnabled bool) (api.StoredWorkflowLiveSuite, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.StoredWorkflowLiveSuite{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if err := q.LockWorkflowRunAdmission(ctx, tx, appID); err != nil {
		return api.StoredWorkflowLiveSuite{}, err
	}
	prior, err := getLiveSuiteSQL(ctx, tx, appID, name, id)
	exists := err == nil
	if err != nil && !errors.Is(err, ErrNotFound) {
		return api.StoredWorkflowLiveSuite{}, err
	}
	if exists && plan != nil {
		if err := managedSuiteReplay(prior, *plan); err != nil {
			return api.StoredWorkflowLiveSuite{}, err
		}
		return liveSuiteViewSQL(ctx, tx, prior) // Replaying start never advances a later case.
	}
	if !exists && plan == nil {
		return api.StoredWorkflowLiveSuite{}, ErrNotFound
	}
	if exists && !prior.Managed {
		return api.StoredWorkflowLiveSuite{}, ErrWorkflowRunIdempotencyConflict
	}
	if !exists {
		if err := validateManagedSuiteCreation(id); err != nil {
			return api.StoredWorkflowLiveSuite{}, err
		}
		if err := liveSuiteRoomSQL(ctx, tx, appID, name); err != nil {
			return api.StoredWorkflowLiveSuite{}, err
		}
	}
	if err := lockManagedSuiteTarget(ctx, tx, appID, name); err != nil {
		return api.StoredWorkflowLiveSuite{}, err
	}
	current, deployment, err := liveCurrentSQL(ctx, tx, appID, name)
	if err != nil {
		return api.StoredWorkflowLiveSuite{}, err
	}
	record := prior
	if !exists {
		if err := validateLiveSuiteTarget(*plan, current, deployment); err != nil {
			return api.StoredWorkflowLiveSuite{}, err
		}
		record = storedWorkflowLiveSuiteRecord{ID: id, AppID: appID, Name: name, Plan: *plan, Managed: true}
	}
	record, err = advanceManagedSuite(record, current, deployment,
		func(id string) (api.StoredWorkflowLiveVerification, error) {
			return checkLiveVerificationTx(ctx, tx, appID, name, id)
		},
		func(key string, request api.StartStoredWorkflowLiveVerificationRequest) (api.StoredWorkflowLiveVerification, error) {
			fingerprint, err := validateLiveStart(&request, key)
			if err != nil {
				return api.StoredWorkflowLiveVerification{}, err
			}
			row, err := startLiveVerificationTx(ctx, tx, appID, name, key, request, fingerprint, runtimeEnabled)
			if err != nil {
				return api.StoredWorkflowLiveVerification{}, err
			}
			return liveViewSQL(ctx, tx, row, current, deployment)
		})
	if err != nil {
		return api.StoredWorkflowLiveSuite{}, err
	}
	record, err = persistLiveSuiteSQL(ctx, tx, appID, name, id, record.Plan, prior, exists, true)
	if err != nil {
		return api.StoredWorkflowLiveSuite{}, err
	}
	view, err := liveSuiteViewSQL(ctx, tx, record)
	if err != nil {
		return api.StoredWorkflowLiveSuite{}, err
	}
	if err := q.PruneStoredWorkflowLiveSuites(ctx, tx, sqlc.PruneStoredWorkflowLiveSuitesParams{AppID: mustPgUUID(appID), Name: name, Retained: api.StoredWorkflowLiveSuiteRetainedLimit, ProtectedSince: pgtypeTimestamptzFromTime(time.Now().Add(-api.StoredWorkflowLiveSuiteProtectedWindow))}); err != nil {
		return api.StoredWorkflowLiveSuite{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return api.StoredWorkflowLiveSuite{}, err
	}
	return view, nil
}

func liveSuiteRoomSQL(ctx context.Context, db sqlc.DBTX, appID, name string) error {
	count, err := sqlc.New().CountProtectedStoredWorkflowLiveSuites(ctx, db, sqlc.CountProtectedStoredWorkflowLiveSuitesParams{AppID: mustPgUUID(appID), Name: name, ProtectedSince: pgtypeTimestamptzFromTime(time.Now().Add(-api.StoredWorkflowLiveSuiteProtectedWindow))})
	if err != nil {
		return err
	}
	if count >= int64(api.StoredWorkflowLiveSuiteRetainedLimit) {
		return api.ErrCapacity("Recent suites still own their recovery records. Retry after the suite creation window expires.")
	}
	return nil
}

func lockManagedSuiteTarget(ctx context.Context, tx pgx.Tx, appID, name string) error {
	q := sqlc.New()
	if _, err := q.LockEventWorkflowTarget(ctx, tx, mustPgUUID(appID)); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if _, err := q.LockWorkflowScheduleTarget(ctx, tx, mustPgUUID(appID)); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrWorkflowEventTargetUnavailable
		}
		return err
	}
	if _, err := q.LockStoredWorkflowForLiveSuite(ctx, tx, sqlc.LockStoredWorkflowForLiveSuiteParams{AppID: mustPgUUID(appID), Name: name}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	return nil
}
