package state

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// cronRemovalInlineHistoryMax bounds the run rows a transaction that removes
// crons lets invocations_cron_id_fkey detach inline. Deleting a long-lived
// every-minute cron set cron_id to NULL on tens of thousands of runs, each
// firing the table's row triggers: DeleteCron detaches in committed batches
// for that reason (hunt #9, H9-165), but a deploy's gregale.yaml and a project
// apply removed crons with one DELETE inside their own transaction, which
// held the account's quota lock until the statement timed out, on every
// retry (crons audit 2026-11-05).
const cronRemovalInlineHistoryMax = 1000

// cronRemovalAttempts bounds how often a removal is retried after detaching
// its crons' settled history.
const cronRemovalAttempts = 3

// cronHistoryDetachNeeded reports crons a transaction would remove whose run
// history is too large to detach inline. The caller rolls back, detaches it
// with detachSettledCronHistory and tries again.
type cronHistoryDetachNeeded struct{ cronIDs []string }

func (e *cronHistoryDetachNeeded) Error() string {
	return fmt.Sprintf("state: %d removed crons' run history must be detached first", len(e.cronIDs))
}

// prepareCronRemovalTx readies cronIDs for deletion inside tx. It refuses
// with *cronHistoryDetachNeeded when their runs are too many to detach
// inline, and otherwise cancels their runs that have not started, so a removed
// cron never calls the app again; the cancellation commits or rolls back with
// the removal.
func prepareCronRemovalTx(ctx context.Context, tx pgx.Tx, cronIDs []string) error {
	if len(cronIDs) == 0 {
		return nil
	}
	var heavy bool
	if err := tx.QueryRow(ctx, `
		select count(*) > $2::int from (
		  select 1 from invocations where cron_id = any($1::uuid[]) limit $2::int + 1) r`,
		cronIDs, cronRemovalInlineHistoryMax).Scan(&heavy); err != nil {
		return fmt.Errorf("state: count removed crons' runs: %w", err)
	}
	if heavy {
		return &cronHistoryDetachNeeded{cronIDs: cronIDs}
	}
	rows, err := tx.Query(ctx, `
		update invocations
		   set state = 'cancelled', quota_reserved = false,
		       completed_at = coalesce(completed_at, now())
		 where cron_id = any($1::uuid[]) and source = 'cron' and state = 'pending'
		 returning `+invocationSelectCols, cronIDs)
	if err != nil {
		return fmt.Errorf("state: cancel removed crons' pending runs: %w", err)
	}
	cancelled, err := scanInvocations(rows)
	if err != nil {
		return fmt.Errorf("state: cancel removed crons' pending runs: %w", err)
	}
	for _, inv := range cancelled {
		if err := operationTransitionTx(ctx, tx, inv, false); err != nil {
			return err
		}
	}
	return nil
}

// detachSettledCronHistory detaches the settled runs of cronIDs in committed
// batches, as DeleteCron does, so the removal that asked for it deletes the
// crons quickly. Runs not yet settled stay attached: the removal cancels the
// pending ones itself. A removal that then fails leaves the surviving crons'
// settled runs out of their history; it never loses or reroutes a run.
func (s *PgStore) detachSettledCronHistory(ctx context.Context, cronIDs []string) error {
	for _, id := range cronIDs {
		for {
			tag, err := s.pool.Exec(ctx, `
				update invocations set cron_id = null
				 where id in (select id from invocations
				               where cron_id = $1::uuid
				                 and state in ('completed','failed','cancelled','superseded','expired')
				               limit $2)`, id, cronDeleteBatch)
			if err != nil {
				return fmt.Errorf("state: detach removed cron %s history: %w", id, err)
			}
			if tag.RowsAffected() < cronDeleteBatch {
				break
			}
		}
	}
	return nil
}

// retryAfterDetachingCronHistory runs a removal, and when it asks for its
// crons' history to be detached first, detaches it and runs it again.
func (s *PgStore) retryAfterDetachingCronHistory(ctx context.Context, run func() error) error {
	for attempt := 1; ; attempt++ {
		err := run()
		var need *cronHistoryDetachNeeded
		if !errors.As(err, &need) || attempt >= cronRemovalAttempts {
			return err
		}
		if err := s.detachSettledCronHistory(ctx, need.cronIDs); err != nil {
			return err
		}
	}
}

// removedHTTPCronIDsTx lists appID's HTTP crons that target (a JSON array of
// objects with an "id") does not keep.
// removedHTTPCronIDsTx lists the HTTP crons of one environment of appID
// (scope ” is production) that target no longer holds.
func removedHTTPCronIDsTx(ctx context.Context, tx pgx.Tx, appID, scope string, target []byte) ([]string, error) {
	return cronIDsTx(ctx, tx, `
		select id::text from crons
		 where app_id = $1::text::uuid and cardinality(command) = 0 and deployment_scope = $3
		   and id not in (select (value->>'id')::uuid from jsonb_array_elements($2::jsonb))`, appID, target, scope)
}

// appCronIDsTx lists every cron of appID.
func appCronIDsTx(ctx context.Context, tx pgx.Tx, appID string) ([]string, error) {
	return cronIDsTx(ctx, tx, `select id::text from crons where app_id = $1`, appID)
}

func cronIDsTx(ctx context.Context, tx pgx.Tx, query string, args ...any) ([]string, error) {
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("state: list crons to remove: %w", err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("state: list crons to remove: %w", err)
	}
	return ids, nil
}
