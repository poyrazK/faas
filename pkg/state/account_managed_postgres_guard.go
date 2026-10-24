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
