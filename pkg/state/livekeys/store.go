// Package livekeys stores the live-query keys of managed databases
// (ADR-20261016151947). apid writes them; livequeryd reads its database's key ring.
// Secrets are sealed before they reach this package, to livequeryd's age
// recipient, so neither the store nor apid can read one back.
package livekeys

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/state/livekeys/sqlc"
)

// SealNamespace tags a sealed live secret; livequeryd opens no other blob.
const SealNamespace = "livequery"

var (
	// ErrNotFound means the account has no such live database.
	ErrNotFound = errors.New("livekeys: database not found")
	// ErrEnabled means the database already has live queries.
	ErrEnabled = errors.New("livekeys: live queries already enabled")
	// ErrDisabled means the database has no live queries to rotate.
	ErrDisabled = errors.New("livekeys: live queries not enabled")
)

// Key describes one key without its secret.
type Key struct {
	KID       string
	CreatedAt time.Time
	// RetiresAt is zero for the active key.
	RetiresAt time.Time
}

// SealedKey is one key of a database's ring, sealed to Recipient.
type SealedKey struct {
	KID       string
	Sealed    []byte
	Recipient string
	RetiresAt time.Time
}

// NewKey is a key to store.
type NewKey struct {
	KID       string
	Sealed    []byte
	Recipient string
}

// Route is one live key an app may route: a key of a database bound to it,
// with the logins its live queries read as there ("" when the app has no
// such binding).
type Route struct {
	KID                string
	DatabaseID         string
	BackendID          string
	BackendFingerprint string
	SealedRole         string // the app's read-only, else read-write, binding login
	DataAPIRole        string // the app's Data API binding login
}

// Login is the login livequeryd decodes a live database with: its
// connection string sealed to livequeryd, the provider's name for it, and
// the replication slot it decodes.
type Login struct {
	Sealed    []byte
	Recipient string
	Identity  string
	Slot      string
}

// LiveDatabase is a database livequeryd serves.
type LiveDatabase struct {
	DatabaseID string
	BackendID  string
	Sealed     []byte
	Recipient  string
	Slot       string
}

// Store reads and writes managed_postgres_live_keys and
// managed_postgres_live_logins.
type Store struct {
	pool *pgxpool.Pool
	q    *sqlc.Queries
}

// New returns a store over pool.
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool, q: sqlc.New()} }

// Enable stores a database's first key and, when the database's provider
// issued one, its login, in one transaction.
func (s *Store) Enable(ctx context.Context, accountID, databaseID string, key NewKey, login *Login) error {
	return s.locked(ctx, accountID, databaseID, func(tx pgx.Tx) error {
		active, err := s.q.HasActiveLiveKey(ctx, tx, databaseID)
		if err != nil {
			return fmt.Errorf("livekeys: read active key: %w", err)
		}
		if active {
			return ErrEnabled
		}
		// Retiring keys exist only beside an active one (Disable removes
		// every key), so the database has no keys here.
		if err := s.insert(ctx, tx, accountID, databaseID, key); err != nil {
			return err
		}
		if login == nil {
			return nil
		}
		err = s.q.UpsertLiveLogin(ctx, tx, sqlc.UpsertLiveLoginParams{DatabaseID: databaseID, AccountID: accountID,
			SealedLogin: login.Sealed, Recipient: login.Recipient, ProviderIdentityID: login.Identity, Slot: login.Slot})
		if err != nil {
			return fmt.Errorf("livekeys: store login: %w", err)
		}
		return nil
	})
}

// LoginOf returns a database's login, without its sealed connection string,
// or ok false when it has none.
func (s *Store) LoginOf(ctx context.Context, accountID, databaseID string) (Login, bool, error) {
	rows, err := s.q.LiveLoginOf(ctx, s.pool, sqlc.LiveLoginOfParams{DatabaseID: databaseID, AccountID: accountID})
	if err != nil {
		return Login{}, false, fmt.Errorf("livekeys: read login: %w", err)
	}
	if len(rows) == 0 {
		return Login{}, false, nil
	}
	return Login{Identity: rows[0].ProviderIdentityID, Slot: rows[0].Slot}, true, nil
}

// LiveDatabases lists the live databases of the given backends that have a
// login, by database ID.
func (s *Store) LiveDatabases(ctx context.Context, backendIDs []string) ([]LiveDatabase, error) {
	rows, err := s.q.LiveDatabases(ctx, s.pool, backendIDs)
	if err != nil {
		return nil, fmt.Errorf("livekeys: list live databases: %w", err)
	}
	out := make([]LiveDatabase, 0, len(rows))
	for _, r := range rows {
		out = append(out, LiveDatabase{DatabaseID: r.DatabaseID, BackendID: r.BackendID, Sealed: r.SealedLogin, Recipient: r.Recipient, Slot: r.Slot})
	}
	return out, nil
}

