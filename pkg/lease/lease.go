package lease

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/dispatch"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ErrLeaseConflict aliases dispatch.ErrLeaseConflict so callers
// who already type-switch on dispatch.ErrLeaseConflict get the
// primitive's errors too. The pkg/lease primitive re-uses the
// dispatch sentinel rather than declaring its own — the two
// surfaces share a single error contract.
var ErrLeaseConflict = dispatch.ErrLeaseConflict

// Lease aliases dispatch.Lease for the same reason as
// ErrLeaseConflict. Keeps a single type vocabulary across the
// dispatch / lease packages.
type Lease = dispatch.Lease

// Manager is the per-table CAS-with-token primitive. Construct
// once at package init with the table + column names; pass around
// as a dependency. Stateless and safe for concurrent use.
type Manager struct {
	DB            sqlc.DBTX
	Table         string
	IDColumn      string
	StateColumn   string
	LeaseColumn   string
	ExpiresColumn string
	Now           func() time.Time
}

// Compile-time guarantee *Manager satisfies dispatch.Leaser[string]
// so any future signature drift between the two surfaces in code
// review, not at runtime in a downstream drain.
var _ dispatch.Leaser[string] = (*Manager)(nil)

// New constructs a Manager with the given column names. now is the
// clock; inject time.Now in production, a fixed function in tests.
// Panics if any column or table name is empty or contains a
// character outside [A-Za-z0-9_] — the names are interpolated
// directly into SQL, and a typo would be a SQL-injection vector.
func New(db sqlc.DBTX, table, idCol, stateCol, leaseCol, expiresCol string, now func() time.Time) *Manager {
	for _, n := range []string{table, idCol, stateCol, leaseCol, expiresCol} {
		if n == "" {
			panic("lease: New: empty column or table name")
		}
		if !isSafeIdent(n) {
			panic(fmt.Sprintf("lease: New: unsafe SQL identifier %q", n))
		}
	}
	if now == nil {
		panic("lease: New: nil clock")
	}
	return &Manager{
		DB:            db,
		Table:         table,
		IDColumn:      idCol,
		StateColumn:   stateCol,
		LeaseColumn:   leaseCol,
		ExpiresColumn: expiresCol,
		Now:           now,
	}
}

// isSafeIdent allows only ASCII letters, digits, and underscores.
// Defends against SQL-injection in the column-name interpolation
// (caller-supplied values would be the attack vector).
func isSafeIdent(s string) bool {
	for _, r := range s {
		switch {
		case r >= 'A' && r <= 'Z':
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '_':
		default:
			return false
		}
	}
	return true
}

// Acquire claims (id, expectedState) for ttl, minting a fresh
// UUIDv4 token. The CAS predicate is:
//
//	state = expectedState
//	AND (lease_token IS NULL OR lease_expires_at < NOW())
//
// On success the row's lease_token is set to the new UUID and
// lease_expires_at to Now()+ttl. Returns the Lease on success or
// ErrLeaseConflict on predicate failure (state mismatch or lease
// still live). The minted token is opaque; the caller is expected
// to pass it back to Renew/Release without modification.
func (m *Manager) Acquire(ctx context.Context, id, expectedState string, ttl time.Duration) (dispatch.Lease, error) {
	token := uuid.NewString()
	expiresAt := m.Now().Add(ttl)

	q := fmt.Sprintf(`update %s
		set %s = $2,
		    %s = $3
		where %s = $1
		  and %s = $4
		  and (%s is null or %s < now())
		returning %s, %s`,
		m.Table, m.LeaseColumn, m.ExpiresColumn,
		m.IDColumn, m.StateColumn,
		m.LeaseColumn, m.ExpiresColumn,
		m.LeaseColumn, m.ExpiresColumn)

	var got dispatch.Lease
	err := m.DB.QueryRow(ctx, q, id, token, expiresAt, expectedState).
		Scan(&got.Token, &got.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return dispatch.Lease{}, dispatch.ErrLeaseConflict
	}
	if err != nil {
		return dispatch.Lease{}, fmt.Errorf("lease: acquire %s=%s: %w", m.Table, id, err)
	}
	return got, nil
}

// Renew extends the holder's lease by ttl. The CAS predicate is
// strict on lease_token=$token — a stolen-lease path cannot be
// silently extended by the new holder because the new holder
// doesn't know the previous token. Returns the updated Lease or
// ErrLeaseConflict.
func (m *Manager) Renew(ctx context.Context, id, token string, ttl time.Duration) (dispatch.Lease, error) {
	expiresAt := m.Now().Add(ttl)

	q := fmt.Sprintf(`update %s
		set %s = $3
		where %s = $1
		  and %s = $2
		returning %s, %s`,
		m.Table, m.ExpiresColumn,
		m.IDColumn, m.LeaseColumn,
		m.LeaseColumn, m.ExpiresColumn)

	var got dispatch.Lease
	err := m.DB.QueryRow(ctx, q, id, token, expiresAt).
		Scan(&got.Token, &got.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return dispatch.Lease{}, dispatch.ErrLeaseConflict
	}
	if err != nil {
		return dispatch.Lease{}, fmt.Errorf("lease: renew %s=%s: %w", m.Table, id, err)
	}
	return got, nil
}

// Release drops the holder's lease. The CAS predicate is strict
// on lease_token=$token — Release is intentionally strict so a
// stolen-lease path can't accidentally clear a live holder's
// lease. Returns ErrLeaseConflict on token mismatch.
func (m *Manager) Release(ctx context.Context, id, token string) error {
	q := fmt.Sprintf(`update %s
		set %s = null,
		    %s = null
		where %s = $1
		  and %s = $2`,
		m.Table, m.LeaseColumn, m.ExpiresColumn,
		m.IDColumn, m.LeaseColumn)

	tag, err := m.DB.Exec(ctx, q, id, token)
	if err != nil {
		return fmt.Errorf("lease: release %s=%s: %w", m.Table, id, err)
	}
	if tag.RowsAffected() == 0 {
		return dispatch.ErrLeaseConflict
	}
	return nil
}
