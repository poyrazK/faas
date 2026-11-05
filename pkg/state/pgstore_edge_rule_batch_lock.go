package state

import (
	"context"
	"fmt"
	"slices"
	"strings"
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
	return s.AcquireEdgeRuleAccountMutationLocks(ctx, nil, apps)
}

// AcquireEdgeRuleAccountMutationLocks is AcquireEdgeRuleMutationLocks with
// the accounts' locks taken first, in account ID order (ADR-091 amendment
// 2026-10-27). The app keys are unchanged, so an apid that predates account
// locks still contends with this one per app.
func (s *PgStore) AcquireEdgeRuleAccountMutationLocks(ctx context.Context, accounts, apps []string) (func(context.Context), error) {
	apps, err := canonicalGitOpsEffectNames(apps)
	if err != nil {
		return nil, err
	}
	keys := make([]edgeRuleLockKey, 0, len(accounts)+len(apps))
	for _, account := range sortedEdgeRuleLockIDs(accounts) {
		keys = append(keys, edgeRuleLockKey{id: account, account: true})
	}
	for _, app := range apps {
		keys = append(keys, edgeRuleLockKey{id: app})
	}
	if len(keys) == 0 {
		return func(context.Context) {}, nil
	}
	for {
		conn, err := db.DirectPool(s.pool.Pool).Acquire(ctx)
		if err != nil {
			return nil, fmt.Errorf("acquire edge-rule batch connection: %w", err)
		}
		locked := []edgeRuleLockKey{}
		for _, key := range keys {
			acquired, err := key.tryLock(ctx, conn)
			if err != nil {
				closeGitOpsLockConnection(ctx, conn)
				return nil, fmt.Errorf("acquire edge-rule batch lock: %w", err)
			}
			if !acquired {
				break
			}
			locked = append(locked, key)
		}
		release := releaseGitOpsBatchLocks(conn, locked)
		if len(locked) == len(keys) {
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

// edgeRuleLockKey is one advisory lock of an edge-rule mutation: an account
// key ('edge-rule-account:' || id) or an app key (the bare app id).
type edgeRuleLockKey struct {
	id      string
	account bool
}

func (k edgeRuleLockKey) tryLock(ctx context.Context, conn *pgxpool.Conn) (bool, error) {
	if k.account {
		return sqlc.New().TryEdgeRuleAccountMutationLock(ctx, conn, k.id)
	}
	return sqlc.New().TryEdgeRuleMutationLock(ctx, conn, k.id)
}

func (k edgeRuleLockKey) unlock(ctx context.Context, conn *pgxpool.Conn) (bool, error) {
	if k.account {
		return sqlc.New().ReleaseEdgeRuleAccountMutationLock(ctx, conn, k.id)
	}
	return sqlc.New().ReleaseEdgeRuleMutationLock(ctx, conn, k.id)
}

func sortedEdgeRuleLockIDs(ids []string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id = strings.TrimSpace(id); id != "" {
			out = append(out, id)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

func closeGitOpsLockConnection(ctx context.Context, conn *pgxpool.Conn) {
	closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	_ = conn.Hijack().Close(closeCtx)
}

func releaseGitOpsBatchLocks(conn *pgxpool.Conn, keys []edgeRuleLockKey) func(context.Context) {
	var once sync.Once
	return func(ctx context.Context) {
		once.Do(func() {
			unlockCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
			defer cancel()
			for i := len(keys) - 1; i >= 0; i-- {
				unlocked, err := keys[i].unlock(unlockCtx, conn)
				if err != nil || !unlocked {
					closeGitOpsLockConnection(ctx, conn)
					return
				}
			}
			conn.Release()
		})
	}
}
