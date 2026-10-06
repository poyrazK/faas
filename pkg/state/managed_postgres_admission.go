package state

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var ErrManagedPostgresAdmissionFenced = errors.New("state: managed postgres cutover admission fenced")

// ManagedPostgresAdmissionReader is the scheduler's durable admission check.
// The PostgreSQL trigger is the authoritative guard against concurrent changes.
type ManagedPostgresAdmissionReader interface {
	ManagedPostgresAdmissionFenced(context.Context, string) (bool, error)
}

var (
	_ ManagedPostgresAdmissionReader = (*PgStore)(nil)
	_ ManagedPostgresAdmissionReader = (*MemStore)(nil)
)

func (s *PgStore) ManagedPostgresAdmissionFenced(ctx context.Context, appID string) (bool, error) {
	fenced, err := sqlc.New().ManagedPostgresAdmissionFenced(ctx, s.pool, appID)
	return fenced, mapErr(err)
}

func (m *MemStore) ManagedPostgresAdmissionFenced(ctx context.Context, appID string) (bool, error) {
	_, err := m.AppByID(ctx, appID)
	return false, err
}
