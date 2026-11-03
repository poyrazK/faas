package managedpostgres

import (
	"context"
	"fmt"
	"time"
)

// ADR-20261103091657: a database with live restore copies can be deleted. A
// copy lives inside its source's provider resource (a Neon child branch), so
// the source's provider resource stays until the last copy is gone. Until
// then the source waits in deleting, past any undo window, held so nothing
// connects to it, and it no longer counts toward its owner's database limit.
// Its usage is still metered: the copies' usage is recorded against it.

// RestoreCopiesRemainCode is the last_error_code of a deleted database kept
// for its restore copies.
const RestoreCopiesRemainCode = "restore_copies_remain"

// restoreCopiesRetry is how often a kept source checks for its copies.
const restoreCopiesRetry = time.Hour

// RestoreCopiesStore is the store capability the delete path needs.
type RestoreCopiesStore interface {
	// HasRestoreCopies reports whether an undeleted database was restored
	// from databaseID.
	HasRestoreCopies(ctx context.Context, databaseID string) (bool, error)
}

var (
	// errDatabaseHasBindings and errDatabaseCloneHold name why a delete is
	// refused; a bare conflict read like lease contention.
	errDatabaseHasBindings = fmt.Errorf("%w: the database still has bindings; delete them first (gregale postgres bindings list)", ErrConflict)
	errDatabaseCloneHold   = fmt.Errorf("%w: an environment clone is copying the database; delete it once the clone finishes", ErrConflict)
)

// CountsTowardLimit reports whether the database takes one of its owner's
// databases. A delete that can no longer be undone frees it, including a
// source kept for its restore copies.
func (d Database) CountsTowardLimit() bool {
	return d.State != StateDeleted && (d.State != StateDeleting || !d.PurgeAfter.IsZero())
}

// keepForRestoreCopies runs after a delete claim. A database with restore
// copies is held, released to wait for them, and returned with kept set; the
// caller must not touch its provider resource.
func (s *Service) keepForRestoreCopies(ctx context.Context, database Database) (Database, bool, error) {
	store, ok := s.store.(RestoreCopiesStore)
	if !ok || database.ProviderResourceID == "" {
		return Database{}, false, nil
	}
	copies, err := store.HasRestoreCopies(ctx, database.ID)
	if err != nil {
		return Database{}, true, s.releaseKnownError(ctx, database, StateDeleting, "", err, s.pollInterval)
	}
	if !copies {
		return Database{}, false, nil
	}
	// A scheduled delete is held already; an immediate one is held here, once.
	if database.LastErrorCode != RestoreCopiesRemainCode {
		if err := s.deletionHold(ctx, database, true); err != nil {
			return Database{}, true, s.releaseKnownError(ctx, database, StateDeleting, "deletion_hold_failed", err, retryDelay(err, database.AttemptCount))
		}
	}
	if err := s.release(ctx, database.ID, database.LeaseToken, StateDeleting, RestoreCopiesRemainCode, restoreCopiesRetry); err != nil {
		return Database{}, true, err
	}
	kept, err := s.store.Get(ctx, database.AccountID, database.ID)
	return kept, true, err
}
