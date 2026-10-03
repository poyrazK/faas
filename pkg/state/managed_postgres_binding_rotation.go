package state

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// ManagedPostgresBindingRotationStore marks a staged credential's previous
// provider identity safe to retire after the scheduler completes a rolling
// runtime refresh for the rotation wake ID.
type ManagedPostgresBindingRotationStore interface {
	FinalizeManagedPostgresBindingRotationsForApp(context.Context, string, string) error
}

func (s *PgStore) FinalizeManagedPostgresBindingRotationsForApp(ctx context.Context, appID, wakeID string) error {
	if _, err := uuid.Parse(appID); err != nil {
		return ErrInvalidArgument
	}
	if _, err := uuid.Parse(wakeID); err != nil {
		return ErrInvalidArgument
	}
	_, err := s.pool.Exec(ctx,
		`UPDATE managed_postgres_bindings
		 SET rotation_cleanup_ready = true, retry_at = now(), updated_at = now()
		 WHERE app_id = $1 AND rotation_wake_id = $2
		   AND rotation_previous_generation IS NOT NULL AND state <> 'deleted'`,
		mustPgUUID(appID), mustPgUUID(wakeID),
	)
	if err != nil {
		return fmt.Errorf("finalize managed postgres binding rotation: %w", mapErr(err))
	}
	return nil
}

// MemStore does not own the managed PostgreSQL binding catalog. Keeping the
// scheduler capability here makes local and scheduler tests use the same
// interface; the package that owns bindings supplies stateful rotation tests.
func (*MemStore) FinalizeManagedPostgresBindingRotationsForApp(context.Context, string, string) error {
	return nil
}
