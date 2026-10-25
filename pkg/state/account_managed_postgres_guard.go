package state

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/jackc/pgx/v5/pgconn"
)

// ErrAccountManagedPostgresDatabases is the account deletion guard: an
// account cannot move to deleted_pending while it owns a live personal
// managed PostgreSQL database (trigger guard_account_managed_postgres_databases).
var ErrAccountManagedPostgresDatabases = errors.New("state: account still owns live managed postgres databases")

const accountManagedPostgresDatabasesConstraint = "account_has_managed_postgres_databases"

// AccountManagedPostgresDatabasesError carries how many live databases block
// the transition. Live is 0 when the guard did not report a count (a schema
// older than migration 20261024145700943).
type AccountManagedPostgresDatabasesError struct{ Live int64 }

func (e *AccountManagedPostgresDatabasesError) Error() string {
	return fmt.Sprintf("state: account still owns %d live managed postgres databases", e.Live)
}
func (e *AccountManagedPostgresDatabasesError) Unwrap() error {
	return ErrAccountManagedPostgresDatabases
}

func accountManagedPostgresDatabasesError(detail string) error {
	live, err := strconv.ParseInt(detail, 10, 64)
	if err != nil || live < 0 {
		live = 0
	}
	return &AccountManagedPostgresDatabasesError{Live: live}
}

// AccountDeletionGuardError maps the account deletion guard triggers to the
// errors the Store returns for them. apid's mail outbox moves accounts to
// deleted_pending in its own transaction, so it maps its errors here to
// match the PgStore path. Any other error is returned unchanged.
func AccountDeletionGuardError(err error) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return err
	}
	switch pgErr.ConstraintName {
	case "account_shared_app_ownership_pending":
		return ErrOrgAppOwnershipPending
	case accountManagedPostgresDatabasesConstraint:
		return accountManagedPostgresDatabasesError(pgErr.Detail)
	}
	return err
}

// AccountDeletionOption changes what an account deletion includes.
type AccountDeletionOption func(*accountDeletion)

type accountDeletion struct{ managedPostgres bool }

// IncludingManagedPostgres makes a move to deleted_pending include the
// account's personal managed PostgreSQL databases (ADR-20261024150500): the
// guard lets the account in, and apid's grace sweep deletes the databases when
// the grace ends. A repeat request never withdraws an inclusion already
// recorded for the pending deletion. MemStore models no databases, so there it
// changes nothing.
func IncludingManagedPostgres(include bool) AccountDeletionOption {
	return func(d *accountDeletion) { d.managedPostgres = d.managedPostgres || include }
}

func accountDeletionOptions(opts []AccountDeletionOption) accountDeletion {
	var d accountDeletion
	for _, opt := range opts {
		opt(&d)
	}
	return d
}

// DeletionIncludesManagedPostgres reports whether opts include the account's
// managed PostgreSQL databases, for writers that move accounts to
// deleted_pending in their own transaction (apid's mail outbox).
func DeletionIncludesManagedPostgres(opts ...AccountDeletionOption) bool {
	return accountDeletionOptions(opts).managedPostgres
}
