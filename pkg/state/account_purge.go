package state

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Reasons DeleteAccount keeps an account past its grace. Each names rows a
// workflow of their own must retire first: deleting them with the account
// would orphan what they hold outside the database.
const (
	// AccountPurgeBlockedHostingSite: a hosting site that is not purged. The
	// grace sweep starts its deletion; the hosting reconciler retires its
	// routes and releases and purges it.
	AccountPurgeBlockedHostingSite = "hosting_site"
	// AccountPurgeBlockedEnvironmentClone: environment clone state of the
	// account, or a clone fencing or copying one of its buckets, databases or
	// bindings. The clone workflow releases those rows and their provider
	// copies.
	AccountPurgeBlockedEnvironmentClone = "environment_clone"
	// AccountPurgeBlockedManagedPostgresFence: a checkpoint or cutover fence
	// on one of the account's databases, owned by the managed PostgreSQL
	// service.
	AccountPurgeBlockedManagedPostgresFence = "managed_postgres_fence"
)

// AccountPurgeBlockedError says why DeleteAccount kept an account. It matches
// ErrConflict, like the active-bucket refusal before it.
type AccountPurgeBlockedError struct {
	Reason string
	Count  int64
}

func (e *AccountPurgeBlockedError) Error() string {
	return fmt.Sprintf("state: account purge waits for %d %s row(s)", e.Count, e.Reason)
}

func (e *AccountPurgeBlockedError) Is(target error) bool { return target == ErrConflict }

// accountPurgeWaitsQuery counts the rows DeleteAccount must not remove
// itself: hosting sites, environment clone state, and managed PostgreSQL
// fences. TestAccountPurgeClearsEveryRestrictingKey (pkg/state/purgeguard)
// exempts exactly these keys. $1 is the account.
const accountPurgeWaitsQuery = `select (select count(*) from hosting_sites where account_id = $1),
       (select count(*) from project_environment_clone_checkpoint_postgres_credentials where account_id = $1)
     + (select count(*) from project_environment_clone_postgres_archives where account_id = $1)
     + (select count(*) from project_environment_clone_postgres_contents where account_id = $1)
     + (select count(*) from project_environment_clone_postgres_copy_readers where account_id = $1)
     + (select count(*) from project_environment_clone_postgres_copy_targets where account_id = $1)
     + (select count(*) from project_environment_clone_postgres_database_plans where account_id = $1)
     + (select count(*) from project_environment_clone_postgres_database_sql_pins where account_id = $1)
     + (select count(*) from project_environment_clone_postgres_imports where account_id = $1)
     + (select count(*) from project_environment_clone_postgres_inventories where account_id = $1)
     + (select count(*) from project_environment_clone_postgres_membership_plans where account_id = $1)
     + (select count(*) from project_environment_clone_postgres_role_plans where account_id = $1)
     + (select count(*) from project_environment_clone_postgres_snapshot_restores where account_id = $1)
     + (select count(*) from project_environment_clone_postgres_target_sql_pins where account_id = $1)
     + (select count(*) from project_environment_clone_postgres_verification_read_budgets where account_id = $1)
     + (select count(*) from project_environment_clone_postgres_verifications where account_id = $1)
     + (select count(*) from project_environment_clone_checkpoint_postgres_isolation where adopted_database_id in (select id from managed_postgres_databases where account_id = $1))
     + (select count(*) from project_environment_clone_postgres_snapshots where source_database_id in (select id from managed_postgres_databases where account_id = $1))
     + (select count(*) from project_environment_clone_postgres_write_fences where source_database_id in (select id from managed_postgres_databases where account_id = $1))
     + (select count(*) from project_environment_clone_postgres_bindings where target_binding_id in (select id from managed_postgres_bindings where account_id = $1))
     + (select count(*) from object_bucket_write_fences where bucket_id in (select id from object_buckets where account_id = $1))
     + (select count(*) from project_environment_clone_checkpoint_object_targets where source_bucket_id in (select id from object_buckets where account_id = $1) or target_bucket_id in (select id from object_buckets where account_id = $1))
     + (select count(*) from project_environment_clone_checkpoint_unused_object_reservations where target_bucket_id in (select id from object_buckets where account_id = $1)),
       (select count(*) from managed_postgres_checkpoint_maintenance where source_database_id in (select id from managed_postgres_databases where account_id = $1))
     + (select count(*) from managed_postgres_cutover_source_fences where source_database_id in (select id from managed_postgres_databases where account_id = $1))`

// accountPurgeWaitsTx refuses DeleteAccount, with a typed reason, while the
// account still has rows another workflow must retire. Without it the purge
// failed on their keys at the final delete, every tick, with nothing but a
// warning in the log (2026-11-03).
func accountPurgeWaitsTx(ctx context.Context, tx pgx.Tx, accountID string) error {
	var sites, clones, fences int64
	if err := tx.QueryRow(ctx, accountPurgeWaitsQuery, accountID).Scan(&sites, &clones, &fences); err != nil {
		return fmt.Errorf("state: count account purge waits: %w", err)
	}
	switch {
	case sites != 0:
		return &AccountPurgeBlockedError{Reason: AccountPurgeBlockedHostingSite, Count: sites}
	case clones != 0:
		return &AccountPurgeBlockedError{Reason: AccountPurgeBlockedEnvironmentClone, Count: clones}
	case fences != 0:
		return &AccountPurgeBlockedError{Reason: AccountPurgeBlockedManagedPostgresFence, Count: fences}
	}
	return nil
}
