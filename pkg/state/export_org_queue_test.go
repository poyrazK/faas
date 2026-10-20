package state

import (
	"context"
	"fmt"
)

// PoolOrgAsyncReservations moves one organization's async reservations to its
// own counter, as the release after ADR-202610171908's capture release does
// (adopt_org_async_reservations on Postgres).
func PoolOrgAsyncReservations(ctx context.Context, store Store, orgID string) error {
	switch s := store.(type) {
	case *MemStore:
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.poolOrgAsyncReservationsLocked(orgID)
	case *PgStore:
		_, err := s.pool.Exec(ctx, `SELECT adopt_org_async_reservations($1::uuid)`, orgID)
		return err
	default:
		return fmt.Errorf("pool org async reservations: unsupported store %T", store)
	}
}
