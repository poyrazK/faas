package state

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// AcquireEdgeRuleMutationLocks holds all sorted app locks on ONE direct
// connection. One connection per app would exhaust the pool before a large
// environment could persist its intent. A contended batch releases its partial
// lock set and its connection before waiting, preserving the holder's ability
// to finish. It uses the same advisory keys as ordinary edge-rule mutations.
func (s *PgStore) AcquireEdgeRuleMutationLocks(ctx context.Context, apps []string) (func(context.Context), error) {
	apps, err := canonicalGitOpsEffectNames(apps)
	if err != nil {
		return nil, err
	}
	if len(apps) == 0 {
		return func(context.Context) {}, nil
	}
	for {
		conn, err := db.DirectPool(s.pool).Acquire(ctx)
		if err != nil {
			return nil, fmt.Errorf("acquire edge-rule batch connection: %w", err)
		}
		locked := []string{}
		for _, app := range apps {
			acquired, err := sqlc.New().TryEdgeRuleMutationLock(ctx, conn, app)
			if err != nil {
				closeGitOpsLockConnection(ctx, conn)
				return nil, fmt.Errorf("acquire edge-rule batch lock: %w", err)
			}
			if !acquired {
				break
			}
			locked = append(locked, app)
		}
		release := releaseGitOpsBatchLocks(conn, locked)
		if len(locked) == len(apps) {
			return release, nil
		}
		release(ctx)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func closeGitOpsLockConnection(ctx context.Context, conn *pgxpool.Conn) {
	closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	_ = conn.Hijack().Close(closeCtx)
}

func releaseGitOpsBatchLocks(conn *pgxpool.Conn, apps []string) func(context.Context) {
	var once sync.Once
	return func(ctx context.Context) {
		once.Do(func() {
			unlockCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
			defer cancel()
			for i := len(apps) - 1; i >= 0; i-- {
				unlocked, err := sqlc.New().ReleaseEdgeRuleMutationLock(unlockCtx, conn, apps[i])
				if err != nil || !unlocked {
					closeGitOpsLockConnection(ctx, conn)
					return
				}
			}
			conn.Release()
		})
	}
}
