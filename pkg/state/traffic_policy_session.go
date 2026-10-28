// adr: 570
package state

import (
	"context"
	"fmt"
	"sync"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

const globalTrafficRoutesLock = "gregale.traffic.global-routes.v1"

// Lock outside the transaction so its snapshot cannot predate the previous
// holder's commit. Waiters release all locks and the connection before retrying.
func (s *PgStore) tryAcquireTrafficPolicySession(ctx context.Context, account pgtype.UUID, global bool) (*pgxpool.Conn, func(context.Context), bool, error) {
	keys := []string{"gregale.traffic.account.v1:" + account.String()}
	if global {
		keys = append([]string{globalTrafficRoutesLock}, keys...)
	}
	return s.tryAcquireTrafficPolicySessionKeys(ctx, keys)
}

func (s *PgStore) tryAcquireTrafficPolicySessionKeys(ctx context.Context, keys []string) (*pgxpool.Conn, func(context.Context), bool, error) {
	locks := make([]trafficSessionLock, len(keys))
	for i, key := range keys {
		locks[i] = trafficSessionLock{Key: key}
	}
	return s.tryAcquireTrafficPolicySessionLocks(ctx, locks)
}

// Shared keys admit other shared holders; exclusive keys admit no one.
func (s *PgStore) tryAcquireTrafficPolicySessionLocks(ctx context.Context, locks []trafficSessionLock) (*pgxpool.Conn, func(context.Context), bool, error) {
	conn, err := db.DirectPool(s.pool.Pool).Acquire(ctx)
	if err != nil {
		return nil, nil, false, fmt.Errorf("state: acquire traffic policy connection: %w", err)
	}
	locked := make([]trafficSessionLock, 0, len(locks))
	for _, lock := range locks {
		var acquired bool
		if lock.Shared {
			acquired, err = sqlc.New().TryLockTrafficPolicySessionShared(ctx, conn, lock.Key)
		} else {
			acquired, err = sqlc.New().TryLockTrafficPolicySession(ctx, conn, lock.Key)
		}
		if err != nil {
			// A canceled query may have granted the session lock. Close the
			// session even if the client never received the successful reply.
			closeTrafficPolicySession(ctx, conn)
			return nil, nil, false, fmt.Errorf("state: acquire traffic policy session lock: %w", err)
		}
		if !acquired {
			releaseTrafficPolicySession(conn, locked)(ctx)
			return nil, nil, true, nil
		}
		locked = append(locked, lock)
	}
	return conn, releaseTrafficPolicySession(conn, locked), false, nil
}

func closeTrafficPolicySession(ctx context.Context, conn *pgxpool.Conn) {
	bounded, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.TrafficPolicyAnalysisTimeout)
	defer cancel()
	_ = conn.Hijack().Close(bounded)
}

func releaseTrafficPolicySession(conn *pgxpool.Conn, locks []trafficSessionLock) func(context.Context) {
	var once sync.Once
	return func(ctx context.Context) {
		once.Do(func() {
			bounded, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.TrafficPolicyAnalysisTimeout)
			defer cancel()
			for i := len(locks) - 1; i >= 0; i-- {
				var unlocked bool
				var err error
				if locks[i].Shared {
					unlocked, err = sqlc.New().UnlockTrafficPolicySessionShared(bounded, conn, locks[i].Key)
				} else {
					unlocked, err = sqlc.New().UnlockTrafficPolicySession(bounded, conn, locks[i].Key)
				}
				if err != nil || !unlocked {
					closeTrafficPolicySession(ctx, conn)
					return
				}
			}
			conn.Release()
		})
	}
}
