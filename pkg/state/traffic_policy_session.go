// adr: 375
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
	conn, err := db.DirectPool(s.pool).Acquire(ctx)
	if err != nil {
		return nil, nil, false, fmt.Errorf("state: acquire traffic policy connection: %w", err)
	}
	keys := []string{"gregale.traffic.account.v1:" + account.String()}
	if global {
		keys = append([]string{globalTrafficRoutesLock}, keys...)
	}
	locked := make([]string, 0, len(keys))
	for _, key := range keys {
		acquired, err := sqlc.New().TryLockTrafficPolicySession(ctx, conn, key)
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
		locked = append(locked, key)
	}
	return conn, releaseTrafficPolicySession(conn, locked), false, nil
}

func closeTrafficPolicySession(ctx context.Context, conn *pgxpool.Conn) {
	bounded, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.TrafficPolicyAnalysisTimeout)
	defer cancel()
	_ = conn.Hijack().Close(bounded)
}

func releaseTrafficPolicySession(conn *pgxpool.Conn, keys []string) func(context.Context) {
	var once sync.Once
	return func(ctx context.Context) {
		once.Do(func() {
			bounded, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.TrafficPolicyAnalysisTimeout)
			defer cancel()
			for i := len(keys) - 1; i >= 0; i-- {
				unlocked, err := sqlc.New().UnlockTrafficPolicySession(bounded, conn, keys[i])
				if err != nil || !unlocked {
					closeTrafficPolicySession(ctx, conn)
					return
				}
			}
			conn.Release()
		})
	}
}
