package state

import (
	"errors"
	"fmt"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"
)

// Reasons DeleteAppPermanently leaves an expired, claimed app in place. They
// are a closed set because the grace sweep exports them as a metric label.
const (
	// AppPurgeBlockedObjectBucket: a bucket that is not deleted.
	AppPurgeBlockedObjectBucket = "object_bucket"
	// AppPurgeBlockedManagedPostgresBinding: a binding that is not a deleted
	// tombstone. Only the binding service may release it, because deletion
	// revokes the provider credential before it tombstones the row (ADR-155);
	// deleting or cascading the row would leave that credential live. Deleting
	// an app is refused while it has one, so this is an anomaly for an
	// operator, not a state the purge resolves.
	AppPurgeBlockedManagedPostgresBinding = "managed_postgres_binding"
	// AppPurgeBlockedMultipartWrite: a dispatched, unsettled part write,
	// which ADR-590 retains until it settles.
	AppPurgeBlockedMultipartWrite = "multipart_write"
	// AppPurgeBlockedForeignKey: a row the purge does not remove still
	// references the app (or a row the purge deletes), so a DELETE failed.
	AppPurgeBlockedForeignKey = "foreign_key"
)

// AppPurgeBlockedError says why DeleteAppPermanently refused to remove an
// app. A retained resource is a wait and matches ErrConflict. A foreign key
// is a purge defect and matches no sentinel; Constraint and Table name the
// referencing rows so an operator can add the missing purge step.
type AppPurgeBlockedError struct {
	Reason     string // one of the AppPurgeBlocked* constants
	Count      int64  // retained rows, for the retained-resource reasons
	Constraint string // violated foreign key, for AppPurgeBlockedForeignKey
	Table      string // referencing table, for AppPurgeBlockedForeignKey
}

func (e *AppPurgeBlockedError) Error() string {
	if e.Reason == AppPurgeBlockedForeignKey {
		return fmt.Sprintf("state: app purge blocked by foreign key %s on %s", e.Constraint, e.Table)
	}
	return fmt.Sprintf("state: app purge waits for %d %s row(s)", e.Count, e.Reason)
}

// Is makes a retained-resource wait match ErrConflict, as it did before the
// reason was recorded.
func (e *AppPurgeBlockedError) Is(target error) bool {
	return target == ErrConflict && e.Reason != AppPurgeBlockedForeignKey
}

// appPurgeForeignKeyError turns a foreign-key violation into the typed block
// the grace sweep backs off on, and returns any other error unchanged. Postgres
// reports the referencing table for both sides of the key.
func appPurgeForeignKeyError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == pgerrcode.ForeignKeyViolation {
		return &AppPurgeBlockedError{Reason: AppPurgeBlockedForeignKey, Constraint: pgErr.ConstraintName, Table: pgErr.TableName}
	}
	return err
}
