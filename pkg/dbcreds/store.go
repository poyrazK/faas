// Package dbcreds stores the SCRAM verifier of each native managed-database
// role (ADR-1027, database_role_verifiers). apid's driver writes verifiers
// when it sets a role's password inside the VM; pgproxyd reads them to
// authenticate clients. No password is ever stored.
package dbcreds

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/pgscram"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ErrNoVerifier reports that a database has no such role.
var ErrNoVerifier = errors.New("dbcreds: no verifier for role")

// VerifierStore reads and writes database_role_verifiers. apid's native
// driver writes; pgproxyd reads through a short cache.
type VerifierStore struct {
	pool *pgxpool.Pool
	q    *sqlc.Queries
	ttl  time.Duration
	now  func() time.Time

	mu    sync.Mutex
	cache map[string]cachedVerifier
}

type cachedVerifier struct {
	v       pgscram.Verifier
	expires time.Time
}

// NewVerifierStore caches positive lookups for ttl; misses are never
// cached, so a new role works on its first connection.
func NewVerifierStore(pool *pgxpool.Pool, ttl time.Duration) (*VerifierStore, error) {
	if pool == nil {
		return nil, errors.New("dbcreds: nil pool")
	}
	return &VerifierStore{pool: pool, q: sqlc.New(), ttl: ttl, now: time.Now, cache: map[string]cachedVerifier{}}, nil
}

func databaseUUID(id string) (pgtype.UUID, error) {
	parsed, err := uuid.Parse(id)
	if err != nil {
		return pgtype.UUID{}, ErrNoVerifier
	}
	return pgtype.UUID{Bytes: parsed, Valid: true}, nil
}

func (s *VerifierStore) Verifier(ctx context.Context, databaseID, role string) (pgscram.Verifier, error) {
	key := databaseID + "\x00" + role
	s.mu.Lock()
	if hit, ok := s.cache[key]; ok && s.now().Before(hit.expires) {
		s.mu.Unlock()
		return hit.v, nil
	}
	s.mu.Unlock()
	id, err := databaseUUID(databaseID)
	if err != nil {
		return pgscram.Verifier{}, err
	}
	raw, err := s.q.GetDatabaseRoleVerifier(ctx, s.pool, sqlc.GetDatabaseRoleVerifierParams{DatabaseID: id, RoleName: role})
	if errors.Is(err, pgx.ErrNoRows) {
		return pgscram.Verifier{}, ErrNoVerifier
	}
	if err != nil {
		return pgscram.Verifier{}, fmt.Errorf("dbcreds: verifier lookup: %w", err)
	}
	v, err := pgscram.ParseVerifier(raw)
	if err != nil {
		return pgscram.Verifier{}, fmt.Errorf("dbcreds: stored verifier: %w", err)
	}
	if s.ttl > 0 {
		s.mu.Lock()
		s.cache[key] = cachedVerifier{v: v, expires: s.now().Add(s.ttl)}
		s.mu.Unlock()
	}
	return v, nil
}

// Forget drops a role's cached verifier, so the next lookup reads the
// current one. pgproxyd calls it after a failed login: after a rotation the
// cached verifier is stale until its TTL otherwise.
func (s *VerifierStore) Forget(databaseID, role string) {
	s.mu.Lock()
	delete(s.cache, databaseID+"\x00"+role)
	s.mu.Unlock()
}

// Put stores a role's verifier.
func (s *VerifierStore) Put(ctx context.Context, databaseID, role string, v pgscram.Verifier) error {
	id, err := databaseUUID(databaseID)
	if err != nil {
		return err
	}
	return s.q.UpsertDatabaseRoleVerifier(ctx, s.pool, sqlc.UpsertDatabaseRoleVerifierParams{DatabaseID: id, RoleName: role, Verifier: v.String()})
}

// Delete removes a role's verifier; the proxy refuses the role at once on
// cache miss and within the cache TTL otherwise.
func (s *VerifierStore) Delete(ctx context.Context, databaseID, role string) error {
	id, err := databaseUUID(databaseID)
	if err != nil {
		return err
	}
	_, err = s.q.DeleteDatabaseRoleVerifier(ctx, s.pool, sqlc.DeleteDatabaseRoleVerifierParams{DatabaseID: id, RoleName: role})
	return err
}

// DeleteDatabase removes every verifier of a database.
func (s *VerifierStore) DeleteDatabase(ctx context.Context, databaseID string) error {
	id, err := databaseUUID(databaseID)
	if err != nil {
		return err
	}
	_, err = s.q.DeleteDatabaseRoleVerifiers(ctx, s.pool, id)
	return err
}
