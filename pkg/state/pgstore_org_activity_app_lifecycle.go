package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var _ OrgActivityAppLifecycleMutationStore = (*PgStore)(nil)

// CreateAppIfUnderQuotaWithActivity commits the app and its creation event in
// the same transaction. Quota and slug-conflict behavior matches the ordinary
// create path.
func (s *PgStore) CreateAppIfUnderQuotaWithActivity(ctx context.Context, app App, limits api.Limits, entry OrgActivity) (App, int64, error) {
	tx, err := s.beginTrafficPolicyMutation(ctx, uuidToPgtype(app.AccountID))
	if err != nil {
		return App{}, 0, fmt.Errorf("state: begin app activity create: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	created, err := createAppIfUnderQuotaTx(ctx, tx, app, limits)
	if err != nil {
		return App{}, 0, err
	}
	entry, err = bindOrgActivityToApp(entry, created)
	if err != nil {
		return App{}, 0, err
	}
	outboxID, err := enqueueOrgActivityOutboxTx(ctx, tx, entry)
	if err != nil {
		return App{}, 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return App{}, 0, fmt.Errorf("state: commit app activity create: %w", err)
	}
	return created, outboxID, nil
}

// ScheduleAppDeletionWithActivity atomically schedules the restore-window
// tombstone and records its timeline event. Repeated scheduling remains
// idempotent and does not create duplicate deletion events.
func (s *PgStore) ScheduleAppDeletionWithActivity(ctx context.Context, id string, graceUntil time.Time, entry OrgActivity) (App, int64, error) {
	if graceUntil.IsZero() {
		graceUntil = time.Now().UTC().Add(AppDeleteGraceDuration())
	}
	tx, err := s.beginAppTrafficMutation(ctx, id)
	if err != nil {
		return App{}, 0, fmt.Errorf("state: begin app activity delete: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	var priorStatus string
	if err := tx.QueryRow(ctx, `select status from apps where id = $1 for update`, id).Scan(&priorStatus); err != nil {
		return App{}, 0, mapErr(err)
	}
	var app App
	row := tx.QueryRow(ctx, `
		with suspended_crons as (
			-- Suspend, don't delete: restoring the app inside its grace
			-- window brings its schedules back. The purge removes them.
			update crons set suspended_reason = 'app_deleted' where app_id = $1 returning id
		), cancelled_app_tasks as (
			update app_tasks
			   set status = case when status = 'queued' then 'cancelled' else status end,
			       cancel_requested_at = case
			           when status in ('restoring', 'running') then coalesce(cancel_requested_at, now())
			           else cancel_requested_at
			       end,
			       finished_at = case when status = 'queued' then now() else finished_at end,
			       updated_at = now()
			 where app_id = $1 and status in ('queued', 'restoring', 'running')
			 returning id
		)
		update apps
		   set status = 'deleted',
		       deleted_at = coalesce(deleted_at, now()),
		       delete_grace_until = coalesce(delete_grace_until, $2)
		 where id = $1
		 returning `+appsSelectColumns, id, graceUntil.UTC())
	if err := scanAppInto(&app, row); err != nil {
		return App{}, 0, mapErr(err)
	}
	if err := cancelAppInvocationsTx(ctx, tx, id); err != nil {
		return App{}, 0, err
	}
	var outboxID int64
	if priorStatus != string(AppDeleted) {
		entry, err = bindOrgActivityToApp(entry, app)
		if err != nil {
			return App{}, 0, err
		}
		outboxID, err = enqueueOrgActivityOutboxTx(ctx, tx, entry)
		if err != nil {
			return App{}, 0, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return App{}, 0, fmt.Errorf("state: commit app activity delete: %w", err)
	}
	return app, outboxID, nil
}

// RestoreAppWithActivity commits the grace-window restore and the timeline
// handoff together. A closed or claimed grace window retains RestoreApp's
// existing conflict behavior.
func (s *PgStore) RestoreAppWithActivity(ctx context.Context, id string, limits api.Limits, entry OrgActivity) (App, int64, error) {
	tx, err := s.beginAppTrafficMutation(ctx, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return App{}, 0, ErrConflict
		}
		return App{}, 0, fmt.Errorf("state: begin app activity restore: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	app, err := restoreAppTx(ctx, tx, id, limits)
	if err != nil {
		return App{}, 0, err
	}
	entry, err = bindOrgActivityToApp(entry, app)
	if err != nil {
		return App{}, 0, err
	}
	outboxID, err := enqueueOrgActivityOutboxTx(ctx, tx, entry)
	if err != nil {
		return App{}, 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return App{}, 0, fmt.Errorf("state: commit app activity restore: %w", err)
	}
	return app, outboxID, nil
}
