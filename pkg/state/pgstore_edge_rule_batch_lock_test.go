//go:build !no_pg

package state_test

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgStoreEdgeRuleBatchLocksUseOneConnectionAndInteroperateWithSingleAppWriters(t *testing.T) {
	base := pgtest.Open(t)
	cfg := base.Config()
	cfg.MaxConns = 2
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	store := state.NewPgStore(pool)
	apps := make([]string, 32)
	for i := range apps {
		apps[i] = uuid.NewString()
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	release, err := store.AcquireEdgeRuleMutationLocks(ctx, append(apps, apps[0]))
	if err != nil {
		t.Fatal("batch larger than the pool could not acquire", err)
	}
	defer release(ctx)
	var one int
	if err := pool.QueryRow(ctx, "select 1").Scan(&one); err != nil {
		t.Fatal("batch holder exhausted its intent connection", err)
	}
	contenderCtx, stop := context.WithTimeout(ctx, 100*time.Millisecond)
	defer stop()
	if unexpected, err := store.AcquireEdgeRuleMutationLock(contenderCtx, apps[0]); err == nil {
		unexpected(ctx)
		t.Fatal("ordinary API writer bypassed the batch lock")
	} else if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	release(ctx)
	release(ctx)
	for _, app := range apps {
		unlock, err := store.AcquireEdgeRuleMutationLock(ctx, app)
		if err != nil {
			t.Fatal("batch release leaked an app lock", err)
		}
		unlock(ctx)
	}
}

func TestPgStoreEdgeRuleBatchCanceledContenderReleasesPartialLocks(t *testing.T) {
	pool := pgtest.Open(t)
	store := state.NewPgStore(pool)
	apps := []string{uuid.NewString(), uuid.NewString()}
	sort.Strings(apps)
	ctx := t.Context()
	holder, err := store.AcquireEdgeRuleMutationLock(ctx, apps[1])
	if err != nil {
		t.Fatal(err)
	}
	defer holder(ctx)
	waitCtx, stop := context.WithTimeout(ctx, 120*time.Millisecond)
	defer stop()
	if release, err := store.AcquireEdgeRuleMutationLocks(waitCtx, apps); err == nil {
		release(ctx)
		t.Fatal("contended batch acquired every lock")
	} else if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	probeCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	probe, err := store.AcquireEdgeRuleMutationLock(probeCtx, apps[0])
	if err != nil {
		t.Fatal("cancelled batch leaked its uncontended prefix lock", err)
	}
	probe(ctx)
	holder(ctx)
	release, err := store.AcquireEdgeRuleMutationLocks(probeCtx, apps)
	if err != nil {
		t.Fatal("batch could not recover after cancellation", err)
	}
	release(ctx)
}
