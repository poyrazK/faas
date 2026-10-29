package managedpostgres

import (
	"context"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// AccountErasure is what deleting an account removes from managed
// PostgreSQL (ADR-20261024150500): its live personal databases, the ones the
// account deletion guard counts, and whether its pending deletion includes
// them.
type AccountErasure struct {
	Included  bool
	Databases []Database
}

// AccountErasureStore is implemented by stores that can read an account's
// deletion state. The in-memory store cannot, so it reports nothing to erase.
type AccountErasureStore interface {
	AccountErasure(context.Context, string) (AccountErasure, error)
}

// AccountErasure lists the account's live personal databases, newest first so
// restore descendants come before their sources.
func (s *PostgresStore) AccountErasure(ctx context.Context, accountID string) (AccountErasure, error) {
	account, err := postgresUUID(accountID)
	if err != nil {
		return AccountErasure{}, err
	}
	q := new(sqlc.Queries)
	rows, err := q.ListManagedPostgresAccountErasureDatabases(ctx, s.catalog, account)
	if err != nil {
		return AccountErasure{}, mapPostgresError(err)
	}
	if len(rows) == 0 {
		return AccountErasure{}, nil
	}
	included, err := q.ManagedPostgresAccountDeletionIncluded(ctx, s.catalog, account)
	if err != nil {
		return AccountErasure{}, mapPostgresError(err)
	}
	return AccountErasure{Included: included, Databases: databasesFromSQL(rows)}, nil
}

// AccountErasure reports what deleting the account removes. A store that
// cannot read account state reports nothing.
func (s *Service) AccountErasure(ctx context.Context, accountID string) (AccountErasure, error) {
	store, ok := s.store.(AccountErasureStore)
	if !ok {
		return AccountErasure{}, nil
	}
	return store.AccountErasure(ctx, accountID)
}