// Rotate makes key the active key. The previous active key retires after
// overlap, or at once when overlap is zero; any older key is removed, so a
// database holds at most two keys.
func (s *Store) Rotate(ctx context.Context, accountID, databaseID string, key NewKey, overlap time.Duration) error {
	return s.locked(ctx, accountID, databaseID, func(tx pgx.Tx) error {
		if err := s.q.DeleteRetiringLiveKeys(ctx, tx, databaseID); err != nil {
			return fmt.Errorf("livekeys: drop retiring keys: %w", err)
		}
		var (
			n   int64
			err error
		)
		if overlap > 0 {
			n, err = s.q.RetireActiveLiveKey(ctx, tx, sqlc.RetireActiveLiveKeyParams{OverlapSeconds: overlap.Seconds(), DatabaseID: databaseID})
		} else {
			n, err = s.q.DeleteActiveLiveKey(ctx, tx, databaseID)
		}
		if err != nil {
			return fmt.Errorf("livekeys: retire active key: %w", err)
		}
		if n == 0 {
			return ErrDisabled
		}
		return s.insert(ctx, tx, accountID, databaseID, key)
	})
}

// Disable removes every key of a database, revoking its outstanding seals,
// and its login. It reports whether there were any keys.
func (s *Store) Disable(ctx context.Context, accountID, databaseID string) (bool, error) {
	var removed bool
	err := s.locked(ctx, accountID, databaseID, func(tx pgx.Tx) error {
		n, err := s.q.DeleteLiveKeys(ctx, tx, sqlc.DeleteLiveKeysParams{DatabaseID: databaseID, AccountID: accountID})
		if err != nil {
			return fmt.Errorf("livekeys: delete keys: %w", err)
		}
		removed = n > 0
		if _, err := s.q.DeleteLiveLogin(ctx, tx, sqlc.DeleteLiveLoginParams{DatabaseID: databaseID, AccountID: accountID}); err != nil {
			return fmt.Errorf("livekeys: delete login: %w", err)
		}
		return nil
	})
	return removed, err
}

// Keys lists a database's unretired keys, newest first.
func (s *Store) Keys(ctx context.Context, accountID, databaseID string) ([]Key, error) {
	rows, err := s.q.ListLiveKeys(ctx, s.pool, sqlc.ListLiveKeysParams{DatabaseID: databaseID, AccountID: accountID})
	if err != nil {
		return nil, fmt.Errorf("livekeys: list keys: %w", err)
	}
	keys := make([]Key, 0, len(rows))
	for _, r := range rows {
		keys = append(keys, Key{KID: r.Kid, CreatedAt: r.CreatedAt.Time, RetiresAt: timeOrZero(r.RetiresAt)})
	}
	return keys, nil
}

// Ring returns a database's unretired keys, sealed, oldest first. A deleted
// or deleting database has none.
func (s *Store) Ring(ctx context.Context, databaseID string) ([]SealedKey, error) {
	rows, err := s.q.LiveKeyRing(ctx, s.pool, databaseID)
	if err != nil {
		return nil, fmt.Errorf("livekeys: read key ring: %w", err)
	}
	keys := make([]SealedKey, 0, len(rows))
	for _, r := range rows {
		keys = append(keys, SealedKey{KID: r.Kid, Sealed: r.SealedSecret, Recipient: r.Recipient, RetiresAt: timeOrZero(r.RetiresAt)})
	}
	return keys, nil
}

// AppRoutes lists the unretired keys of live databases the app is bound to
// (a ready or retiring binding), ordered by key ID.
func (s *Store) AppRoutes(ctx context.Context, accountID, appID string) ([]Route, error) {
	rows, err := s.q.AppLiveRoutes(ctx, s.pool, sqlc.AppLiveRoutesParams{AppID: appID, AccountID: accountID})
	if err != nil {
		return nil, fmt.Errorf("livekeys: list app routes: %w", err)
	}
	routes := make([]Route, 0, len(rows))
	for _, r := range rows {
		routes = append(routes, Route{KID: r.Kid, DatabaseID: r.DatabaseID, BackendID: r.BackendID, BackendFingerprint: r.BackendFingerprint,
			SealedRole: r.SealedRole, DataAPIRole: r.DataApiRole})
	}
	return routes, nil
}

// DatabaseApps lists the apps bound to a database.
func (s *Store) DatabaseApps(ctx context.Context, databaseID string) ([]string, error) {
	apps, err := s.q.LiveDatabaseApps(ctx, s.pool, databaseID)
	if err != nil {
		return nil, fmt.Errorf("livekeys: list bound apps: %w", err)
	}
	return apps, nil
}

// locked runs fn in a transaction holding the database row, so enabling,
// rotating and disabling one database serialize.
func (s *Store) locked(ctx context.Context, accountID, databaseID string, fn func(pgx.Tx) error) error {
	return pgx.BeginTxFunc(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := s.q.LockLiveDatabase(ctx, tx, sqlc.LockLiveDatabaseParams{DatabaseID: databaseID, AccountID: accountID}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("livekeys: lock database: %w", err)
		}
		return fn(tx)
	})
}

func (s *Store) insert(ctx context.Context, tx pgx.Tx, accountID, databaseID string, key NewKey) error {
	err := s.q.InsertLiveKey(ctx, tx, sqlc.InsertLiveKeyParams{
		Kid: key.KID, AccountID: accountID, DatabaseID: databaseID, SealedSecret: key.Sealed, Recipient: key.Recipient,
	})
	if err != nil {
		return fmt.Errorf("livekeys: insert key: %w", err)
	}
	return nil
}

func timeOrZero(t pgtype.Timestamptz) time.Time {
	if !t.Valid {
		return time.Time{}
	}
	return t.Time
}
