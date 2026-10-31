//go:build !no_pg

package state_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 091 — amendment 2026-10-27: a gateway's edge listener stamp is the
// database clock; a node is stale once its stamp is older than the bound, and
// a node that never reported is never stale.
func TestPgStoreEdgeListenerHeartbeat(t *testing.T) {
	store, ctx := pgStore(t)
	reported, silent := "edge-hb-"+uuid.NewString()[:8], "edge-hb-"+uuid.NewString()[:8]
	for _, name := range []string{reported, silent} {
		if _, err := store.CreateComputeNode(ctx, state.ComputeNode{Name: name, TargetURL: "tcp://10.0.0.1:9000", VPCPUs: 8, MemMB: 8192,
			MaxConcurrency: 10, AdmissionCeilingMB: 4096, VCPUBudget: 8, Active: true}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.TouchEdgeListenerHeartbeat(ctx, reported); err != nil {
		t.Fatal(err)
	}
	stale := func(after time.Duration) []string {
		t.Helper()
		nodes, err := store.StaleEdgeListenerNodes(ctx, after)
		if err != nil {
			t.Fatal(err)
		}
		return nodes
	}
	if slices.Contains(stale(time.Hour), reported) {
		t.Fatal("a fresh stamp is stale under a one-hour bound")
	}
	if got := stale(0); !slices.Contains(got, reported) || slices.Contains(got, silent) {
		t.Fatalf("stale(0) = %v, want %s and never %s", got, reported, silent)
	}
}

// adr: 091 — amendment 2026-10-27: one account's edge-rule mutations
// serialize across apps, another account's do not wait, and the app key
// still blocks an apid that only takes app locks.
func TestPgStoreEdgeRuleAccountMutationLocks(t *testing.T) {
	store := state.NewPgStore(pgtest.Open(t))
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	acctA, acctB := uuid.NewString(), uuid.NewString()
	shop, blog, other := uuid.NewString(), uuid.NewString(), uuid.NewString()
	release, err := store.AcquireEdgeRuleAccountMutationLocks(ctx, []string{acctA}, []string{shop})
	if err != nil {
		t.Fatal(err)
	}
	blocked := func(name string, acquire func(context.Context) (func(context.Context), error)) {
		t.Helper()
		waitCtx, stop := context.WithTimeout(ctx, 150*time.Millisecond)
		defer stop()
		if unlock, err := acquire(waitCtx); err == nil {
			unlock(ctx)
			t.Fatalf("%s did not wait for the held lock", name)
		} else if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
	}
	blocked("same account, another app", func(c context.Context) (func(context.Context), error) {
		return store.AcquireEdgeRuleAccountMutationLocks(c, []string{acctA}, []string{blog})
	})
	blocked("app-only writer", func(c context.Context) (func(context.Context), error) {
		return store.AcquireEdgeRuleMutationLock(c, shop)
	})
	otherRelease, err := store.AcquireEdgeRuleAccountMutationLocks(ctx, []string{acctB}, []string{other})
	if err != nil {
		t.Fatal("another account waited:", err)
	}
	otherRelease(ctx)
	release(ctx)
	release(ctx)
	next, err := store.AcquireEdgeRuleAccountMutationLocks(ctx, []string{acctA}, []string{blog})
	if err != nil {
		t.Fatal("release leaked the account lock:", err)
	}
	next(ctx)
}
