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

func (s *PgStore) StartAutomationLiveSuite(ctx context.Context, appID, name, id string, request api.StartAutomationLiveSuiteRequest, runtimeEnabled bool) (api.AutomationLiveSuite, error) {
	id, plan, err := managedSuitePlan(id, request)
	if err != nil {
		return api.AutomationLiveSuite{}, err
	}
	return s.mutateManagedSuite(ctx, appID, name, id, &plan, runtimeEnabled)
}

func (s *PgStore) AdvanceAutomationLiveSuite(ctx context.Context, appID, name, id string, request api.AdvanceAutomationLiveSuiteRequest, runtimeEnabled bool) (api.AutomationLiveSuite, error) {
	if !request.ConfirmRealActions {
		return api.AutomationLiveSuite{}, api.ErrValidation("Confirm real actions before continuing this suite.")
	}
	parsed, err := uuid.Parse(id)
	if err != nil {
		return api.AutomationLiveSuite{}, ErrNotFound
	}
	return s.mutateManagedSuite(ctx, appID, name, parsed.String(), nil, runtimeEnabled)
}

func (s *PgStore) mutateManagedSuite(ctx context.Context, appID, name, id string, plan *api.SaveAutomationLiveSuiteRequest, runtimeEnabled bool) (api.AutomationLiveSuite, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.AutomationLiveSuite{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if err := q.LockWorkflowRunAdmission(ctx, tx, appID); err != nil {
		return api.AutomationLiveSuite{}, err
	}
	prior, err := getLiveSuiteSQL(ctx, tx, appID, name, id)
	exists := err == nil
	if err != nil && !errors.Is(err, ErrNotFound) {
		return api.AutomationLiveSuite{}, err
	}
	if exists && plan != nil {
		if err := managedSuiteReplay(prior, *plan); err != nil {
			return api.AutomationLiveSuite{}, err
		}
		return liveSuiteViewSQL(ctx, tx, prior) // Replaying start never advances a later case.
	}
	if !exists && plan == nil {
		return api.AutomationLiveSuite{}, ErrNotFound
	}
	if exists && !prior.Managed {
		return api.AutomationLiveSuite{}, ErrWorkflowRunIdempotencyConflict
	}
	if !exists {
		if err := validateManagedSuiteCreation(id); err != nil {
			return api.AutomationLiveSuite{}, err
		}
		if err := liveSuiteRoomSQL(ctx, tx, appID, name); err != nil {
			return api.AutomationLiveSuite{}, err
		}
	}
	if err := lockManagedSuiteTarget(ctx, tx, appID, name); err != nil {
		return api.AutomationLiveSuite{}, err
	}
	current, deployment, err := liveCurrentSQL(ctx, tx, appID, name)
	if err != nil {
		return api.AutomationLiveSuite{}, err
	}
	record := prior
	if !exists {
		if err := validateLiveSuiteTarget(*plan, current, deployment); err != nil {
			return api.AutomationLiveSuite{}, err
		}
		record = automationLiveSuiteRecord{ID: id, AppID: appID, Name: name, Plan: *plan, Managed: true}
	}
	record, err = advanceManagedSuite(record, current, deployment,
		func(id string) (api.AutomationLiveVerification, error) {
			return checkLiveVerificationTx(ctx, tx, appID, name, id)
		},
		func(key string, request api.StartAutomationLiveVerificationRequest) (api.AutomationLiveVerification, error) {
			fingerprint, err := validateLiveStart(&request, key)
			if err != nil {
				return api.AutomationLiveVerification{}, err
			}
			row, err := startLiveVerificationTx(ctx, tx, appID, name, key, request, fingerprint, runtimeEnabled)
			if err != nil {
				return api.AutomationLiveVerification{}, err
			}
			return liveViewSQL(ctx, tx, row, current, deployment)
		})
	if err != nil {
		return api.AutomationLiveSuite{}, err
	}
	record, err = persistLiveSuiteSQL(ctx, tx, appID, name, id, record.Plan, prior, exists, true)
	if err != nil {
		return api.AutomationLiveSuite{}, err
	}
	view, err := liveSuiteViewSQL(ctx, tx, record)
	if err != nil {
		return api.AutomationLiveSuite{}, err
	}
	if err := q.PruneAutomationLiveSuites(ctx, tx, sqlc.PruneAutomationLiveSuitesParams{AppID: mustPgUUID(appID), Name: name, Retained: api.AutomationLiveSuiteRetainedLimit, ProtectedSince: pgtypeTimestamptzFromTime(time.Now().Add(-api.AutomationLiveSuiteStartWindow))}); err != nil {
		return api.AutomationLiveSuite{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return api.AutomationLiveSuite{}, err
	}
	return view, nil
}

func liveSuiteRoomSQL(ctx context.Context, db sqlc.DBTX, appID, name string) error {
	count, err := sqlc.New().CountProtectedAutomationLiveSuites(ctx, db, sqlc.CountProtectedAutomationLiveSuitesParams{AppID: mustPgUUID(appID), Name: name, ProtectedSince: pgtypeTimestamptzFromTime(time.Now().Add(-api.AutomationLiveSuiteStartWindow))})
	if err != nil {
		return err
	}
	if count >= int64(api.AutomationLiveSuiteRetainedLimit) {
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
	if _, err := q.LockAutomationForLiveSuite(ctx, tx, sqlc.LockAutomationForLiveSuiteParams{AppID: mustPgUUID(appID), Name: name}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	return nil
}
