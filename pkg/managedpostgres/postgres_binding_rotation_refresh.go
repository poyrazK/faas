package managedpostgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ BindingRotationRefreshStore = (*PostgresStore)(nil)

// EnsureBindingRotationRefresh repairs ready rotations left by older workers
// or retries terminal scheduler deliveries. The app lock precedes the binding
// lock, as it does when publication commits the original refresh intent.
func (s *PostgresStore) EnsureBindingRotationRefresh(ctx context.Context, accountID, bindingID string, now time.Time) (Binding, error) {
	id, err := postgresUUID(bindingID)
	if err != nil || now.IsZero() {
		return Binding{}, ErrInvalid
	}
	tx, err := s.catalog.Begin(ctx)
	if err != nil {
		return Binding{}, mapPostgresError(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	q := sqlc.New()
	if err := q.LockManagedPostgresRotationApp(ctx, tx, id); err != nil {
		return Binding{}, mapPostgresError(err)
	}
	row, err := q.LockManagedPostgresRotationBinding(ctx, tx, id)
	if err != nil {
		return Binding{}, mapPostgresError(err)
	}
	binding := bindingFromDeliveryRow(sqlc.FinishManagedPostgresBindingProvisionRow(row))
	if binding.AccountID != accountID {
		return Binding{}, ErrNotFound
	}
	binding, err = ensurePostgresRotationRefresh(ctx, tx, binding)
	if err != nil {
		return Binding{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Binding{}, mapPostgresError(err)
	}
	return binding, nil
}

func ensurePostgresRotationRefresh(ctx context.Context, tx pgx.Tx, binding Binding) (Binding, error) {
	if !bindingRotationNeedsRefresh(binding) {
		return binding, nil
	}
	q := sqlc.New()
	appID, err := postgresUUID(binding.AppID)
	if err != nil {
		return Binding{}, err
	}
	id, err := postgresUUID(binding.ID)
	if err != nil {
		return Binding{}, err
	}
	runtime, err := q.ManagedPostgresRotationRuntime(ctx, tx, sqlc.ManagedPostgresRotationRuntimeParams{
		AppID: appID, ResidentStates: state.LiveStates(),
	})
	if err != nil {
		return Binding{}, mapPostgresError(err)
	}
	if !runtime.Live && !runtime.Resident {
		if err := q.CompleteIdleManagedPostgresRotationRefresh(ctx, tx, id); err != nil {
			return Binding{}, mapPostgresError(err)
		}
		binding.RotationCleanupReady = true
		return binding, nil
	}
	if err := q.QueueManagedPostgresRotationRefresh(ctx, tx, sqlc.QueueManagedPostgresRotationRefreshParams{AppID: binding.AppID, WakeID: binding.RotationWakeID}); err != nil {
		return Binding{}, mapPostgresError(err)
	}
	return binding, nil
}
