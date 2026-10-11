package state

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

var _ OrgActivityAppConfigMutationStore = (*PgStore)(nil)

// UpdateAppWithActivity locks and reads the current app, applies its partial
// update, and inserts the activity handoff in the same transaction. The
// builder sees the exact before/after rows and must not perform I/O while the
// transaction is open.
func (s *PgStore) UpdateAppWithActivity(ctx context.Context, id string, p UpdateAppParams, entry OrgActivity, build OrgActivityAppConfigBuilder) (App, int64, error) {
	if build == nil {
		return App{}, 0, ErrInvalidArgument
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return App{}, 0, fmt.Errorf("state: begin app config activity update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var before App
	if err := scanAppInto(&before, tx.QueryRow(ctx, `select `+appsSelectColumns+` from apps where id = $1 for update`, id)); err != nil {
		return App{}, 0, mapErr(err)
	}
	after, err := updateApp(ctx, tx, id, p)
	if err != nil {
		return App{}, 0, err
	}
	after, err = syncProductionWorkloadSpecTx(ctx, tx, after, p)
	if err != nil {
		return App{}, 0, err
	}
	data, record, err := build(before, after)
	if err != nil {
		return App{}, 0, err
	}
	var outboxID int64
	if record {
		entry.Data = data
		entry, err = bindOrgActivityToApp(entry, after)
		if err != nil {
			return App{}, 0, err
		}
		outboxID, err = enqueueOrgActivityOutboxTx(ctx, tx, entry)
		if err != nil {
			return App{}, 0, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return App{}, 0, fmt.Errorf("state: commit app config activity update: %w", err)
	}
	return after, outboxID, nil
}
