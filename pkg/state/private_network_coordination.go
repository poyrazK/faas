package state

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"golang.org/x/sync/semaphore"
)

// PrivateNetworkCoordinator orders intent writes and complete reconciliation
// attempts across every controller. Callbacks must join their work before
// returning; the callback context and pinned connection expire on return.
type PrivateNetworkCoordinator interface {
	WithPrivateNetworkScope(context.Context, string, func(context.Context) error) error
}

func PrivateNetworkAttachmentScope(appID string) string {
	return privateNetworkScopeKey("attachment", appID)
}

func PrivateNetworkPeeringScope(accountID, region string) string {
	return privateNetworkScopeKey("peering", accountID, region)
}

func privateNetworkScopeKey(parts ...string) string {
	raw, _ := json.Marshal(parts)
	return string(raw)
}

func WithPrivateNetworkScope(ctx context.Context, store any, scope string, fn func(context.Context) error) error {
	coordinator, ok := store.(PrivateNetworkCoordinator)
	if !ok {
		return errors.New("state: private network coordination unavailable")
	}
	return coordinator.WithPrivateNetworkScope(ctx, scope, fn)
}

type privateNetworkScopeContextKey struct{}
type privateNetworkScopeLease struct {
	owner  any
	scope  string
	conn   *pgxpool.Conn
	active atomic.Bool
	parent *privateNetworkScopeLease
}

func existingPrivateNetworkScope(ctx context.Context, owner any, scope string) (bool, error) {
	for lease := privateNetworkScopeFrom(ctx); lease != nil; lease = lease.parent {
		if !lease.active.Load() {
			return false, errors.New("state: private network scope expired")
		}
		if lease.owner == owner && lease.scope == scope {
			return true, ctx.Err()
		}
	}
	return false, nil
}

func privateNetworkScopeFrom(ctx context.Context) *privateNetworkScopeLease {
	lease, _ := ctx.Value(privateNetworkScopeContextKey{}).(*privateNetworkScopeLease)
	return lease
}

func nestedPrivateNetworkScope(ctx context.Context, owner any, scope string) (*privateNetworkScopeLease, error) {
	for parent := privateNetworkScopeFrom(ctx); parent != nil; parent = parent.parent {
		if parent.owner != owner {
			continue
		}
		var prior, next []string
		if json.Unmarshal([]byte(parent.scope), &prior) != nil || json.Unmarshal([]byte(scope), &next) != nil || len(prior) == 0 || len(next) == 0 || prior[0] != "peering" || next[0] != "attachment" {
			return nil, errors.New("state: private network lock order must be peering then attachment")
		}
		return parent, nil
	}
	return nil, nil
}

func runPrivateNetworkScope(ctx context.Context, lease *privateNetworkScopeLease, fn func(context.Context) error) error {
	lease.parent = privateNetworkScopeFrom(ctx)
	lease.active.Store(true)
	defer lease.active.Store(false)
	scoped, cancel := context.WithCancel(ctx)
	defer cancel()
	return fn(context.WithValue(scoped, privateNetworkScopeContextKey{}, lease))
}

func (m *MemStore) WithPrivateNetworkScope(ctx context.Context, scope string, fn func(context.Context) error) error {
	if scope == "" || fn == nil {
		return ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if held, err := existingPrivateNetworkScope(ctx, m, scope); held || err != nil {
		if err != nil {
			return err
		}
		return fn(ctx)
	}
	if _, err := nestedPrivateNetworkScope(ctx, m, scope); err != nil {
		return err
	}
	m.mu.Lock()
	if m.privateNetworkScopes == nil {
		m.privateNetworkScopes = make(map[string]*semaphore.Weighted)
	}
	gate := m.privateNetworkScopes[scope]
	if gate == nil {
		gate = semaphore.NewWeighted(1)
		m.privateNetworkScopes[scope] = gate
	}
	m.mu.Unlock()
	if err := gate.Acquire(ctx, 1); err != nil {
		return err
	}
	defer gate.Release(1)
	return runPrivateNetworkScope(ctx, &privateNetworkScopeLease{owner: m, scope: scope}, fn)
}

func (s *PgStore) WithPrivateNetworkScope(ctx context.Context, scope string, fn func(context.Context) error) error {
	if scope == "" || fn == nil {
		return ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.pool == nil {
		return errors.New("state: private network pool unavailable")
	}
	if held, err := existingPrivateNetworkScope(ctx, s.pool.Pool, scope); held || err != nil {
		if err != nil {
			return err
		}
		return fn(ctx)
	}
	parent, err := nestedPrivateNetworkScope(ctx, s.pool.Pool, scope)
	if err != nil {
		return err
	}
	if parent != nil {
		return s.withNestedPrivateNetworkScope(ctx, parent, scope, fn)
	}
	q := sqlc.New()
	conn, release, err := s.acquireSessionAdvisoryLockConn(ctx, sessionAdvisoryLock{
		what: "private network scope", keyArg: scope, retryWait: 25 * time.Millisecond,
		tryQuery: q.TryPrivateNetworkScopeLock, unlockQuery: q.UnlockPrivateNetworkScope,
	})
	if err != nil {
		return err
	}
	defer release(ctx)
	return runPrivateNetworkScope(ctx, &privateNetworkScopeLease{owner: s.pool.Pool, scope: scope, conn: conn}, fn)
}

// All callback queries use the pinned direct connection, including ordinary
// Store reads. This keeps a one-connection pool usable without bypassing the
// existing organization access fence on its transactions.
func privateNetworkScopeConnection(ctx context.Context, pool *pgxpool.Pool) (*pgxpool.Conn, error) {
	var conn *pgxpool.Conn
	for lease := privateNetworkScopeFrom(ctx); lease != nil; lease = lease.parent {
		if !lease.active.Load() {
			return nil, errors.New("state: private network scope expired")
		}
		if lease.owner == pool && conn == nil {
			conn = lease.conn
		}
	}
	return conn, nil
}

func (s *PgStore) withNestedPrivateNetworkScope(ctx context.Context, parent *privateNetworkScopeLease, scope string, fn func(context.Context) error) (err error) {
	q := sqlc.New()
	for {
		locked, lockErr := q.TryPrivateNetworkScopeLock(ctx, parent.conn, scope)
		if lockErr != nil {
			closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
			_ = parent.conn.Conn().Close(closeCtx)
			cancel()
			parent.active.Store(false)
			return lockErr
		}
		if locked {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		ok, unlockErr := q.UnlockPrivateNetworkScope(unlockCtx, parent.conn, scope)
		if unlockErr != nil || !ok {
			_ = parent.conn.Conn().Close(unlockCtx)
			parent.active.Store(false)
			if unlockErr == nil {
				unlockErr = errors.New("state: private network scope unlock failed")
			}
			err = errors.Join(err, unlockErr)
		}
	}()
	return runPrivateNetworkScope(ctx, &privateNetworkScopeLease{owner: s.pool.Pool, scope: scope, conn: parent.conn}, fn)
}

func withPrivateNetworkScopeResult[T any](ctx context.Context, store PrivateNetworkCoordinator, scope string, fn func(context.Context) (T, error)) (out T, err error) {
	err = store.WithPrivateNetworkScope(ctx, scope, func(ctx context.Context) error {
		out, err = fn(ctx)
		return err
	})
	return out, err
}
