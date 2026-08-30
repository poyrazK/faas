package dispatch

import (
	"context"
	"errors"
	"time"
)

// ErrLeaseConflict is returned when a CAS-with-token claim fails
// because the row's state or lease token did not match the
// expectation. pkg/lease (PR-D) returns this; the two schedd
// drains map it onto the producer's existing sentinel
// (state.ErrConflict for trigger_records, the inline
// RowsAffected==0 check on invocations at pgstore.go:9765).
var ErrLeaseConflict = errors.New("dispatch: lease conflict")

// Leaser is the contract every per-table lease manager satisfies.
// T is the row id type (string for uuid PKs today; PR-D may add
// typed ids later).
//
// The interface is generic on T so a future typed-id migration
// (issue #1103, deferred) doesn't have to break the call sites.
// Today all implementations are table-name parameterized; the
// generic parameter is the row id type.
//
// All methods are idempotent under retries: Acquire may be called
// repeatedly with the same (id, ttl) and the second call either
// returns the existing valid lease or ErrLeaseConflict if the
// row's state changed.
type Leaser[T ~string] interface {
	// Acquire claims (id, expectedState) for ttl. Returns the
	// fresh Lease on success, or ErrLeaseConflict if the row is
	// not in expectedState. If the row is already leased by
	// another holder whose lease has expired, Acquire steals it.
	Acquire(ctx context.Context, id T, expectedState string, ttl time.Duration) (Lease, error)

	// Renew extends the holder's lease by ttl. Returns
	// ErrLeaseConflict if the token does not match (the lease
	// was stolen or expired and re-acquired by someone else).
	Renew(ctx context.Context, id T, token string, ttl time.Duration) (Lease, error)

	// Release drops the holder's lease. Returns ErrLeaseConflict
	// if the token does not match — Release is intentionally
	// strict so a stolen-lease path can't accidentally clear a
	// live holder's lease.
	Release(ctx context.Context, id T, token string) error
}
